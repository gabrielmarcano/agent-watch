package push

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

// testAccessToken is what the fake OAuth token source hands out. It is not a
// credential: no real service account is ever read in tests.
const testAccessToken = "test-access-token"

// newTestFCM builds the sender the way the relay does (NewFCM, the part of
// NewFCMFromCredentials after the JSON is parsed), with a static OAuth token
// and the endpoint pointed at a fake server.
func newTestFCM(endpoint string, tokens func() []string, onInvalidToken func(string)) *FCM {
	creds := &google.Credentials{
		ProjectID:   "test-proj",
		TokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: testAccessToken, TokenType: "Bearer"}),
	}
	f := NewFCM(context.Background(), creds, tokens, onInvalidToken)
	f.Endpoint = endpoint
	return f
}

// capturedRequest is one request seen by a fake FCM endpoint.
type capturedRequest struct {
	method, path, contentType, authorization string
	body                                     []byte
}

// TestFCM_Payload checks the exact request FCM receives: no key missing, no
// extra key, every data value a string, no notification block (data-only), and
// the OAuth Authorization header from the credentials.
func TestFCM_Payload(t *testing.T) {
	tests := []struct {
		name string
		msg  Message
		want string
	}{
		{
			name: "blocked",
			msg: Message{
				Event: EventBlocked, PaneID: "w5:pAE", Agent: "claude", Label: "bizum",
				Title: "bizum needs approval", Body: "Bash command: go test ./...",
				StateChangeSeq: 334, Fingerprint: "9f2c61d0a4b3e871",
				AllowOptionID: "opt-1", DenyOptionID: "opt-3",
			},
			// The example of docs/phases/3b-push.md, verbatim.
			want: `{"message":{"token":"device-token-1",
				"data":{"event":"blocked","pane_id":"w5:pAE","agent":"claude","label":"bizum",
				        "title":"bizum needs approval","body":"Bash command: go test ./...",
				        "state_change_seq":"334","fingerprint":"9f2c61d0a4b3e871",
				        "allow_option_id":"opt-1","deny_option_id":"opt-3"},
				"android":{"priority":"high","ttl":"600s"}}}`,
		},
		{
			name: "done",
			msg: Message{
				Event: EventDone, PaneID: "w5:pAE", Agent: "claude", Label: "bizum",
				Title: "bizum finished", Body: "Task finished", StateChangeSeq: 335,
			},
			want: `{"message":{"token":"device-token-1",
				"data":{"event":"done","pane_id":"w5:pAE","agent":"claude","label":"bizum",
				        "title":"bizum finished","body":"Task finished",
				        "state_change_seq":"335","fingerprint":"",
				        "allow_option_id":"","deny_option_id":""},
				"android":{"priority":"normal","ttl":"600s"}}}`,
		},
		{
			// A digest covering a blocked agent is an approval too: high
			// priority, or Doze may hold it.
			name: "digest with a blocked agent",
			msg:  Message{Event: EventDigest, Title: "4 agents need you", Body: "a, b, c, d", AnyBlocked: true},
			want: `{"message":{"token":"device-token-1",
				"data":{"event":"digest","pane_id":"","agent":"","label":"",
				        "title":"4 agents need you","body":"a, b, c, d",
				        "state_change_seq":"0","fingerprint":"",
				        "allow_option_id":"","deny_option_id":""},
				"android":{"priority":"high","ttl":"600s"}}}`,
		},
		{
			name: "digest of done agents",
			msg:  Message{Event: EventDigest, Title: "4 agents finished", Body: "a, b, c, d"},
			want: `{"message":{"token":"device-token-1",
				"data":{"event":"digest","pane_id":"","agent":"","label":"",
				        "title":"4 agents finished","body":"a, b, c, d",
				        "state_change_seq":"0","fingerprint":"",
				        "allow_option_id":"","deny_option_id":""},
				"android":{"priority":"normal","ttl":"600s"}}}`,
		},
		{
			// Exactly three data keys: the app withdraws the pane's notification.
			name: "resolved",
			msg: Message{
				Event: EventResolved, PaneID: "w5:pAE", Agent: "claude", Label: "bizum",
				Title: "ignored", Body: "ignored", StateChangeSeq: 335, Fingerprint: "ignored",
			},
			want: `{"message":{"token":"device-token-1",
				"data":{"event":"resolved","pane_id":"w5:pAE","state_change_seq":"335"},
				"android":{"priority":"normal","ttl":"600s"}}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqs := make(chan capturedRequest, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				reqs <- capturedRequest{
					method: r.Method, path: r.URL.Path, body: body,
					contentType: r.Header.Get("Content-Type"), authorization: r.Header.Get("Authorization"),
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"name":"projects/test-proj/messages/123"}`))
			}))
			defer server.Close()

			fcm := newTestFCM(server.URL, func() []string { return []string{"device-token-1"} }, nil)
			fcm.EnableResolved = true
			if err := fcm.Send(context.Background(), tt.msg); err != nil {
				t.Fatalf("fcm.Send failed: %v", err)
			}

			var got capturedRequest
			select {
			case got = <-reqs:
			default:
				t.Fatalf("no request received by fake server")
			}

			if got.method != http.MethodPost {
				t.Errorf("Method = %s, want POST", got.method)
			}
			if want := "/v1/projects/test-proj/messages:send"; got.path != want {
				t.Errorf("Path = %q, want %q", got.path, want)
			}
			if got.contentType != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", got.contentType)
			}
			if want := "Bearer " + testAccessToken; got.authorization != want {
				t.Errorf("Authorization = %q, want %q", got.authorization, want)
			}
			assertSameJSON(t, got.body, tt.want)
		})
	}
}

