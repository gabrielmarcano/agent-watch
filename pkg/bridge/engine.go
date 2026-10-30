package bridge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/agents"
	"github.com/gabrielmarcano/agent-monitor/pkg/herdr"
	"github.com/gabrielmarcano/agent-monitor/pkg/model"
	"github.com/gabrielmarcano/agent-monitor/pkg/relayclient"
)

type paneState struct {
	info       herdr.AgentInfo
	public     model.AgentState
	prompt     *agents.Prompt
	lastHistID string
	turnMarker string // the last turn end seen while working (StartTurnWatch)
	// backfilledSession is the transcript session whose older turns this pane
	// has already sent (engine.go, backfillHistory); "" before the first read.
	backfilledSession string
}

// Engine connects herdr events and agent adapters to the remote relay.
type Engine struct {
	Herdr        *herdr.Client
	Syncer       *herdr.Syncer
	Agents       *agents.Registry
	Relay        *relayclient.Client
	Version      string
	HostName     string
	StatusPath   string
	Logger       *slog.Logger
	RetryDelay   time.Duration // delay between prompt parse retries (defaults to 300ms)
	HistoryDelay time.Duration // delay before capturing history (defaults to 500ms)
	// History budgets (docs/reference/agents.md §6), set by NewEngine: the
	// transcript read, the screen capture, and its one retry after a failure.
	TranscriptTimeout  time.Duration
	ScreenReadTimeout  time.Duration
	ScreenRetryTimeout time.Duration
	// TurnCheckInterval is how often StartTurnWatch checks working panes for
	// a turn that ended without a status change (set by NewEngine; 0 = off).
	TurnCheckInterval time.Duration
	// Presence reads whether the owner is using the host, for the relay's
	// push presence (nil: no reports). PresenceInterval is how often
	// StartPresence reports (set by NewEngine).
	Presence         PresenceReader
	PresenceInterval time.Duration
	// PresenceEnabled says whether the owner turned presence reports on
	// (config push_presence), read before each report; nil means off.
	PresenceEnabled func() bool
	presenceOn      atomic.Bool // the last presence read found it on
	// CommandTimeout bounds a command from its arrival, including the wait
	// for the pane lock (defaults to commandTimeout, below the relay's wait).
	CommandTimeout time.Duration
	// SessionRoots and SessionKinds are the host's new-session allowlist,
	// from config.toml (contracts.md §6). Empty roots turn the feature off;
	// empty kinds mean DefaultSessionKinds.
	SessionRoots []string
	SessionKinds []string
	// SessionTimeout and FolderTimeout bound the host-level commands; 0
	// means defaultSessionTimeout / defaultFolderTimeout.
	SessionTimeout time.Duration
	FolderTimeout  time.Duration

	mu          sync.RWMutex
	states      map[string]*paneState
	herdrOnline bool
	pong        herdr.Pong
	herdrErr    string // why herdr is offline; "" while online
	guard       commandGuard
}

func (e *Engine) lockPane(ctx context.Context, paneID string) (func(), error) {
	unlock, _, err := e.guard.AcquirePane(ctx, paneID, "")
	return unlock, err
}

// NewEngine creates a new bridge Engine instance.
func NewEngine(
	herdrClient *herdr.Client,
	syncer *herdr.Syncer,
	reg *agents.Registry,
	relay *relayclient.Client,
	version, hostName, statusPath string,
	logger *slog.Logger,
) *Engine {
	if hostName == "" {
		hostName, _ = os.Hostname()
	}
	if logger == nil {
		logger = slog.Default()
	}
	e := &Engine{
		Herdr:        herdrClient,
		Syncer:       syncer,
		Agents:       reg,
		Relay:        relay,
		Version:      version,
		HostName:     hostName,
		StatusPath:   statusPath,
		Logger:       logger,
		RetryDelay:   300 * time.Millisecond,
		HistoryDelay: 500 * time.Millisecond,

		TranscriptTimeout:  3 * time.Second,
		ScreenReadTimeout:  3 * time.Second,
		ScreenRetryTimeout: 8 * time.Second,
		TurnCheckInterval:  15 * time.Second,
		PresenceInterval:   15 * time.Second,

		states: make(map[string]*paneState),
	}
	if syncer != nil {
		syncer.Listener = e
	}
	if relay != nil {
		relay.OnConnect = e.ConnectMessages
		relay.OnMessage = e.HandleRelayMessage
	}
	return e
}

