package herdr_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/herdr"
	"github.com/gabrielmarcano/agent-monitor/pkg/herdrtest"
)

// listAsync runs ListAgents in a goroutine and returns its error, or fails the
// test if it is still waiting after limit.
func listAsync(t *testing.T, client *herdr.Client, ctx context.Context, limit time.Duration) error {
	t.Helper()
	errCh := make(chan error, 1)
	go func() {
		_, err := client.ListAgents(ctx)
		errCh <- err
	}()
	select {
	case err := <-errCh:
		return err
	case <-time.After(limit):
		t.Fatalf("ListAgents still waiting for herdr's answer after %v", limit)
		return nil
	}
}

func TestCallHonoursCancelWithoutDeadline(t *testing.T) {
	srv := herdrtest.New(t)
	hold := srv.HoldNext("agent.list") // accepted, never answered
	client := &herdr.Client{SocketPath: srv.SocketPath, Timeout: time.Minute}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-hold.Received()
		cancel()
	}()

	err := listAsync(t, client, ctx, 3*time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if errors.Is(err, herdr.ErrUnavailable) {
		t.Errorf("a cancelled call is not herdr being unavailable: %v", err)
	}
}

func TestCallHonoursCancelBeforeDeadline(t *testing.T) {
	srv := herdrtest.New(t)
	hold := srv.HoldNext("agent.list")
	client := &herdr.Client{SocketPath: srv.SocketPath, Timeout: time.Minute}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	go func() {
		<-hold.Received()
		cancel()
	}()

	if err := listAsync(t, client, ctx, 3*time.Second); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestCallReportsContextDeadline(t *testing.T) {
	srv := herdrtest.New(t)
	srv.HoldNext("agent.list")
	client := &herdr.Client{SocketPath: srv.SocketPath, Timeout: time.Minute}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	if err := listAsync(t, client, ctx, 3*time.Second); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context.DeadlineExceeded", err)
	}
}

func TestCallTimeoutWithoutContextDeadline(t *testing.T) {
	srv := herdrtest.New(t)
	srv.HoldNext("agent.list")
	client := &herdr.Client{SocketPath: srv.SocketPath, Timeout: 50 * time.Millisecond}

	if err := listAsync(t, client, context.Background(), 3*time.Second); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context.DeadlineExceeded after Client.Timeout", err)
	}
}

// echoID builds a raw reply whose %[1]s is replaced by the request id.
func echoID(format string) func(string) string {
	return func(id string) string { return fmt.Sprintf(format, id) + "\n" }
}

func rawClient(t *testing.T, reply func(string) string) *herdr.Client {
	t.Helper()
	return &herdr.Client{SocketPath: rawServerFunc(t, reply), Timeout: 2 * time.Second}
}

// requireProtocolError checks err is a clear client-side error that mentions
// want, and is neither "herdr unavailable" nor a herdr error code.
func requireProtocolError(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("got no error; want one mentioning %q", want)
	}
	if errors.Is(err, herdr.ErrUnavailable) {
		t.Errorf("err = %v; a bad answer is not herdr being unavailable", err)
	}
	if !strings.Contains(err.Error(), want) {
		t.Errorf("err = %q; want it to mention %q", err, want)
	}
}

func TestCallRejectsMismatchedResponseID(t *testing.T) {
	client := rawClient(t, func(string) string {
		return `{"id":"someone-else","result":{"type":"pong","version":"0.9.1","protocol":22}}` + "\n"
	})
	_, err := client.Ping(context.Background())
	requireProtocolError(t, err, "someone-else")
}

// herdr answers a request it could not parse with id "": its error is still
// the most useful thing to return.
func TestCallKeepsHerdrErrorWithEmptyID(t *testing.T) {
	client := rawClient(t, func(string) string {
		return `{"id":"","error":{"code":"invalid_request","message":"missing field"}}` + "\n"
	})
	if err := client.SendKeys(context.Background(), "w1:p1", []string{"1"}); !herdr.IsCode(err, "invalid_request") {
		t.Errorf("err = %v, want herdr invalid_request", err)
	}
}

func TestCallRejectsResponseWithoutResultOrError(t *testing.T) {
	client := rawClient(t, echoID(`{"id":%q}`))
	err := client.SendKeys(context.Background(), "w1:p1", []string{"1"})
	requireProtocolError(t, err, "neither result nor error")
}

func TestListAgentsChecksResult(t *testing.T) {
	for _, tc := range []struct {
		name, reply, want string
	}{
		{"wrong type", `{"id":%q,"result":{"type":"pane_read","read":{"text":"x"}}}`, `"pane_read"`},
		{"no result", `{"id":%q,"result":null}`, `"agent_list"`},
		{"agents missing", `{"id":%q,"result":{"type":"agent_list"}}`, "agents"},
		{"agents null", `{"id":%q,"result":{"type":"agent_list","agents":null}}`, "agents"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agents, err := rawClient(t, echoID(tc.reply)).ListAgents(context.Background())
			requireProtocolError(t, err, tc.want)
			if agents != nil {
				t.Errorf("returned agents %v alongside an error", agents)
			}
		})
	}

	t.Run("empty herd is fine", func(t *testing.T) {
		agents, err := rawClient(t, echoID(`{"id":%q,"result":{"type":"agent_list","agents":[]}}`)).ListAgents(context.Background())
		if err != nil || len(agents) != 0 {
			t.Errorf("got %v, %v; want no agents and no error", agents, err)
		}
	})
}

func TestReadChecksResult(t *testing.T) {
	for _, tc := range []struct {
		name, reply, want string
	}{
		{"wrong type", `{"id":%q,"result":{"type":"agent_list","agents":[]}}`, `"agent_list"`},
		{"read missing", `{"id":%q,"result":{"type":"pane_read"}}`, "read"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text, err := rawClient(t, echoID(tc.reply)).Read(context.Background(), "w1:p1", herdr.SourceVisible, 0)
			requireProtocolError(t, err, tc.want)
			if text != "" {
				t.Errorf("returned text %q alongside an error", text)
			}
		})
	}
}

func TestPingChecksResultType(t *testing.T) {
	_, err := rawClient(t, echoID(`{"id":%q,"result":{"type":"agent_list","agents":[]}}`)).Ping(context.Background())
	requireProtocolError(t, err, `"agent_list"`)
}

func TestSubscribeRejectsMismatchedAckID(t *testing.T) {
	client := rawClient(t, func(string) string {
		return `{"id":"someone-else","result":{"type":"subscription_started"}}` + "\n"
	})
	_, err := client.Subscribe(context.Background(), []herdr.Subscription{{Type: "pane.created"}})
	requireProtocolError(t, err, "someone-else")
}

// Shutdown must not wait for Client.Timeout while herdr sits on an agent.list.
func TestSyncerShutdownWithHungList(t *testing.T) {
	srv := herdrtest.New(t)
	srv.SetAgents([]map[string]any{agentRow("w1:p1", "idle", 1)})
	hold := srv.HoldNext("agent.list")
	syncer := &herdr.Syncer{
		Client:       &herdr.Client{SocketPath: srv.SocketPath, Timeout: time.Minute},
		Listener:     &testListener{},
		PollHealthy:  never,
		PollDegraded: never,
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = syncer.Run(ctx)
	}()
	select {
	case <-hold.Received():
	case <-time.After(2 * time.Second):
		t.Fatal("Syncer never listed")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run still waiting on a hung agent.list after ctx was cancelled")
	}
}