// "resolved" is off unless the relay turns it on: the watch app installed
// today shows an unknown event as "<label> needs you" on the pane's
// notification, which would replace a real approval with a bogus one.
func TestFCM_ResolvedIsOffByDefault(t *testing.T) {
	var requests []string
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, string(body))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	if (&FCM{}).SendsResolved() {
		t.Fatalf("a zero FCM sends resolved")
	}
	fcm := newTestFCM(server.URL, func() []string { return []string{"device-token-1"} }, nil)
	if fcm.SendsResolved() {
		t.Fatalf("NewFCM sends resolved by default")
	}

	// Through the dispatcher: only the blocked push goes out.
	d, _, _ := newTestDispatcher(fcm)
	blocked := model.AgentState{PaneID: "w5:pAE", Label: "bizum", Status: model.StatusBlocked, StateChangeSeq: 334}
	d.OnAgentUpdate(nil, blocked)
	d.Wait()
	d.OnAgentUpdate(&blocked, model.AgentState{PaneID: "w5:pAE", Label: "bizum", Status: model.StatusWorking, StateChangeSeq: 335})
	d.Wait()

	// And even when handed one directly, FCM sends nothing.
	if err := fcm.Send(context.Background(), Message{Event: EventResolved, PaneID: "w5:pAE", StateChangeSeq: 335}); err != nil {
		t.Fatalf("Send(resolved) with resolved off = %v, want nil", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 1 || !strings.Contains(requests[0], `"event":"blocked"`) {
		t.Fatalf("FCM requests = %v, want only the blocked push", requests)
	}
}

// End to end through the real senders: answering a prompt elsewhere sends FCM
// a data-only "resolved" message, and ntfy (which cannot withdraw a
// notification) gets nothing.
func TestDispatcher_ResolvedReachesFCMNotNtfy(t *testing.T) {
	fcmBodies := make(chan []byte, 8)
	fcmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		fcmBodies <- body
		w.WriteHeader(http.StatusOK)
	}))
	defer fcmServer.Close()

	ntfyTitles := make(chan string, 8)
	ntfyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ntfyTitles <- r.Header.Get("Title")
		w.WriteHeader(http.StatusOK)
	}))
	defer ntfyServer.Close()

	fcm := newTestFCM(fcmServer.URL, func() []string { return []string{"device-token-1"} }, nil)
	fcm.EnableResolved = true
	ntfy := &Ntfy{BaseURL: ntfyServer.URL, Topic: "test-topic", Client: ntfyServer.Client()}
	d, _, _ := newTestDispatcher(fcm, ntfy)

	blocked := model.AgentState{PaneID: "w5:pAE", Agent: "claude", Label: "bizum", Status: model.StatusBlocked, StateChangeSeq: 334}
	d.OnAgentUpdate(nil, blocked)
	d.Wait()
	<-fcmBodies // the blocked push
	d.OnAgentUpdate(&blocked, model.AgentState{PaneID: "w5:pAE", Agent: "claude", Label: "bizum", Status: model.StatusWorking, StateChangeSeq: 335})
	d.Wait()

	select {
	case body := <-fcmBodies:
		assertSameJSON(t, body, `{"message":{"token":"device-token-1",
			"data":{"event":"resolved","pane_id":"w5:pAE","state_change_seq":"335"},
			"android":{"priority":"normal","ttl":"600s"}}}`)
	default:
		t.Fatalf("FCM never got the resolved message")
	}
	if n := len(ntfyTitles); n != 1 {
		t.Fatalf("ntfy got %d requests, want 1 (the blocked push only)", n)
	}
}