// Run starts all background loops (status writer, turn watch, presence,
// syncer, and relay client) and blocks until ctx is done or a fatal error
// occurs. On cancellation, it ensures the status file writes stopped status
// before returning.
func (e *Engine) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	writerDone := e.StartStatusWriter(runCtx, 5*time.Second)
	e.StartTurnWatch(runCtx)
	e.StartPresence(runCtx)

	if e.Syncer != nil {
		go func() {
			if err := e.Syncer.Run(runCtx); err != nil && runCtx.Err() == nil && e.Logger != nil {
				e.Logger.Error("syncer error", "err", err)
			}
		}()
	}

	var runErr error
	if e.Relay != nil {
		runErr = e.Relay.Run(runCtx)
	} else {
		<-runCtx.Done()
	}

	cancel()
	<-writerDone

	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		return fmt.Errorf("relay client run: %w", runErr)
	}
	return nil
}

// OnHerdrOnline implements herdr.Listener.
func (e *Engine) OnHerdrOnline(online bool, pong herdr.Pong) {
	e.mu.Lock()
	e.herdrOnline = online
	e.pong = pong
	if online {
		e.herdrErr = ""
	} else {
		e.herdrErr = "herdr unreachable"
		if e.Herdr != nil && e.Herdr.SocketPath != "" {
			e.herdrErr = "herdr unreachable at " + e.Herdr.SocketPath
		}
	}
	e.mu.Unlock()

	if e.Relay != nil {
		e.Relay.Send(model.HerdrStatusMsg{
			Type:        model.WireHerdrStatus,
			HerdrOnline: online,
		})
	}
}

// OnChanges implements herdr.Listener.
func (e *Engine) OnChanges(changes []herdr.Change) {
	e.mu.Lock()
	defer e.mu.Unlock()

	for _, ch := range changes {
		paneID := ch.Agent.PaneID
		switch ch.Kind {
		case herdr.Removed:
			delete(e.states, paneID)
			e.guard.Forget(paneID, 0, true)
			if e.Relay != nil {
				e.Relay.Send(model.AgentRemovedMsg{
					Type:   model.WireAgentRemoved,
					PaneID: paneID,
				})
			}

		case herdr.Added, herdr.Updated:
			info := ch.Agent
			agentName := ""
			if info.Agent != nil {
				agentName = *info.Agent
			}

			// A new seq (or a lost agent) ends any "already answered" record.
			e.guard.Forget(paneID, info.StateChangeSeq, agentName == "")

			// Only panes with a detected agent are tracked
			if agentName == "" {
				if _, ok := e.states[paneID]; ok {
					delete(e.states, paneID)
					if e.Relay != nil {
						e.Relay.Send(model.AgentRemovedMsg{
							Type:   model.WireAgentRemoved,
							PaneID: paneID,
						})
					}
				}
				continue
			}

			st, exists := e.states[paneID]
			if !exists {
				st = &paneState{}
				e.states[paneID] = st
			}

			prevStatus := ""
			if ch.Prev != nil {
				prevStatus = ch.Prev.AgentStatus
			} else if exists {
				prevStatus = string(st.public.Status)
			}

			pub, cwd := e.buildAgentState(info)
			st.info = info

			if pub.Status == model.StatusBlocked {
				needParse := !exists ||
					prevStatus != string(model.StatusBlocked) ||
					(ch.Prev != nil && ch.Prev.StateChangeSeq != info.StateChangeSeq) ||
					st.prompt == nil

				if needParse {
					// Drop the previous prompt: it belongs to an older screen and
					// must not be re-published or used while the re-parse runs.
					st.prompt = nil
					pub.Prompt = nil
					st.public = pub
					go e.resolvePrompt(info.PaneID, info.StateChangeSeq, agentName)
					continue
				}
				if st.prompt != nil {
					pub.Prompt = &st.prompt.Public
				}
			} else {
				st.prompt = nil
				pub.Prompt = nil
			}

			// Child terminals from terminal.list (info.SubagentCount) or the
			// turn watch populate background_agents while working; task counts
			// hold across statuses until the next transcript read.
			if info.SubagentCount != nil {
				pub.BackgroundAgents = *info.SubagentCount
			} else if pub.Status == model.StatusWorking {
				pub.BackgroundAgents = st.public.BackgroundAgents
			}
			if info.BackgroundShellCount != nil {
				pub.BackgroundShells = *info.BackgroundShellCount
			} else {
				pub.BackgroundShells = st.public.BackgroundShells
			}
			if info.BackgroundMonitorCount != nil {
				pub.BackgroundMonitors = *info.BackgroundMonitorCount
			} else {
				pub.BackgroundMonitors = st.public.BackgroundMonitors
			}

			st.public = pub
			if e.Relay != nil {
				e.Relay.Send(model.AgentUpdateMsg{
					Type:  model.WireAgentUpdate,
					Agent: pub,
				})
			}

			e.maybeBackfillHistoryLocked(info.PaneID, cwd, agentName, info)

			// History triggers: working -> done|idle, and a pane coming back
			// to a conversation from a view without one (Claude's agents
			// view), whose turns may have finished out of sight.
			settled := pub.Status == model.StatusDone || pub.Status == model.StatusIdle
			if settled && (prevStatus == string(model.StatusWorking) || e.backToConversation(agentName, ch.Prev, info)) {
				delay := e.HistoryDelay
				go func(paneID, cwd, agent string, info herdr.AgentInfo) {
					if delay > 0 {
						time.Sleep(delay)
					}
					e.captureHistory(paneID, cwd, agent, info)
				}(info.PaneID, cwd, agentName, info)
			}
		}
	}
}

