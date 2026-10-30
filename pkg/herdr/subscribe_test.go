package herdr_test

import (
	"bufio"
	"context"
	"encoding/json"
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

func rawServer(t *testing.T, reply string) string {
	t.Helper()
	return rawServerFunc(t, func(string) string { return reply })
}

func rawServerFunc(t *testing.T, reply func(reqID string) string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "hr")
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
		line, err := bufio.NewReader(conn).ReadBytes('\n')
		if err != nil {
			return
		}
		var req struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(line, &req)
		_, _ = conn.Write([]byte(reply(req.ID)))
		<-done
	}()
	return sock
}

func subscribeAsync(t *testing.T, client *herdr.Client, ctx context.Context, limit time.Duration) error {
	t.Helper()
	errCh := make(chan error, 1)
	go func() {
		_, err := client.Subscribe(ctx)
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
	srv.HoldNext("events.subscribe")
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

	ctx, cancel := context.WithCancel(context.Background())
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
	stream, err := client.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

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
		if !ok || ev.Name != "event" {
			t.Fatalf("got %+v, %v", ev, ok)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event after the handshake timeout elapsed")
	}

	cancel()
	select {
	case _, ok := <-stream:
		if ok {
			for range stream {
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not close after ctx cancel")
	}
}

func TestSubscribeKeepsEventBufferedWithAck(t *testing.T) {
	sock := rawServerFunc(t, func(id string) string {
		return `{"jsonrpc":"2.0","id":"` + id + `","result":{"seq":1,"terminals":[]}}` + "\n" +
			`{"jsonrpc":"2.0","method":"event","params":{"kind":"status","terminal":"w1:p1","state":"working"}}` + "\n"
	})
	client := &herdr.Client{SocketPath: sock, Timeout: 2 * time.Second}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := client.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	select {
	case ev, ok := <-stream:
		if !ok {
			t.Fatal("stream closed; the event buffered with the ack was lost")
		}
		if ev.Name != "event" || !strings.Contains(string(ev.Data), "w1:p1") {
			t.Errorf("unexpected event %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the event buffered with the ack was lost")
	}
}

func TestSubscribeRejectsUnexpectedAckShape(t *testing.T) {
	sock := rawServerFunc(t, func(id string) string {
		return `{"jsonrpc":"2.0","id":"` + id + `","result":{"type":"pong"}}` + "\n"
	})
	client := &herdr.Client{SocketPath: sock, Timeout: 2 * time.Second}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := client.Subscribe(ctx); err == nil {
		t.Fatal("Subscribe accepted an ack without terminals")
	}
}

func TestSubscribeMapsHerdrError(t *testing.T) {
	srv := herdrtest.New(t)
	srv.FailNext("events.subscribe", "invalid_request", "rejected")
	client := &herdr.Client{SocketPath: srv.SocketPath, Timeout: 2 * time.Second}

	_, err := client.Subscribe(context.Background())
	if !herdr.IsCode(err, "invalid_request") {
		t.Fatalf("err = %v, want herdr invalid_request", err)
	}
}