// assertSameJSON fails unless got and want decode to the same value: same
// keys at every level, no extra ones, same types and values.
func assertSameJSON(t *testing.T, got []byte, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("request body is not JSON: %v\n%s", err, got)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("bad expected JSON in test: %v", err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Fatalf("FCM request body mismatch\n got: %s\nwant: %s", got, compactJSON(t, want))
	}
}

func compactJSON(t *testing.T, s string) string {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(s)); err != nil {
		t.Fatalf("compact JSON: %v", err)
	}
	return buf.String()
}

func TestFCM_DonePriorityNormal(t *testing.T) {
	var capturedPayload fcmMessagePayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&capturedPayload)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	fcm := &FCM{
		ProjectID: "test-proj",
		Endpoint:  server.URL,
		Tokens:    func() []string { return []string{"tok-1"} },
		Client:    server.Client(),
	}

	msg := Message{
		Event: EventDone,
		Title: "done",
		Body:  "Task finished",
	}

	if err := fcm.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	if capturedPayload.Message.Android.Priority != "normal" {
		t.Errorf("Priority for done event = %q, want 'normal'", capturedPayload.Message.Android.Priority)
	}
}

// state_change_seq is a decimal string even when it is 0 (a digest, or an
// agent herdr has not counted yet): the app parses it as a number.
func TestFCM_ZeroStateChangeSeqIsSent(t *testing.T) {
	var capturedPayload fcmMessagePayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&capturedPayload)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	fcm := &FCM{
		ProjectID: "test-proj",
		Endpoint:  server.URL,
		Tokens:    func() []string { return []string{"tok-1"} },
		Client:    server.Client(),
	}

	if err := fcm.Send(context.Background(), Message{Event: EventDigest, Title: "4 agents need you"}); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	got, ok := capturedPayload.Message.Data["state_change_seq"]
	if !ok || got != "0" {
		t.Fatalf(`data["state_change_seq"] = %q (present: %t), want "0"`, got, ok)
	}
}

func TestFCM_InvalidTokens(t *testing.T) {
	var deadTokens []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload fcmMessagePayload
		_ = json.NewDecoder(r.Body).Decode(&payload)

		if strings.Contains(payload.Message.Token, "bare-404") {
			// A 404 without FCM's error code: e.g. a wrong project id.
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(fcmErrBare404))
			return
		}
		if strings.Contains(payload.Message.Token, "404") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(fcmErrUnregistered))
			return
		}
		if strings.Contains(payload.Message.Token, "unregistered") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":400,"message":"The registration token is not registered","status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNREGISTERED"}]}}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	tokens := []string{"token-404", "token-bare-404", "token-unregistered", "token-valid"}
	var mu sync.Mutex // tokens are sent concurrently
	fcm := &FCM{
		ProjectID: "test-proj",
		Endpoint:  server.URL,
		Tokens:    func() []string { return tokens },
		Client:    server.Client(),
		OnInvalidToken: func(token string) {
			mu.Lock()
			defer mu.Unlock()
			deadTokens = append(deadTokens, token)
		},
	}

	msg := Message{Event: EventDone, Title: "test"}
	_ = fcm.Send(context.Background(), msg)

	sort.Strings(deadTokens)
	if len(deadTokens) != 2 {
		t.Fatalf("expected 2 dead tokens, got %d: %v", len(deadTokens), deadTokens)
	}
	if deadTokens[0] != "token-404" || deadTokens[1] != "token-unregistered" {
		t.Errorf("unexpected dead tokens: %v", deadTokens)
	}
}

