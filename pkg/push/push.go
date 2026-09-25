package push

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

// Event describes the notification type.
type Event string

const (
	EventBlocked Event = "blocked"
	EventDone    Event = "done"
	EventDigest  Event = "digest"
)

// Message is the push notification payload forwarded to Senders.
type Message struct {
	Event          Event
	PaneID         string
	Agent          string
	Label          string
	Title          string
	Body           string
	StateChangeSeq uint64
	Fingerprint    string
	AllowOptionID  string
	DenyOptionID   string
}

// Sender delivers a push notification to a platform.
type Sender interface {
	Name() string
	Send(ctx context.Context, m Message) error
}

// noRetryError marks a Send error that the Dispatcher must not retry.
type noRetryError struct {
	err error
}

func (e noRetryError) Error() string { return e.err.Error() }
func (e noRetryError) Unwrap() error { return e.err }

// NoRetry marks err so the Dispatcher does not call Send again. A sender with
// several targets returns it once it has retried each failed target itself:
// retrying the whole Send would deliver twice to the targets that succeeded.
// NoRetry(nil) is nil.
func NoRetry(err error) error {
	if err == nil {
		return nil
	}
	return noRetryError{err: err}
}

// Dispatcher processes agent updates, filters them, applies debounce/digest windows,
// and delivers messages to registered senders.
type Dispatcher struct {
	Senders          []Sender
	Now              func() time.Time
	Logger           *slog.Logger
	DebounceDuration time.Duration
	WindowDuration   time.Duration

	mu        sync.Mutex
	afterFunc func(time.Duration, func()) stopper // time.AfterFunc; tests fake it
	lastPush  map[string]time.Time                // key: pane_id + ":" + event; pruned past DebounceDuration
	lastPrune time.Time
	window    []Message // pending messages in the current window
	timer     stopper
	wg        sync.WaitGroup
}

// stopper is the part of *time.Timer the Dispatcher uses.
type stopper interface {
	Stop() bool
}

func realAfterFunc(d time.Duration, f func()) stopper {
	return time.AfterFunc(d, f)
}

// NewDispatcher creates a new Dispatcher.
func NewDispatcher(senders []Sender, now func() time.Time, logger *slog.Logger) *Dispatcher {
	if now == nil {
		now = time.Now
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Dispatcher{
		Senders:          senders,
		Now:              now,
		Logger:           logger,
		DebounceDuration: 5 * time.Second,
		WindowDuration:   10 * time.Second,
		afterFunc:        realAfterFunc,
		lastPush:         make(map[string]time.Time),
	}
}

// TruncateRunes cuts s to at most maxRunes on a rune boundary and appends "…" when cut.
func TruncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return "…"
	}
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}

	target := maxRunes - 1
	count := 0
	for i := range s {
		if count == target {
			return s[:i] + "…"
		}
		count++
	}
	return s + "…"
}

// OnAgentUpdate implements relay.Notifier.
func (d *Dispatcher) OnAgentUpdate(prev *model.AgentState, cur model.AgentState) {
	msg, shouldPush := d.buildMessage(prev, cur)
	if !shouldPush {
		return
	}

	d.enqueue(msg)
}

// buildMessage determines if a transition should push, and formats the message.
func (d *Dispatcher) buildMessage(prev *model.AgentState, cur model.AgentState) (Message, bool) {
	// Transition 1: (prev == nil || prev.Status != blocked) && cur.Status == blocked
	if (prev == nil || prev.Status != model.StatusBlocked) && cur.Status == model.StatusBlocked {
		title := fmt.Sprintf("%s needs approval", cur.Label)
		body := ""
		if cur.Prompt == nil || cur.Prompt.Kind == model.PromptUnknown {
			body = "Open Agent Watch to see the question"
		} else {
			if cur.Prompt.Detail != "" {
				body = fmt.Sprintf("%s: %s", cur.Prompt.Title, cur.Prompt.Detail)
			} else {
				body = cur.Prompt.Title
			}
		}
		body = TruncateRunes(body, 240)

		fingerprint := ""
		allowOpt := ""
		denyOpt := ""
		if cur.Prompt != nil {
			fingerprint = cur.Prompt.Fingerprint
			for _, opt := range cur.Prompt.Options {
				if allowOpt == "" && opt.Role == model.RoleAllowOnce {
					allowOpt = opt.ID
				}
				if denyOpt == "" && opt.Role == model.RoleDeny {
					denyOpt = opt.ID
				}
			}
		}

		return Message{
			Event:          EventBlocked,
			PaneID:         cur.PaneID,
			Agent:          cur.Agent,
			Label:          cur.Label,
			Title:          title,
			Body:           body,
			StateChangeSeq: cur.StateChangeSeq,
			Fingerprint:    fingerprint,
			AllowOptionID:  allowOpt,
			DenyOptionID:   denyOpt,
		}, true
	}

	// Transition 2: prev != nil && prev.Status == working && cur.Status == done
	if prev != nil && prev.Status == model.StatusWorking && cur.Status == model.StatusDone {
		return Message{
			Event:          EventDone,
			PaneID:         cur.PaneID,
			Agent:          cur.Agent,
			Label:          cur.Label,
			Title:          fmt.Sprintf("%s finished", cur.Label),
			Body:           "Task finished",
			StateChangeSeq: cur.StateChangeSeq,
		}, true
	}

	return Message{}, false
}

