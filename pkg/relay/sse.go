package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

// sseWriteTimeoutDefault bounds each SSE write: a client that stops reading
// (a watch that lost its network without closing the socket) is dropped
// instead of pinning the handler forever.
const sseWriteTimeoutDefault = 10 * time.Second

// events handles GET /v1/events SSE streaming.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	if _, ok := w.(http.Flusher); !ok {
		writeError(w, model.ErrInternal, "streaming unsupported")
		return
	}

	// The request context is cancelled when the client goes away, the device
	// is revoked or the relay shuts down. Then also expire the write
	// deadline, so a write blocked on a client that stopped reading returns.
	ctx := r.Context()
	rc := http.NewResponseController(w)
	stopDeadline := context.AfterFunc(ctx, func() { _ = rc.SetWriteDeadline(time.Now()) })
	defer stopDeadline()

	writeTimeout := s.sseWriteTimeout
	if writeTimeout <= 0 {
		writeTimeout = sseWriteTimeoutDefault
	}
	// send writes one event and flushes it, within writeTimeout.
	send := func(format string, args ...any) bool {
		_ = rc.SetWriteDeadline(time.Now().Add(writeTimeout))
		// Checked after moving the deadline: if ctx ended before, the
		// AfterFunc's "now" deadline may just have been overwritten.
		if ctx.Err() != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, format, args...); err != nil {
			return false
		}
		return rc.Flush() == nil
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")

	// 1. Subscribe BEFORE taking the snapshot so no update is lost in between
	sub, subCh := s.state.Subscribe()
	defer s.state.Unsubscribe(sub)

	// 2. Write initial snapshot
	snap := s.state.Snapshot()
	snapData, err := json.Marshal(snap)
	if err == nil {
		if !send("event: snapshot\ndata: %s\n\n", snapData) {
			return
		}
	}

	// 3. Keepalive and event loop
	interval := s.keepAliveInterval
	if interval <= 0 {
		interval = 15 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case ev, ok := <-subCh:
			if !ok || ctx.Err() != nil {
				return
			}
			if !send("event: %s\ndata: %s\n\n", ev.Name, ev.Data) {
				return
			}
		case <-ticker.C:
			if !send(":\n\n") {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}