// Real FCM v1 error bodies. Only errors about the token itself mean the token
// is dead; payload errors also come back as 400 INVALID_ARGUMENT.
const (
	fcmErrTTL             = `{"error":{"code":400,"message":"Invalid value at 'message.android.ttl' (type.googleapis.com/google.protobuf.Duration), Field 'ttl', Illegal duration format","status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.rpc.BadRequest","fieldViolations":[{"field":"message.android.ttl","description":"Invalid value at 'message.android.ttl'"}]}]}}`
	fcmErrTooBig          = `{"error":{"code":400,"message":"Request contains an invalid argument.","status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"INVALID_ARGUMENT"}]}}`
	fcmErrBadToken        = `{"error":{"code":400,"message":"The registration token is not a valid FCM registration token","status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"INVALID_ARGUMENT"},{"@type":"type.googleapis.com/google.rpc.BadRequest","fieldViolations":[{"field":"message.token","description":"The registration token is not a valid FCM registration token"}]}]}}`
	fcmErrUnregistered    = `{"error":{"code":404,"message":"Requested entity was not found.","status":"NOT_FOUND","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNREGISTERED"}]}}`
	fcmErrUnregistered400 = `{"error":{"code":400,"message":"Requested entity was not found.","status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNREGISTERED"}]}}`
	fcmErrNotJSON         = `<html>Bad Request: INVALID_ARGUMENT</html>`
	// 404s that are not about the token: a wrong or deleted project id, or a
	// proxy in the way. They must never wipe every device's token.
	fcmErrBare404         = `{"error":{"code":404,"message":"Requested entity was not found."}}`
	fcmErrProjectNotFound = `{"error":{"code":404,"message":"Requested entity was not found.","status":"NOT_FOUND"}}`
	fcmErrUnregistered503 = `{"error":{"code":503,"message":"The service is currently unavailable.","status":"UNAVAILABLE","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNREGISTERED"}]}}`
)

// TestDispatcher_FCMRetriesPerToken checks that a failure on one token never
// re-sends the push to the tokens that already got it, and that only transient
// failures (5xx, 429, network) are retried, once.
func TestDispatcher_FCMRetriesPerToken(t *testing.T) {
	var mu sync.Mutex
	hits := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p fcmMessagePayload
		_ = json.NewDecoder(r.Body).Decode(&p)
		tok := p.Message.Token
		mu.Lock()
		hits[tok]++
		n := hits[tok]
		mu.Unlock()

		switch {
		case tok == "tok-down":
			w.WriteHeader(http.StatusServiceUnavailable)
		case tok == "tok-flaky" && n == 1:
			w.WriteHeader(http.StatusInternalServerError)
		case tok == "tok-throttled" && n == 1:
			w.WriteHeader(http.StatusTooManyRequests)
		case tok == "tok-net" && n == 1:
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close() // network error on the client side
			}
		case tok == "tok-payload":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(fcmErrTTL))
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	tokens := []string{"tok-good", "tok-down", "tok-flaky", "tok-throttled", "tok-net", "tok-payload", "tok-good-2"}
	fcm := &FCM{
		ProjectID:  "test-proj",
		Endpoint:   server.URL,
		Tokens:     func() []string { return tokens },
		Client:     server.Client(),
		RetryDelay: time.Millisecond,
	}
	d, _, _ := newTestDispatcher(fcm)

	d.OnAgentUpdate(nil, model.AgentState{PaneID: "p1", Label: "test", Status: model.StatusBlocked})
	d.Wait()

	want := map[string]int{
		"tok-good":      1, // delivered once, never duplicated
		"tok-good-2":    1,
		"tok-down":      2, // 503: one retry
		"tok-flaky":     2, // 500 then delivered
		"tok-throttled": 2, // 429 then delivered
		"tok-net":       2, // network error then delivered
		"tok-payload":   1, // 400 payload error: retrying cannot help
	}
	mu.Lock()
	defer mu.Unlock()
	for tok, n := range want {
		if hits[tok] != n {
			t.Errorf("%s: %d requests, want %d", tok, hits[tok], n)
		}
	}
}

func TestFCM_DeadTokenDetection(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		wantDead bool
	}{
		{"payload error: bad ttl", http.StatusBadRequest, fcmErrTTL, false},
		{"payload error: message too big", http.StatusBadRequest, fcmErrTooBig, false},
		{"400 without a JSON body", http.StatusBadRequest, fcmErrNotJSON, false},
		{"invalid token (field message.token)", http.StatusBadRequest, fcmErrBadToken, true},
		{"unregistered (404)", http.StatusNotFound, fcmErrUnregistered, true},
		{"unregistered errorCode on a 400", http.StatusBadRequest, fcmErrUnregistered400, true},
		{"bare 404 (no FCM error code)", http.StatusNotFound, fcmErrBare404, false},
		{"404 NOT_FOUND without UNREGISTERED (wrong project)", http.StatusNotFound, fcmErrProjectNotFound, false},
		{"404 without a JSON body", http.StatusNotFound, fcmErrNotJSON, false},
		{"UNREGISTERED on a 5xx", http.StatusServiceUnavailable, fcmErrUnregistered503, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			var mu sync.Mutex // tokens are sent concurrently
			var dead []string
			fcm := &FCM{
				ProjectID: "test-proj",
				Endpoint:  server.URL,
				Tokens:    func() []string { return []string{"tok-a", "tok-b"} },
				Client:    server.Client(),
				OnInvalidToken: func(token string) {
					mu.Lock()
					defer mu.Unlock()
					dead = append(dead, token)
				},
				RetryDelay: time.Millisecond, // the 5xx case retries
			}

			if err := fcm.Send(context.Background(), Message{Event: EventDone, Title: "t"}); err == nil {
				t.Fatalf("Send returned nil for a %d response", tt.status)
			}

			if tt.wantDead && len(dead) != 2 {
				t.Fatalf("expected both tokens reported dead, got %v", dead)
			}
			if !tt.wantDead && len(dead) != 0 {
				t.Fatalf("a %d that is not about the token must not unregister tokens, got %v", tt.status, dead)
			}
		})
	}
}

