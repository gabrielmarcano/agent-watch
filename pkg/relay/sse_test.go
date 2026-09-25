package relay

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

// stalledWriter is an SSE ResponseWriter whose client stopped reading: the
// first okWrites writes succeed, later ones block until the write deadline
// (set through http.ResponseController) passes, like a full TCP socket.
type stalledWriter struct {
	header   http.Header
	okWrites int
	written  chan string   // what each successful write carried
	release  chan struct{} // closed at test end so nothing leaks

	mu       sync.Mutex
	writes   int
	deadline time.Time
	moved    chan struct{} // closed and replaced whenever the deadline moves
}

func newStalledWriter(okWrites int) *stalledWriter {
	return &stalledWriter{
		header:   http.Header{},
		okWrites: okWrites,
		written:  make(chan string, 16),
		release:  make(chan struct{}),
		moved:    make(chan struct{}),
	}
}

func (w *stalledWriter) Header() http.Header { return w.header }
func (w *stalledWriter) WriteHeader(int)     {}
func (w *stalledWriter) Flush()              {}
func (w *stalledWriter) FlushError() error   { return nil }

func (w *stalledWriter) SetWriteDeadline(t time.Time) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.deadline = t
	close(w.moved)
	w.moved = make(chan struct{})
	return nil
}

func (w *stalledWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	w.writes++
	n := w.writes
	w.mu.Unlock()
	if n <= w.okWrites {
		w.written <- string(p)
		return len(p), nil
	}
	for {
		w.mu.Lock()
		deadline, moved := w.deadline, w.moved
		w.mu.Unlock()
		var expired <-chan time.Time
		if !deadline.IsZero() {
			timer := time.NewTimer(time.Until(deadline))
			defer timer.Stop()
			expired = timer.C
		}
		select {
		case <-expired:
			return 0, os.ErrDeadlineExceeded
		case <-moved:
		case <-w.release:
			return 0, errors.New("released by the test")
		}
	}
}

// An event write to a client that stopped reading must give up after the
// per-event write deadline, even though the request context is still live.
func TestSSE_StalledClientHitsWriteDeadline(t *testing.T) {
	server, _ := setupTestServer(t)
	server.sseWriteTimeout = 50 * time.Millisecond

	w := newStalledWriter(1) // the snapshot goes through, then the client stalls
	defer close(w.release)
	req, _ := http.NewRequestWithContext(context.Background(), "GET", "/v1/events", nil)

	returned := make(chan struct{})
	go func() {
		server.events(w, req)
		close(returned)
	}()

	select {
	case first := <-w.written:
		if !strings.HasPrefix(first, "event: snapshot\n") {
			t.Fatalf("first write = %q, want the snapshot event", first)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("snapshot never written")
	}
	server.State().Upsert(model.AgentState{PaneID: "w1:p1", Status: model.StatusWorking})

	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatalf("SSE handler still blocked on a stalled client")
	}
}
