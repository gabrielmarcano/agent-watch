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
	// EventResolved tells the app to withdraw the notification of a blocked
	// push whose agent is no longer blocked.
	EventResolved Event = "resolved"
)

const (
	defaultDebounce = 5 * time.Second
	defaultWindow   = 10 * time.Second
	// maxShownBlocked caps Dispatcher.blockedShown.
	maxShownBlocked = 1024
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

// ResolvedSender is a Sender whose app can withdraw a notification it already
// shows. Only senders whose SendsResolved reports true get EventResolved
// messages: the others (ntfy, or FCM until it is enabled) cannot take a
// notification back, and would show a bogus one instead.
type ResolvedSender interface {
	Sender
	SendsResolved() bool
}

func sendsResolved(s Sender) bool {
	rs, ok := s.(ResolvedSender)
	return ok && rs.SendsResolved()
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

	// The current window. Its first message went out right away; the later
	// ones wait in held until the window timer flushes them.
	timer      stopper // nil while no window is open
	windowGen  uint64  // tells a stale timer from the current window's
	windowSent int     // messages of this window already sent
	held       []Message
	latest     map[string]model.AgentState // newest state of every pane in held

	// blockedShown holds the panes whose own blocked push went out and was
	// not withdrawn yet, with when it went out. Capped at maxShownBlocked:
	// a pane removed while blocked is never seen leaving blocked.
	blockedShown map[string]time.Time

	wg sync.WaitGroup
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
		DebounceDuration: defaultDebounce,
		WindowDuration:   defaultWindow,
		afterFunc:        realAfterFunc,
		lastPush:         make(map[string]time.Time),
		latest:           make(map[string]model.AgentState),
		blockedShown:     make(map[string]time.Time),
	}
}

// initLocked gives a Dispatcher built without NewDispatcher (or with zero
// fields) the same defaults, so it never panics on a nil clock or logger.
func (d *Dispatcher) initLocked() {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.DebounceDuration <= 0 {
		d.DebounceDuration = defaultDebounce
	}
	if d.WindowDuration <= 0 {
		d.WindowDuration = defaultWindow
	}
	if d.afterFunc == nil {
		d.afterFunc = realAfterFunc
	}
	if d.lastPush == nil {
		d.lastPush = make(map[string]time.Time)
	}
	if d.latest == nil {
		d.latest = make(map[string]model.AgentState)
	}
	if d.blockedShown == nil {
		d.blockedShown = make(map[string]time.Time)
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

	d.mu.Lock()
	defer d.mu.Unlock()
	d.initLocked()

	// A held message is checked against its pane's newest state at flush.
	if _, held := d.latest[cur.PaneID]; held {
		d.latest[cur.PaneID] = cur
	}

	// The pane's blocked notification is stale once the pane leaves blocked
	// (answered on the Mac, on another watch, or canceled): withdraw it right
	// away. No debounce, no window, no digest.
	if _, shown := d.blockedShown[cur.PaneID]; shown && cur.Status != model.StatusBlocked {
		delete(d.blockedShown, cur.PaneID)
		d.dispatchLocked(Message{Event: EventResolved, PaneID: cur.PaneID, StateChangeSeq: cur.StateChangeSeq})
	}

	if shouldPush {
		d.enqueueLocked(msg, cur)
	}
}

// buildMessage determines if a transition should push, and formats the message.
func (d *Dispatcher) buildMessage(prev *model.AgentState, cur model.AgentState) (Message, bool) {
	// Transition 1: (prev == nil || prev.Status != blocked) && cur.Status == blocked
	if (prev == nil || prev.Status != model.StatusBlocked) && cur.Status == model.StatusBlocked {
		return blockedMessage(cur), true
	}

	// Transition 2: prev != nil && prev.Status == working && cur.Status == done
	if prev != nil && prev.Status == model.StatusWorking && cur.Status == model.StatusDone {
		return doneMessage(cur), true
	}

	return Message{}, false
}

// blockedMessage announces that cur waits for an answer to its prompt.
func blockedMessage(cur model.AgentState) Message {
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
	}
}

// doneMessage announces that cur finished its turn.
func doneMessage(cur model.AgentState) Message {
	return Message{
		Event:          EventDone,
		PaneID:         cur.PaneID,
		Agent:          cur.Agent,
		Label:          cur.Label,
		Title:          fmt.Sprintf("%s finished", cur.Label),
		Body:           "Task finished",
		StateChangeSeq: cur.StateChangeSeq,
	}
}

// enqueueLocked applies the debounce and the window to m, built from cur.
func (d *Dispatcher) enqueueLocked(m Message, cur model.AgentState) {
	now := d.Now()
	d.pruneLocked(now)

	// Debounce: drop if the same pane_id + event was accepted less than
	// DebounceDuration ago.
	key := m.PaneID + ":" + string(m.Event)
	if last, ok := d.lastPush[key]; ok && now.Sub(last) < d.DebounceDuration {
		return
	}
	d.lastPush[key] = now

	// Window & digest: latency matters for blocked agents, so the first
	// message of a window goes out immediately and the later ones are held
	// until the window ends. The flush then sends them one by one, or a
	// single digest when more than 3 pushes would go out in the window.
	if d.timer == nil {
		d.sendLocked(m, now)
		d.windowSent = 1
		d.windowGen++
		gen := d.windowGen
		d.timer = d.afterFunc(d.WindowDuration, func() { d.flush(gen) })
		return
	}
	d.held = append(d.held, m)
	d.latest[m.PaneID] = cur
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

// Flush ends the current window now: it sends the held messages that still
// hold, one by one or as one digest.
func (d *Dispatcher) Flush() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.initLocked()
	d.flushLocked()
}