// resolvePrompt retries the parse because the TUI may draw the menu a moment
// after herdr flips the pane to blocked.
func (e *Engine) resolvePrompt(paneID string, seq uint64, agentName string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var prompt agents.Prompt
	var parsed bool
	var lastScreen string

	delay := e.RetryDelay
retry:
	for attempt := 0; attempt < 3; attempt++ {
		screen, err := e.Herdr.Read(ctx, paneID, herdr.SourceVisible, 0)
		if err == nil {
			lastScreen = screen
			ad := e.Agents.For(agentName)
			p, ok := ad.ParsePrompt(screen)
			if ok {
				prompt = p
				parsed = true
				break
			}
		}
		if attempt < 2 && delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				// Out of time: stop retrying (a bare break would only leave
				// the select) and fall back to an unknown prompt.
				timer.Stop()
				break retry
			case <-timer.C:
			}
		}
	}

	if !parsed {
		prompt = agents.UnknownPrompt(lastScreen)
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	st, exists := e.states[paneID]
	if !exists || st.info.StateChangeSeq != seq || st.public.Status != model.StatusBlocked {
		return
	}
	if !parsed && st.info.StatusMsg != "" {
		prompt.Public.Title = st.info.StatusMsg
	}

	st.prompt = &prompt
	st.public.Prompt = &prompt.Public
	st.public.UpdatedAt = model.Now()

	if e.Relay != nil {
		e.Relay.Send(model.AgentUpdateMsg{
			Type:  model.WireAgentUpdate,
			Agent: st.public,
		})
	}
}

// adoptPrompt stores p, freshly parsed by a command, as the pane's prompt and
// publishes it, so the watch stops showing a prompt it can never answer. It
// only acts while the engine tracks the pane as blocked at seq; the seq stays
// as herdr reported it.
func (e *Engine) adoptPrompt(paneID string, seq uint64, p agents.Prompt) {
	e.mu.Lock()
	defer e.mu.Unlock()

	st, exists := e.states[paneID]
	if !exists || st.info.StateChangeSeq != seq || st.public.Status != model.StatusBlocked {
		return
	}
	if st.prompt != nil && st.prompt.Public.Fingerprint == p.Public.Fingerprint {
		return
	}

	prompt := p
	st.prompt = &prompt
	st.public.Prompt = &prompt.Public
	st.public.UpdatedAt = model.Now()

	if e.Relay != nil {
		e.Relay.Send(model.AgentUpdateMsg{
			Type:  model.WireAgentUpdate,
			Agent: st.public,
		})
	}
}

