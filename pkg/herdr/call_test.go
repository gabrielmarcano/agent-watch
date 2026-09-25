package herdr_test

import (
	"context"
	"errors"
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
