package herdrtest

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func writeSubscribe(t *testing.T, sockPath, paneID string) (net.Conn, *bufio.Reader) {
	t.Helper()
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	data, _ := json.Marshal(map[string]any{
		"id":     "sub-1",
		"method": "events.subscribe",
		"params": map[string]any{"subscriptions": []map[string]any{
			{"type": "pane.agent_status_changed", "pane_id": paneID},
		}},
	})
	if _, err := conn.Write(append(data, '\n')); err != nil {
		t.Fatalf("write: %v", err)
	}
	return conn, bufio.NewReader(conn)
}

// An event emitted as soon as the client has read the ack must reach it: the
// subscriber has to be registered before the ack goes out.
func TestSubscribeRegisteredBeforeAck(t *testing.T) {
	s := New(t)
	emitted := make(chan struct{})
	s.mu.Lock()
	s.testHookAfterAck = func() { <-emitted } // park the handler right after the ack
	s.mu.Unlock()

	conn, reader := writeSubscribe(t, s.SocketPath, "w1:p1")
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := reader.ReadBytes('\n'); err != nil {
		close(emitted)
		t.Fatalf("read ack: %v", err)
	}

	s.EmitStatusChanged("w1:p1", "working")
	close(emitted)

	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("event emitted right after the ack was lost: %v", err)
	}
	if !strings.Contains(string(line), "pane_agent_status_changed") {
		t.Errorf("unexpected line %s", line)
	}
}

// A subscribe still being handled when the server stops must not leave an
// open stream behind.
func TestSubscribeDuringStopIsClosed(t *testing.T) {
	s := New(t)
	s.mu.Lock()
	s.testHookBeforeRegister = func() { s.Stop() }
	s.mu.Unlock()

	conn, reader := writeSubscribe(t, s.SocketPath, "w1:p1")
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	line, err := reader.ReadBytes('\n')
	if !errors.Is(err, io.EOF) {
		t.Fatalf("got %q, %v after Stop; want the connection closed (EOF) without an ack", line, err)
	}
}