// backToConversation reports whether an update brings a pane back to a
// conversation from a view without one, by its adapter's reading of the
// terminal titles.
func (e *Engine) backToConversation(agent string, prev *herdr.AgentInfo, info herdr.AgentInfo) bool {
	if prev == nil {
		return false
	}
	ad := e.Agents.For(agent)
	return !ad.ShowsConversation(titleOf(*prev)) && ad.ShowsConversation(titleOf(info))
}

func titleOf(info herdr.AgentInfo) string {
	if info.Title == nil {
		return ""
	}
	return *info.Title
}

// sessionRefFor returns the adapter SessionRef and a session key (used to
// deduplicate history items and backfills) when herdr reports either a trusted
// session or a foreground process ID.
func sessionRefFor(agent, cwd string, info herdr.AgentInfo) (agents.SessionRef, string, bool) {
	if ref := info.TrustedSession(); ref != nil {
		return agents.SessionRef{
			Agent: ref.Agent,
			Kind:  ref.Kind,
			Value: ref.Value,
			PID:   info.ForegroundPID,
			CWD:   cwd,
			Title: titleOf(info),
		}, ref.Value, true
	}
	if info.ForegroundPID > 0 {
		return agents.SessionRef{
			Agent: agent,
			PID:   info.ForegroundPID,
			CWD:   cwd,
			Title: titleOf(info),
		}, fmt.Sprintf("pid:%d", info.ForegroundPID), true
	}
	return agents.SessionRef{}, "", false
}

func (e *Engine) captureHistory(paneID, cwd, agent string, info herdr.AgentInfo) {
	budget := 2*e.TranscriptTimeout + e.ScreenReadTimeout + e.ScreenRetryTimeout + time.Second
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()

	// The pane's title now, not when the capture was scheduled: it says what
	// the pane shows.
	e.mu.RLock()
	if st, ok := e.states[paneID]; ok && st.info.Title != nil {
		info.Title = st.info.Title
	}
	e.mu.RUnlock()

	sRef, sessionKey, hasSession := sessionRefFor(agent, cwd, info)
	ad := e.Agents.For(agent)

	if !ad.ShowsConversation(titleOf(info)) {
		if e.Logger != nil {
			e.Logger.Info("no history: the pane shows no conversation", "pane_id", paneID, "agent", agent)
		}
		e.setTasks(paneID, agents.BackgroundTasks{})
		return
	}

	var item *model.HistoryItem
	var err error

	if !hasSession {
		e.setTasks(paneID, agents.BackgroundTasks{})
	} else {
		readCtx, readCancel := context.WithTimeout(ctx, e.TranscriptTimeout)
		var tasks agents.BackgroundTasks
		item, tasks, err = ad.ObserveSettledTurn(readCtx, sRef)
		readCancel()
		e.setTasks(paneID, tasks)

		if errors.Is(err, agents.ErrNoReply) {
			if e.Logger != nil {
				e.Logger.Info("no history: the last turn has no reply yet", "pane_id", paneID, "agent", agent, "reason", err)
			}
			return
		}
		if (err != nil || item == nil) && e.Logger != nil {
			// Never log the transcript itself; the error names the cause (ids only).
			e.Logger.Warn("LastTurn failed; falling back to a screen capture for history",
				"pane_id", paneID, "agent", agent, "session_kind", sRef.Kind, "err", err)
		}
	}

	if err != nil || item == nil {
		text, readErr := e.readScreenForHistory(ctx, paneID)
		if readErr != nil {
			if e.Logger != nil {
				e.Logger.Warn("screen read fallback failed for history", "pane_id", paneID, "err", readErr)
			}
			return
		}
		item = ad.ScreenTurn(text)
		if item == nil && e.Logger != nil {
			e.Logger.Info("no history: the screen shows no conversation", "pane_id", paneID, "agent", agent)
		}
	}

	if item == nil {
		return
	}

	e.publishHistory(paneID, agent, sessionKey, *item)
}

