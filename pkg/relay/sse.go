package relay

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

// events handles GET /v1/events SSE streaming.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, model.ErrInternal, "streaming unsupported")
		return
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
		if _, writeErr := fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", snapData); writeErr != nil {
			return
		}
		flusher.Flush()
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
			if !ok {
				return
			}
			if _, writeErr := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Name, ev.Data); writeErr != nil {
				return
			}
			flusher.Flush()
		case <-ticker.C:
			if _, writeErr := fmt.Fprintf(w, ":\n\n"); writeErr != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}
