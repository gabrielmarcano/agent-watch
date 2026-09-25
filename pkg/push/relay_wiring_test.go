package push_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"testing"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
	"github.com/gabrielmarcano/agent-monitor/pkg/push"
	"github.com/gabrielmarcano/agent-monitor/pkg/relay"
)

// TestFCM_DeadTokenIsRemovedFromRelayStore wires FCM the way
// pkg/relay/server.go does (Tokens: store.AllFCMTokens, OnInvalidToken:
// store.RemoveFCMToken) and checks the result in the real store: the token FCM
// reports UNREGISTERED is gone, the healthy one stays, and so does a token that
// only got a bare 404.
func TestFCM_DeadTokenIsRemovedFromRelayStore(t *testing.T) {
	store, err := relay.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	for _, d := range []struct{ name, fcmToken string }{
		{"watch-dead", "tok-dead"},
		{"watch-live", "tok-live"},
		{"watch-bare-404", "tok-bare-404"},
	} {
		dev, err := store.AddDevice(d.name, "hash-"+d.name)
		if err != nil {
			t.Fatalf("AddDevice(%s): %v", d.name, err)
		}
		if !store.UpdateDeviceFCMToken(dev.ID, d.fcmToken) {
			t.Fatalf("UpdateDeviceFCMToken(%s) found no device", d.name)
		}
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			Message struct {
				Token string `json:"token"`
			} `json:"message"`
		}
		_ = json.NewDecoder(r.Body).Decode(&p)
		switch p.Message.Token {
		case "tok-dead":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":404,"message":"Requested entity was not found.","status":"NOT_FOUND","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNREGISTERED"}]}}`))
		case "tok-bare-404":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":404,"message":"Requested entity was not found."}}`))
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	creds := &google.Credentials{
		ProjectID:   "test-proj",
		TokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test-access-token", TokenType: "Bearer"}),
	}
	fcm := push.NewFCM(context.Background(), creds, store.AllFCMTokens, store.RemoveFCMToken)
	fcm.Endpoint = server.URL

	d := push.NewDispatcher([]push.Sender{fcm}, nil, nil)
	d.OnAgentUpdate(nil, model.AgentState{PaneID: "p1", Label: "test", Status: model.StatusBlocked})
	d.Flush()
	d.Wait()

	got := store.AllFCMTokens()
	sort.Strings(got)
	if want := []string{"tok-bare-404", "tok-live"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("store FCM tokens = %v, want %v", got, want)
	}
}