// publishHistory sends the pane's last reply to the relay unless it is the
// one sent last for the pane. sessionValue is herdr's session value (or "").
func (e *Engine) publishHistory(paneID, agent, sessionValue string, item model.HistoryItem) {
	item.ID = model.HistoryID(paneID, sessionValue, item.Query, item.Response)
	item.PaneID = paneID
	item.Agent = agent
	if item.CompletedAt == "" {
		// A transcript turn carries its own time; a screen capture has none.
		item.CompletedAt = model.Now()
	}

	e.mu.Lock()
	st, exists := e.states[paneID]
	if !exists {
		e.mu.Unlock()
		return
	}
	item.Label = st.public.Label

	if item.ID == st.lastHistID {
		e.mu.Unlock()
		return
	}
	st.lastHistID = item.ID
	e.mu.Unlock()

	if e.Relay != nil {
		e.Relay.Send(model.HistoryItemMsg{
			Type: model.WireHistoryItem,
			Item: item,
		})
	}
}

// historyBackfillTurns is how many finished turns the bridge reads from a
// transcript when it first sees a pane or a new session in it.
const historyBackfillTurns = 20

// maybeBackfillHistoryLocked starts a transcript backfill when the pane shows
// a conversation whose session has not been read yet. e.mu must be held.
func (e *Engine) maybeBackfillHistoryLocked(paneID, cwd, agent string, info herdr.AgentInfo) {
	if e.Relay == nil || e.Agents == nil {
		return
	}
	sRef, sessionKey, ok := sessionRefFor(agent, cwd, info)
	if !ok {
		return
	}
	ad := e.Agents.For(agent)
	if !ad.ShowsConversation(titleOf(info)) {
		return
	}
	st, ok := e.states[paneID]
	if !ok || st.backfilledSession == sessionKey {
		return
	}
	st.backfilledSession = sessionKey
	go e.backfillHistory(paneID, agent, sRef, sessionKey)
}

// backfillHistory publishes the older turns of a pane's transcript, oldest
// first, so the relay's newest-first bucket ends up in order. The items are
// marked Backfill: the relay stores and shows them but never takes one for
// the reply of a pending "done" push (contracts.md §3). A read failure is
// logged and forgotten: the live capture covers the last turn, and the relay
// drops anything it already holds.
func (e *Engine) backfillHistory(paneID, agent string, sRef agents.SessionRef, sessionKey string) {
	ad := e.Agents.For(agent)
	budget := 2 * e.TranscriptTimeout
	if budget <= 0 {
		budget = 6 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()

	items, err := ad.RecentTurns(ctx, sRef, historyBackfillTurns)
	if err != nil {
		if !errors.Is(err, agents.ErrNoTranscript) && e.Logger != nil {
			e.Logger.Info("no history backfill", "pane_id", paneID, "agent", agent, "err", err)
		}
		if !errors.Is(err, agents.ErrNoTranscript) {
			// A transient failure may succeed on the pane's next change;
			// a missing transcript will not.
			e.mu.Lock()
			if st, ok := e.states[paneID]; ok && st.backfilledSession == sessionKey {
				st.backfilledSession = ""
			}
			e.mu.Unlock()
		}
		return
	}

	e.mu.RLock()
	st, exists := e.states[paneID]
	if !exists || st.backfilledSession != sessionKey {
		e.mu.RUnlock()
		return // the pane went away or moved to another session
	}
	label := st.public.Label
	e.mu.RUnlock()

	for i := len(items) - 1; i >= 0; i-- { // RecentTurns is newest first
		item := items[i]
		item.ID = model.HistoryID(paneID, sessionKey, item.Query, item.Response)
		item.PaneID = paneID
		item.Agent = agent
		item.Label = label
		if item.CompletedAt == "" {
			item.CompletedAt = model.Now()
		}
		e.Relay.Send(model.HistoryItemMsg{
			Type:     model.WireHistoryItem,
			Item:     item,
			Backfill: true,
		})
	}
	if e.Logger != nil && len(items) > 0 {
		e.Logger.Info("history backfilled", "pane_id", paneID, "agent", agent, "turns", len(items))
	}
}

// StartTurnWatch checks, every TurnCheckInterval until ctx is done, the
// panes herdr reports working whose adapter reads turn ends
// (agents.TurnEndReader): a turn can end there without herdr ever reporting
// done (Claude with background agents running), and its reply is published
// like one captured on a working → done transition. The check also keeps
// the pane's background_agents current. It returns at once when
// TurnCheckInterval is 0.
func (e *Engine) StartTurnWatch(ctx context.Context) {
	if e.TurnCheckInterval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(e.TurnCheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				e.checkWorkingTurns(ctx)
			}
		}
	}()
}