// enqueue applies debounce and windowing/digest logic.
func (d *Dispatcher) enqueue(m Message) {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := d.Now()
	d.pruneLocked(now)
	key := m.PaneID + ":" + string(m.Event)

	// Debounce: drop if the same pane_id + event was sent less than 5s ago
	if last, ok := d.lastPush[key]; ok {
		if now.Sub(last) < d.DebounceDuration {
			return
		}
	}
	d.lastPush[key] = now

	// 10s Windowing & Digest:
	// Latency matters for blocked agents, so send the first message of a window
	// immediately and hold subsequent messages. If > 3 messages arrive during the
	// 10s window, flush sends one digest notification covering the remaining messages.
	if len(d.window) == 0 {
		d.window = append(d.window, m)
		d.dispatchLocked(m)

		d.timer = d.afterFunc(d.WindowDuration, func() {
			d.Flush()
		})
		return
	}

	d.window = append(d.window, m)
}

// pruneLocked drops the lastPush entries the debounce can no longer match, so
// the map holds only the panes pushed within the last DebounceDuration. It
// scans at most once per DebounceDuration.
func (d *Dispatcher) pruneLocked(now time.Time) {
	if now.Sub(d.lastPrune) < d.DebounceDuration {
		return
	}
	d.lastPrune = now
	for key, last := range d.lastPush {
		if now.Sub(last) >= d.DebounceDuration {
			delete(d.lastPush, key)
		}
	}
}

// Flush flushes the current pending window, sending pending messages or a digest.
func (d *Dispatcher) Flush() {
	d.mu.Lock()
	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}

	if len(d.window) <= 1 {
		// Only 0 or 1 message in window (the 1st was already sent immediately)
		d.window = nil
		d.mu.Unlock()
		return
	}

	total := len(d.window)
	pending := make([]Message, len(d.window)-1)
	copy(pending, d.window[1:])
	d.window = nil
	d.mu.Unlock()

	if total <= 3 {
		// <= 3 total messages: send remaining messages individually
		for _, m := range pending {
			d.dispatch(m)
		}
	} else {
		// > 3 total messages: send one digest message covering the remaining messages
		seen := make(map[string]bool)
		var labels []string
		for _, m := range pending {
			if m.Label != "" && !seen[m.Label] {
				seen[m.Label] = true
				labels = append(labels, m.Label)
			}
		}

		digestMsg := Message{
			Event: EventDigest,
			Title: fmt.Sprintf("%d agents need you", len(pending)),
			Body:  strings.Join(labels, ", "),
		}
		d.dispatch(digestMsg)
	}
}

// dispatch delivers m to all registered senders in separate goroutines with retry.
func (d *Dispatcher) dispatch(m Message) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.dispatchLocked(m)
}

func (d *Dispatcher) dispatchLocked(m Message) {
	for _, s := range d.Senders {
		d.wg.Add(1)
		go func(sender Sender) {
			defer d.wg.Done()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			if err := sender.Send(ctx, m); err != nil {
				var noRetry noRetryError
				if errors.As(err, &noRetry) {
					d.Logger.Error("push send failed",
						"sender", sender.Name(),
						"event", m.Event,
						"pane", m.PaneID,
						"err", err,
					)
					return
				}

				d.Logger.Warn("push send failed, retrying once",
					"sender", sender.Name(),
					"event", m.Event,
					"pane", m.PaneID,
					"err", err,
				)

				retryCtx, retryCancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer retryCancel()

				if retryErr := sender.Send(retryCtx, m); retryErr != nil {
					d.Logger.Error("push retry failed",
						"sender", sender.Name(),
						"event", m.Event,
						"pane", m.PaneID,
						"err", retryErr,
					)
				}
			}
		}(s)
	}
}

// Wait blocks until all currently active push goroutines finish (useful for tests).
func (d *Dispatcher) Wait() {
	d.wg.Wait()
}
