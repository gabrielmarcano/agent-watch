package push

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNtfy_HeadersAndPriority(t *testing.T) {
	tests := []struct {
		name         string
		event        Event
		token        string
		wantPriority string
		wantTags     string
		wantAuth     string
	}{
		{
			name:         "blocked event with token",
			event:        EventBlocked,
			token:        "secret-ntfy-token",
			wantPriority: "5",
			wantTags:     "warning",
			wantAuth:     "Bearer secret-ntfy-token",
		},
		{
			name:         "done event without token",
			event:        EventDone,
			token:        "",
			wantPriority: "3",
			wantTags:     "white_check_mark",
			wantAuth:     "",
		},
		{
			name:         "digest event",
			event:        EventDigest,
			token:        "tok",
			wantPriority: "4",
			wantTags:     "bell",
			wantAuth:     "Bearer tok",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var capturedReq *http.Request
			var capturedBody string

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedReq = r
				b, _ := io.ReadAll(r.Body)
				capturedBody = string(b)
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			n := &Ntfy{
				BaseURL: server.URL,
				Topic:   "test-topic",
				Token:   tc.token,
				Client:  server.Client(),
			}

			msg := Message{
				Event: tc.event,
				Title: "Test Title",
				Body:  "Test notification message body",
			}

			err := n.Send(context.Background(), msg)
			if err != nil {
				t.Fatalf("Send failed: %v", err)
			}

			if capturedReq.URL.Path != "/test-topic" {
				t.Errorf("Path = %q, want /test-topic", capturedReq.URL.Path)
			}
			if capturedReq.Header.Get("Title") != "Test Title" {
				t.Errorf("Title = %q, want 'Test Title'", capturedReq.Header.Get("Title"))
			}
			if capturedReq.Header.Get("Priority") != tc.wantPriority {
				t.Errorf("Priority = %q, want %q", capturedReq.Header.Get("Priority"), tc.wantPriority)
			}
			if capturedReq.Header.Get("Tags") != tc.wantTags {
				t.Errorf("Tags = %q, want %q", capturedReq.Header.Get("Tags"), tc.wantTags)
			}
			if capturedReq.Header.Get("Authorization") != tc.wantAuth {
				t.Errorf("Authorization = %q, want %q", capturedReq.Header.Get("Authorization"), tc.wantAuth)
			}
			if capturedBody != "Test notification message body" {
				t.Errorf("Body = %q, want 'Test notification message body'", capturedBody)
			}
		})
	}
}

func TestNtfy_Non2xxError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("unauthorized topic"))
	}))
	defer server.Close()

	n := &Ntfy{
		BaseURL: server.URL,
		Topic:   "forbidden-topic",
		Client:  server.Client(),
	}

	err := n.Send(context.Background(), Message{Title: "hi", Body: "there"})
	if err == nil {
		t.Fatalf("expected error on 403 response, got nil")
	}
}