// testDeadline bounds a wait that must succeed long before it expires. It is a
// guard against hanging, never a timing assertion.
const testDeadline = 5 * time.Second

// hangingFCM is a fake FCM endpoint where token "tok-hang" never answers until
// the client gives up or the test ends; every other token gets a 200.
type hangingFCM struct {
	server    *httptest.Server
	release   chan struct{}
	delivered chan string
}

func newHangingFCM(t *testing.T) *hangingFCM {
	t.Helper()
	h := &hangingFCM{release: make(chan struct{}), delivered: make(chan string, 16)}
	h.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p fcmMessagePayload
		_ = json.NewDecoder(r.Body).Decode(&p)
		if p.Message.Token == "tok-hang" {
			select {
			case <-r.Context().Done(): // the client gave up on this token
			case <-h.release:
			}
			return
		}
		w.WriteHeader(http.StatusOK)
		h.delivered <- p.Message.Token
	}))
	t.Cleanup(func() {
		close(h.release)
		h.server.Close()
	})
	return h
}

// A token that never answers must not hold back the other tokens: each one
// is sent on its own, not after the hanging one.
func TestFCM_HangingTokenDoesNotStarveOthers(t *testing.T) {
	h := newHangingFCM(t)
	fcm := &FCM{
		ProjectID: "test-proj",
		Endpoint:  h.server.URL,
		Tokens:    func() []string { return []string{"tok-hang", "tok-good-1", "tok-good-2"} },
		Client:    h.server.Client(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sendDone := make(chan error, 1)
	go func() { sendDone <- fcm.Send(ctx, Message{Event: EventBlocked, PaneID: "p1"}) }()

	got := map[string]bool{}
	for len(got) < 2 {
		select {
		case tok := <-h.delivered:
			got[tok] = true
		case <-time.After(testDeadline):
			t.Fatalf("healthy tokens starved behind a hanging one; delivered so far: %v", got)
		}
	}
	if !got["tok-good-1"] || !got["tok-good-2"] {
		t.Fatalf("delivered = %v, want tok-good-1 and tok-good-2", got)
	}

	cancel() // end the hanging token
	select {
	case err := <-sendDone:
		if err == nil || !strings.Contains(err.Error(), "fcm post") {
			t.Fatalf("Send error = %v, want the hanging token's failure", err)
		}
	case <-time.After(testDeadline):
		t.Fatalf("Send did not return after its context was canceled")
	}
}

// Each token has its own timeout, so a hanging token ends even when the
// caller's context has no deadline.
func TestFCM_TokenTimeoutEndsAHangingToken(t *testing.T) {
	h := newHangingFCM(t)
	fcm := &FCM{
		ProjectID:    "test-proj",
		Endpoint:     h.server.URL,
		Tokens:       func() []string { return []string{"tok-hang", "tok-good-1"} },
		Client:       h.server.Client(),
		TokenTimeout: 20 * time.Millisecond,
	}

	sendDone := make(chan error, 1)
	go func() { sendDone <- fcm.Send(context.Background(), Message{Event: EventBlocked, PaneID: "p1"}) }()

	select {
	case err := <-sendDone:
		if err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
			t.Fatalf("Send error = %v, want the hanging token's timeout", err)
		}
	case <-time.After(testDeadline):
		t.Fatalf("Send never returned: the hanging token has no timeout of its own")
	}
	select {
	case tok := <-h.delivered:
		if tok != "tok-good-1" {
			t.Fatalf("delivered %q, want tok-good-1", tok)
		}
	default:
		t.Fatalf("tok-good-1 was not delivered")
	}
}