// workingPane is a pane checked by checkWorkingTurns.
type workingPane struct {
	paneID, agent, sessionKey string
	ref                       agents.SessionRef
}

// checkWorkingTurns publishes the last completed turn of every working pane
// whose turn-end marker changed since the last check, then the pane's
// background count when it changed. The first marker seen for a pane is
// published too: the relay drops a reply it already holds.
func (e *Engine) checkWorkingTurns(ctx context.Context) {
	var panes []workingPane
	var hidden []string // working panes whose conversation the reader cannot see
	e.mu.RLock()
	for paneID, st := range e.states {
		if st.public.Status != model.StatusWorking {
			continue
		}
		ad := e.Agents.For(st.public.Agent)
		if !ad.ShowsConversation(titleOf(st.info)) {
			hidden = append(hidden, paneID)
			continue
		}
		sRef, sessionKey, ok := sessionRefFor(st.public.Agent, st.public.CWD, st.info)
		if !ok {
			hidden = append(hidden, paneID)
			continue
		}
		panes = append(panes, workingPane{
			paneID:     paneID,
			agent:      st.public.Agent,
			sessionKey: sessionKey,
			ref:        sRef,
		})
	}
	e.mu.RUnlock()

	for _, paneID := range hidden {
		e.setBackground(paneID, 0)
		e.setTasks(paneID, agents.BackgroundTasks{})
	}
	for _, p := range panes {
		if ctx.Err() != nil {
			return
		}
		ad := e.Agents.For(p.agent)
		readCtx, cancel := context.WithTimeout(ctx, e.TranscriptTimeout)
		end, tasks, ok, err := ad.ObserveWorkingTurn(readCtx, p.ref)
		cancel()
		if !ok {
			continue
		}
		if err != nil {
			// The count stays: a failed read says nothing new.
			if e.Logger != nil {
				e.Logger.Debug("turn-end check failed", "pane_id", p.paneID, "agent", p.agent, "err", err)
			}
			continue
		}
		e.mu.Lock()
		st, ok := e.states[p.paneID]
		changed := ok && end.Marker != "" && st.turnMarker != end.Marker
		if changed {
			st.turnMarker = end.Marker
		}
		e.mu.Unlock()
		if changed && end.Item != nil {
			if e.Logger != nil {
				e.Logger.Info("a turn ended while the pane stays working; publishing its reply", "pane_id", p.paneID, "agent", p.agent)
			}
			e.publishHistory(p.paneID, p.agent, p.sessionKey, *end.Item)
		}
		e.setBackground(p.paneID, end.Background)
		e.setTasks(p.paneID, tasks)
	}
}

// setTasks stores n as the pane's background_shells and background_monitors
// and publishes the agent when they changed, whatever its status.
func (e *Engine) setTasks(paneID string, n agents.BackgroundTasks) {
	e.mu.Lock()
	defer e.mu.Unlock()
	st, ok := e.states[paneID]
	if !ok || (st.public.BackgroundShells == n.Shells && st.public.BackgroundMonitors == n.Monitors) {
		return
	}
	if e.Logger != nil {
		e.Logger.Info("background tasks changed", "pane_id", paneID,
			"shells", n.Shells, "monitors", n.Monitors)
	}
	st.public.BackgroundShells = n.Shells
	st.public.BackgroundMonitors = n.Monitors
	st.public.UpdatedAt = model.Now()
	if e.Relay != nil {
		e.Relay.Send(model.AgentUpdateMsg{
			Type:  model.WireAgentUpdate,
			Agent: st.public,
		})
	}
}

