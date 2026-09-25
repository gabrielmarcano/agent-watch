package herdr_test

import (
	"bufio"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/herdr"
	"github.com/gabrielmarcano/agent-monitor/pkg/herdrtest"
)

// rawServer is a one-connection unix socket server that answers the first
// request line with reply, verbatim, and then keeps the connection open until
// the test ends. It covers byte-level cases herdrtest cannot express.
func rawServer(t *testing.T, reply string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "hr") // short path: unix socket paths are limited
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	t.Cleanup(func() { close(done); _ = l.Close() })
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if _, err := bufio.NewReader(conn).ReadBytes('\n'); err != nil {
			return
		}
		_, _ = conn.Write([]byte(reply))
		<-done
	}()
	return sock
}

// subscribeAsync runs Subscribe in a goroutine and returns its outcome, or
// fails the test if it does not return within limit.
func subscribeAsync(t *testing.T, client *herdr.Client, ctx context.Context, limit time.Duration) error {
	t.Helper()
	errCh := make(chan error, 1)
	go func() {
		_, err := client.Subscribe(ctx, []herdr.Subscription{{Type: "pane.created"}})
		errCh <- err
	}()
	select {
	case err := <-errCh:
		return err
	case <-time.After(limit):
		t.Fatalf("Subscribe still blocked after %v waiting for an ack that never comes", limit)
		return nil
	}
}

func TestSubscribeHonoursDeadlineWhileWaitingForAck(t *testing.T) {
	srv := herdrtest.New(t)
	srv.HoldNext("events.subscribe") // accepted, never answered
	client := &herdr.Client{SocketPath: srv.SocketPath, Timeout: time.Minute}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := subscribeAsync(t, client, ctx, 3*time.Second)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
}

func TestSubscribeHonoursCancelWhileWaitingForAck(t *testing.T) {
	srv := herdrtest.New(t)
	hold := srv.HoldNext("events.subscribe")
	client := &herdr.Client{SocketPath: srv.SocketPath, Timeout: time.Minute}

	ctx, cancel := context.WithCancel(context.Background()) // no deadline
	defer cancel()
	go func() {
		<-hold.Received()
		cancel()
	}()

	err := subscribeAsync(t, client, ctx, 3*time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestSubscribeHandshakeUsesClientTimeout(t *testing.T) {
	srv := herdrtest.New(t)
	srv.HoldNext("events.subscribe")
	client := &herdr.Client{SocketPath: srv.SocketPath, Timeout: 50 * time.Millisecond}

	err := subscribeAsync(t, client, context.Background(), 3*time.Second)
	if err == nil {
		t.Fatal("Subscribe succeeded without an ack")
	}
}

func TestSubscribeClearsHandshakeDeadline(t *testing.T) {
	srv := herdrtest.New(t)
	client := &herdr.Client{SocketPath: srv.SocketPath, Timeout: 20 * time.Millisecond}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := client.Subscribe(ctx, []herdr.Subscription{{Type: "pane.agent_status_changed", PaneID: "w1:p1"}})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Wait past the handshake timeout: the stream must not time out.
	select {
	case _, ok := <-stream:
		if !ok {
			t.Fatal("stream closed: the handshake deadline was left on the connection")
		}
	case <-time.After(100 * time.Millisecond):
	}

	srv.EmitStatusChanged("w1:p1", "working")
	select {
	case ev, ok := <-stream:
		if !ok || ev.Name != "pane_agent_status_changed" {
			t.Fatalf("got %+v, %v", ev, ok)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event after the handshake timeout elapsed")
	}

	cancel()
	select {
	case _, ok := <-stream:
		if ok {
			// Drain anything already buffered, then require the close.
			for range stream {
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not close after ctx cancel")
	}
}

// An event written in the same packet as the ack is buffered by the reader
// that parsed the ack; it must still be delivered.
func TestSubscribeKeepsEventBufferedWithAck(t *testing.T) {
	sock := rawServer(t,
		`{"id":"x","result":{"type":"subscription_started"}}`+"\n"+
			`{"event":"pane_agent_status_changed","data":{"pane_id":"w1:p1","agent_status":"working"}}`+"\n")
	client := &herdr.Client{SocketPath: sock, Timeout: 2 * time.Second}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := client.Subscribe(ctx, []herdr.Subscription{{Type: "pane.agent_status_changed", PaneID: "w1:p1"}})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	select {
	case ev, ok := <-stream:
		if !ok {
			t.Fatal("stream closed; the event buffered with the ack was lost")
		}
		if ev.Name != "pane_agent_status_changed" || !strings.Contains(string(ev.Data), "w1:p1") {
			t.Errorf("unexpected event %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the event buffered with the ack was lost")
	}
}

func TestSubscribeRejectsUnexpectedAckType(t *testing.T) {
	sock := rawServer(t, `{"id":"x","result":{"type":"pong"}}`+"\n")
	client := &herdr.Client{SocketPath: sock, Timeout: 2 * time.Second}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := client.Subscribe(ctx, []herdr.Subscription{{Type: "pane.created"}}); err == nil {
		t.Fatal("Subscribe accepted an ack whose result.type is not subscription_started")
	}
}

func TestSubscribeMapsHerdrError(t *testing.T) {
	srv := herdrtest.New(t)
	client := &herdr.Client{SocketPath: srv.SocketPath, Timeout: 2 * time.Second}

	// pane.agent_status_changed without pane_id is rejected by herdr.
	_, err := client.Subscribe(context.Background(), []herdr.Subscription{{Type: "pane.agent_status_changed"}})
	if !herdr.IsCode(err, "invalid_request") {
		t.Fatalf("err = %v, want herdr invalid_request", err)
	}
}