// flush is the window timer's callback. A timer that fires after its window
// was flushed some other way does nothing.
func (d *Dispatcher) flush(gen uint64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.timer == nil || d.windowGen != gen {
		return
	}
	d.flushLocked()
}

func (d *Dispatcher) flushLocked() {
	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
	due := dueMessages(d.held, d.latest)
	sent := d.windowSent
	d.held, d.windowSent = nil, 0
	clear(d.latest)

	if sent+len(due) <= 3 {
		now := d.Now()
		for _, m := range due {
			d.sendLocked(m, now)
		}
		return
	}
	// A digest shows no per-pane notification, so there is nothing for a
	// resolved message to withdraw later.
	d.dispatchLocked(digestMessage(due))
}

// sendLocked dispatches a message about one pane and remembers a blocked
// one, so the pane's notification can be withdrawn when it leaves blocked.
func (d *Dispatcher) sendLocked(m Message, now time.Time) {
	d.dispatchLocked(m)
	if m.Event != EventBlocked {
		return
	}
	if _, ok := d.blockedShown[m.PaneID]; !ok && len(d.blockedShown) >= maxShownBlocked {
		oldest := ""
		for pane, at := range d.blockedShown {
			if oldest == "" || at.Before(d.blockedShown[oldest]) {
				oldest = pane
			}
		}
		delete(d.blockedShown, oldest)
	}
	d.blockedShown[m.PaneID] = now
}

// paneEvent identifies what a held message announces.
type paneEvent struct {
	pane  string
	event Event
}

// dueMessages returns what a flush may still push: at most one message per
// agent, built from the agent's newest state, and only while that state is one
// a held message announced. When the agent has left it (the prompt was
// answered, the finished agent is working again) its messages are dropped:
// the watch must never offer to approve a prompt that is gone.
func dueMessages(held []Message, latest map[string]model.AgentState) []Message {
	announced := make(map[paneEvent]bool)
	var panes []string
	for _, m := range held {
		if !announced[paneEvent{m.PaneID, EventBlocked}] && !announced[paneEvent{m.PaneID, EventDone}] {
			panes = append(panes, m.PaneID)
		}
		announced[paneEvent{m.PaneID, m.Event}] = true
	}

	var due []Message
	for _, pane := range panes {
		cur, ok := latest[pane]
		switch {
		case !ok:
		case cur.Status == model.StatusBlocked && announced[paneEvent{pane, EventBlocked}]:
			due = append(due, blockedMessage(cur))
		case cur.Status == model.StatusDone && announced[paneEvent{pane, EventDone}]:
			due = append(due, doneMessage(cur))
		}
	}
	return due
}

// digestMessage covers due, which holds one message per agent. A digest only
// goes out when more than 3 pushes would in a window, and the window's first
// one is already sent, so len(due) >= 3: the title is always plural.
func digestMessage(due []Message) Message {
	anyBlocked := false
	seen := make(map[string]bool)
	var labels []string
	for _, m := range due {
		if m.Event == EventBlocked {
			anyBlocked = true
		}
		if m.Label != "" && !seen[m.Label] {
			seen[m.Label] = true
			labels = append(labels, m.Label)
		}
	}

	title := fmt.Sprintf("%d agents finished", len(due))
	if anyBlocked {
		title = fmt.Sprintf("%d agents need you", len(due))
	}
	return Message{
		Event: EventDigest,
		Title: title,
		Body:  TruncateRunes(strings.Join(labels, ", "), 240),
	}
}

// dispatchLocked delivers m to all registered senders in separate goroutines with retry.
func (d *Dispatcher) dispatchLocked(m Message) {
	for _, s := range d.Senders {
		if m.Event == EventResolved && !sendsResolved(s) {
			continue
		}
		d.wg.Add(1)
		go func(sender Sender, logger *slog.Logger) {
			defer d.wg.Done()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			if err := sender.Send(ctx, m); err != nil {
				var noRetry noRetryError
				if errors.As(err, &noRetry) {
					logger.Error("push send failed",
						"sender", sender.Name(),
						"event", m.Event,
						"pane", m.PaneID,
						"err", err,
					)
					return
				}

				logger.Warn("push send failed, retrying once",
					"sender", sender.Name(),
					"event", m.Event,
					"pane", m.PaneID,
					"err", err,
				)

				retryCtx, retryCancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer retryCancel()

				if retryErr := sender.Send(retryCtx, m); retryErr != nil {
					logger.Error("push retry failed",
						"sender", sender.Name(),
						"event", m.Event,
						"pane", m.PaneID,
						"err", retryErr,
					)
				}
			}
		}(s, d.Logger)
	}
}

// Wait blocks until all currently active push goroutines finish (useful for tests).
func (d *Dispatcher) Wait() {
	d.wg.Wait()
}