// setBackground stores n as the pane's background_agents and publishes the
// agent when it changed, as long as herdr still reports the pane working.
func (e *Engine) setBackground(paneID string, n int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	st, ok := e.states[paneID]
	if !ok || st.public.Status != model.StatusWorking || st.public.BackgroundAgents == n {
		return
	}
	if e.Logger != nil {
		e.Logger.Info("background agents changed", "pane_id", paneID, "from", st.public.BackgroundAgents, "to", n)
	}
	st.public.BackgroundAgents = n
	st.public.UpdatedAt = model.Now()
	if e.Relay != nil {
		e.Relay.Send(model.AgentUpdateMsg{
			Type:  model.WireAgentUpdate,
			Agent: st.public,
		})
	}
}

// readScreenForHistory captures the pane's recent screen for history. A
// failed capture (a busy herdr can take longer than ScreenReadTimeout) is
// retried once with ScreenRetryTimeout.
func (e *Engine) readScreenForHistory(ctx context.Context, paneID string) (string, error) {
	readCtx, readCancel := context.WithTimeout(ctx, e.ScreenReadTimeout)
	text, err := e.Herdr.Read(readCtx, paneID, herdr.SourceRecentUnwrapped, 200)
	readCancel()
	if err == nil || ctx.Err() != nil {
		return text, err
	}
	if e.Logger != nil {
		e.Logger.Info("screen read for history failed; retrying once", "pane_id", paneID, "err", err)
	}
	readCtx, readCancel = context.WithTimeout(ctx, e.ScreenRetryTimeout)
	defer readCancel()
	return e.Herdr.Read(readCtx, paneID, herdr.SourceRecentUnwrapped, 200)
}

// agentNames returns herdr-core's name for the agent and its terminal title, when
// the title describes a task: a title that only names the program is none.
func (e *Engine) agentNames(info herdr.AgentInfo) (name, title string) {
	if info.Name != nil {
		name = strings.TrimSpace(*info.Name)
	}
	if info.Title != nil {
		agent := ""
		if info.Agent != nil {
			agent = *info.Agent
		}
		var ad agents.Adapter
		if e.Agents != nil {
			ad = e.Agents.For(agent)
		}
		if ad != nil {
			title = ad.TaskTitle(*info.Title)
		} else {
			title = strings.TrimSpace(*info.Title)
		}
	}
	return name, title
}

// customTabLabel returns info.TabLabel unless it is empty or just the tab's number.
func customTabLabel(tabLabel string) string {
	trimmed := strings.TrimSpace(tabLabel)
	if trimmed == "" {
		return ""
	}
	if _, err := strconv.Atoi(trimmed); err == nil {
		return ""
	}
	return trimmed
}

// labelFor names the agent the way the owner did: herdr-core's agent name, else
// its tab's label (unless it is only the tab's number), else its terminal
// title, else the cwd's base name, else the pane id.
func (e *Engine) labelFor(info herdr.AgentInfo, name, title, cwd string) string {
	tab := customTabLabel(info.TabLabel)
	switch {
	case name != "":
		return name
	case tab != "":
		return tab
	case title != "":
		return title
	case cwd != "" && filepath.Base(cwd) != "/" && filepath.Base(cwd) != ".":
		return filepath.Base(cwd)
	}
	return info.PaneID
}

