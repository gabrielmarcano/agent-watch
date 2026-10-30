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
	hold := srv.HoldNext("terminal.list")
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
	hold := srv.HoldNext("terminal.list")
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
	srv.HoldNext("terminal.list")
	client := &herdr.Client{SocketPath: srv.SocketPath, Timeout: time.Minute}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	if err := listAsync(t, client, ctx, 3*time.Second); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context.DeadlineExceeded", err)
	}
}

func TestCallTimeoutWithoutContextDeadline(t *testing.T) {
	srv := herdrtest.New(t)
	srv.HoldNext("terminal.list")
	client := &herdr.Client{SocketPath: srv.SocketPath, Timeout: 50 * time.Millisecond}

	if err := listAsync(t, client, context.Background(), 3*time.Second); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context.DeadlineExceeded after Client.Timeout", err)
	}
}

func echoID(format string) func(string) string {
	return func(id string) string { return fmt.Sprintf(format, id) + "\n" }
}

func rawClient(t *testing.T, reply func(string) string) *herdr.Client {
	t.Helper()
	return &herdr.Client{SocketPath: rawServerFunc(t, reply), Timeout: 2 * time.Second}
}

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
		return `{"jsonrpc":"2.0","id":"someone-else","result":{"version":"0.1.14","generation":1}}` + "\n"
	})
	_, err := client.Ping(context.Background())
	requireProtocolError(t, err, "someone-else")
}

func TestCallKeepsHerdrErrorWithEmptyID(t *testing.T) {
	client := rawClient(t, func(string) string {
		return `{"jsonrpc":"2.0","id":"","error":{"code":-32600,"message":"missing field","data":{"kind":"invalid_request"}}}` + "\n"
	})
	if err := client.SendKeys(context.Background(), "w1:p1", []string{"1"}); !herdr.IsCode(err, "invalid_request") {
		t.Errorf("err = %v, want herdr invalid_request", err)
	}
}

func TestCallRejectsResponseWithoutResultOrError(t *testing.T) {
	client := rawClient(t, echoID(`{"jsonrpc":"2.0","id":%q}`))
	err := client.SendKeys(context.Background(), "w1:p1", []string{"1"})
	requireProtocolError(t, err, "neither result nor error")
}

func TestListAgentsChecksResult(t *testing.T) {
	for _, tc := range []struct {
		name, reply, want string
	}{
		{"no result", `{"jsonrpc":"2.0","id":%q,"result":null}`, "neither result nor error"},
		{"terminals missing", `{"jsonrpc":"2.0","id":%q,"result":{}}`, "terminals"},
		{"terminals null", `{"jsonrpc":"2.0","id":%q,"result":{"terminals":null}}`, "terminals"},
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
		agents, err := rawClient(t, echoID(`{"jsonrpc":"2.0","id":%q,"result":{"terminals":[]}}`)).ListAgents(context.Background())
		if err != nil || len(agents) != 0 {
			t.Errorf("got %v, %v; want no agents and no error", agents, err)
		}
	})
}

func TestReadChecksResult(t *testing.T) {
	for _, tc := range []struct {
		name, reply, want string
	}{
		{"text missing", `{"jsonrpc":"2.0","id":%q,"result":{"complete":true}}`, "text"},
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

func TestPingChecksResultVersion(t *testing.T) {
	_, err := rawClient(t, echoID(`{"jsonrpc":"2.0","id":%q,"result":{"terminals":[]}}`)).Ping(context.Background())
	requireProtocolError(t, err, "version")
}

func TestSubscribeRejectsMismatchedAckID(t *testing.T) {
	client := rawClient(t, func(string) string {
		return `{"jsonrpc":"2.0","id":"someone-else","result":{"seq":1,"terminals":[]}}` + "\n"
	})
	_, err := client.Subscribe(context.Background())
	requireProtocolError(t, err, "someone-else")
}

// Shutdown must not wait for Client.Timeout while herdr sits on a terminal.list.
func TestSyncerShutdownWithHungList(t *testing.T) {
	srv := herdrtest.New(t)
	srv.SetAgents([]map[string]any{agentRow("w1:p1", "idle", 1)})
	hold := srv.HoldNext("terminal.list")
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
		t.Fatal("Run still waiting on a hung terminal.list after ctx was cancelled")
	}
}