func (e *Engine) buildAgentState(info herdr.AgentInfo) (model.AgentState, string) {
	agentName := ""
	if info.Agent != nil {
		agentName = *info.Agent
	}

	cwd := ""
	if info.CWD != nil && *info.CWD != "" {
		cwd = *info.CWD
	}

	paneName, taskTitle := e.agentNames(info)
	label := e.labelFor(info, paneName, taskTitle, cwd)

	return model.AgentState{
		PaneID:         info.PaneID,
		Agent:          agentName,
		Label:          label,
		Name:           paneName,
		Title:          taskTitle,
		CWD:            cwd,
		WorkspaceID:    info.WorkspaceID,
		Workspace:      info.WorkspaceLabel,
		Status:         model.AgentStatus(info.AgentStatus),
		Focused:        info.Focused,
		StateChangeSeq: info.StateChangeSeq,
		UpdatedAt:      model.Now(),
	}, cwd
}

// ConnectMessages returns the initial hello and snapshot messages for OnConnect.
func (e *Engine) ConnectMessages(ctx context.Context) []any {
	// Read the presence before taking e.mu: it runs a subprocess.
	presence, hasPresence := e.presenceMsg(ctx)

	e.mu.RLock()
	defer e.mu.RUnlock()

	agentsList := make([]model.AgentState, 0, len(e.states))
	for _, st := range e.states {
		agentsList = append(agentsList, st.public)
	}
	model.SortAgents(agentsList)

	hello := model.HelloMsg{
		Type:          model.WireHello,
		Version:       e.Version,
		Host:          e.HostName,
		HerdrVersion:  e.pong.Version,
		HerdrProtocol: e.pong.Protocol,
		HerdrOnline:   e.herdrOnline,
	}

	snapshot := model.SnapshotMsg{
		Type:   model.WireSnapshot,
		Agents: agentsList,
	}

	msgs := []any{hello, snapshot}
	if hasPresence {
		msgs = append(msgs, presence)
	}
	return msgs
}

// Status returns a StatusFile snapshot for the status file writer or CLI status command.
func (e *Engine) Status() StatusFile {
	e.mu.RLock()
	defer e.mu.RUnlock()

	relayConnected := false
	relayErr := ""
	relayVersion := ""
	if e.Relay != nil {
		relayConnected = e.Relay.Connected()
		if !relayConnected {
			relayErr = e.Relay.LastError()
		}
		relayVersion = e.Relay.RelayVersion()
	}

	blocked := 0
	for _, st := range e.states {
		if st.public.Status == model.StatusBlocked {
			blocked++
		}
	}

	var errs []string
	if relayErr != "" {
		errs = append(errs, relayErr)
	}
	if e.herdrErr != "" {
		errs = append(errs, e.herdrErr)
	}

	return StatusFile{
		PID:            os.Getpid(),
		RelayConnected: relayConnected,
		HerdrOnline:    e.herdrOnline,
		Agents:         len(e.states),
		Blocked:        blocked,
		LastError:      strings.Join(errs, "; "),
		RelayError:     relayErr,
		HerdrError:     e.herdrErr,
		Version:        e.Version,
		RelayVersion:   relayVersion,
		UpdatedAt:      model.Now(),
	}
}

// StoppedStatus is the status file of a bridge that is not running: pid 0
// and nothing connected. lastErr says why, when it did not stop cleanly.
func StoppedStatus(version, lastErr string) StatusFile {
	return StatusFile{Version: version, LastError: lastErr, UpdatedAt: model.Now()}
}

// StartStatusWriter writes status.json now and every interval until ctx is
// done, then writes a final stopped status (pid 0) so readers do not see a
// stale "connected". The returned channel is closed after that final write;
// wait for it before exiting. It is closed at once if there is no StatusPath.
func (e *Engine) StartStatusWriter(ctx context.Context, interval time.Duration) <-chan struct{} {
	done := make(chan struct{})
	if e.StatusPath == "" || interval <= 0 {
		close(done)
		return done
	}

	write := func(s StatusFile) {
		if err := WriteStatus(e.StatusPath, s); err != nil && e.Logger != nil {
			e.Logger.Warn("failed to write status file", "path", e.StatusPath, "err", err)
		}
	}

	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		write(e.Status())
		for {
			select {
			case <-ctx.Done():
				write(StoppedStatus(e.Version, ""))
				return
			case <-ticker.C:
				write(e.Status())
			}
		}
	}()
	return done
}

func (e *Engine) isHerdrOnline() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.herdrOnline
}
