package bridge

import (
	"context"
	"errors"
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

	mu            sync.RWMutex
	states        map[string]*paneState
	workspaces    map[string]string // workspace_id -> label ("" when unlabeled); from the last workspace.list
	tabs          map[string]string // tab_id -> the owner's label ("" when it is only the tab's number); from the last tab.list
	wsRefreshing  bool              // a workspace.list is in flight
	wsRefreshedAt time.Time         // when the last workspace.list finished (ok or not)
	wsRefreshes   int               // workspace refreshes started (tests)
	herdrOnline   bool
	pong          herdr.Pong
	herdrErr      string                      // why herdr is offline; "" while online
	consumed      map[string]*consumedPrompts // pane_id -> prompts already acted on (guarded by mu)

	lockMu    sync.Mutex
	paneLocks map[string]*paneLock // pane_id -> lock, only while commands are in flight
	requests  requestIDLog
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
	return &Engine{
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

		states:     make(map[string]*paneState),
		workspaces: make(map[string]string),
		tabs:       make(map[string]string),
	}
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
	if online {
		e.startWorkspaceRefreshLocked()
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

	e.maybeRefreshWorkspacesLocked(changes)

	for _, ch := range changes {
		paneID := ch.Agent.PaneID
		switch ch.Kind {
		case herdr.Removed:
			delete(e.states, paneID)
			e.forgetConsumedLocked(paneID, true, 0)
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
			e.forgetConsumedLocked(paneID, agentName == "", info.StateChangeSeq)

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

			// The turn watch owns the background count; it holds only while
			// herdr keeps the pane working. The task counts hold with any
			// status: they change only when the transcript is read again.
			if pub.Status == model.StatusWorking {
				pub.BackgroundAgents = st.public.BackgroundAgents
			}
			pub.BackgroundShells = st.public.BackgroundShells
			pub.BackgroundMonitors = st.public.BackgroundMonitors

			st.public = pub
			if e.Relay != nil {
				e.Relay.Send(model.AgentUpdateMsg{
					Type:  model.WireAgentUpdate,
					Agent: pub,
				})
			}

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
// terminal titles (agents.ViewDetector).
func (e *Engine) backToConversation(agent string, prev *herdr.AgentInfo, info herdr.AgentInfo) bool {
	if prev == nil {
		return false
	}
	v, ok := e.Agents.For(agent).(agents.ViewDetector)
	if !ok {
		return false
	}
	return !v.ShowsConversation(titleOf(*prev)) && v.ShowsConversation(titleOf(info))
}

func titleOf(info herdr.AgentInfo) string {
	if info.TerminalTitleStripped == nil {
		return ""
	}
	return *info.TerminalTitleStripped
}

func (e *Engine) captureHistory(paneID, cwd, agent string, info herdr.AgentInfo) {
	budget := 2*e.TranscriptTimeout + e.ScreenReadTimeout + e.ScreenRetryTimeout + time.Second
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()

	// The pane's title now, not when the capture was scheduled: it says what
	// the pane shows.
	e.mu.RLock()
	if st, ok := e.states[paneID]; ok && st.info.TerminalTitleStripped != nil {
		info.TerminalTitleStripped = st.info.TerminalTitleStripped
	}
	e.mu.RUnlock()

	ref := info.TrustedSession()
	ad := e.Agents.For(agent)

	if v, ok := ad.(agents.ViewDetector); ok && !v.ShowsConversation(titleOf(info)) {
		if e.Logger != nil {
			e.Logger.Info("no history: the pane shows no conversation", "pane_id", paneID, "agent", agent)
		}
		e.setTasks(paneID, agents.BackgroundTasks{})
		return
	}

	if reader, ok := ad.(agents.BackgroundTaskReader); ok {
		if ref == nil {
			e.setTasks(paneID, agents.BackgroundTasks{})
		} else {
			e.refreshTasks(ctx, paneID, agent, reader, agents.SessionRef{
				Agent: ref.Agent, Kind: ref.Kind, Value: ref.Value, CWD: cwd, Title: titleOf(info),
			})
		}
	}

	var item *model.HistoryItem
	var err error

	if ref != nil {
		readCtx, readCancel := context.WithTimeout(ctx, e.TranscriptTimeout)
		item, err = ad.LastTurn(readCtx, agents.SessionRef{
			Agent: ref.Agent,
			Kind:  ref.Kind,
			Value: ref.Value,
			CWD:   cwd,
			Title: titleOf(info),
		})
		readCancel()
		if errors.Is(err, agents.ErrNoReply) {
			if e.Logger != nil {
				e.Logger.Info("no history: the last turn has no reply yet", "pane_id", paneID, "agent", agent, "reason", err)
			}
			return
		}
		if (err != nil || item == nil) && e.Logger != nil {
			// Never log the transcript itself; the error names the cause (ids only).
			e.Logger.Warn("LastTurn failed; falling back to a screen capture for history",
				"pane_id", paneID, "agent", agent, "session_kind", ref.Kind, "err", err)
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
		item = agents.ScreenTurnFor(ad, text)
		if item == nil && e.Logger != nil {
			e.Logger.Info("no history: the screen shows no conversation", "pane_id", paneID, "agent", agent)
		}
	}

	if item == nil {
		return
	}

	sessionValue := ""
	if ref != nil {
		sessionValue = ref.Value
	}
	e.publishHistory(paneID, agent, sessionValue, *item)
}

// publishHistory sends the pane's last reply to the relay unless it is the
// one sent last for the pane. sessionValue is herdr's session value (or "").
func (e *Engine) publishHistory(paneID, agent, sessionValue string, item model.HistoryItem) {
	item.ID = model.HistoryID(paneID, sessionValue, item.Query, item.Response)
	item.PaneID = paneID
	item.Agent = agent
	item.CompletedAt = model.Now()

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
	paneID, agent string
	reader        agents.TurnEndReader
	tasks         agents.BackgroundTaskReader // nil when the adapter counts none
	ref           agents.SessionRef
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
		reader, ok := ad.(agents.TurnEndReader)
		if !ok {
			continue
		}
		if v, ok := ad.(agents.ViewDetector); ok && !v.ShowsConversation(titleOf(st.info)) {
			hidden = append(hidden, paneID)
			continue
		}
		ref := st.info.TrustedSession()
		if ref == nil {
			hidden = append(hidden, paneID)
			continue
		}
		tasks, _ := ad.(agents.BackgroundTaskReader)
		panes = append(panes, workingPane{paneID: paneID, agent: st.public.Agent, reader: reader, tasks: tasks, ref: agents.SessionRef{
			Agent: ref.Agent, Kind: ref.Kind, Value: ref.Value, CWD: st.public.CWD, Title: titleOf(st.info),
		}})
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
		readCtx, cancel := context.WithTimeout(ctx, e.TranscriptTimeout)
		end, err := p.reader.LastCompletedTurn(readCtx, p.ref)
		cancel()
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
			e.publishHistory(p.paneID, p.agent, p.ref.Value, *end.Item)
		}
		e.setBackground(p.paneID, end.Background)
		if p.tasks != nil {
			e.refreshTasks(ctx, p.paneID, p.agent, p.tasks, p.ref)
		}
	}
}

// refreshTasks reads the pane's background shells and monitors and
// publishes them when they changed. A failed read keeps the counts: it says
// nothing new.
func (e *Engine) refreshTasks(ctx context.Context, paneID, agent string, reader agents.BackgroundTaskReader, ref agents.SessionRef) {
	readCtx, cancel := context.WithTimeout(ctx, e.TranscriptTimeout)
	n, err := reader.BackgroundTasks(readCtx, ref)
	cancel()
	if err != nil {
		if e.Logger != nil {
			e.Logger.Debug("background task read failed", "pane_id", paneID, "agent", agent, "err", err)
		}
		return
	}
	e.setTasks(paneID, n)
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

const (
	// wsRetryInterval is the minimum gap between workspace.list calls
	// triggered by a workspace the engine has not seen yet.
	wsRetryInterval = 5 * time.Second
	// wsMaxAge is how stale workspace labels may get while agents change;
	// it picks up renames without a call per change batch.
	wsMaxAge = 60 * time.Second
)

// maybeRefreshWorkspacesLocked starts a workspace.list when a change names a
// workspace missing from the last list (at most every wsRetryInterval), or
// when the labels are older than wsMaxAge. Unlabeled workspaces are cached
// too, so they no longer cause a call per batch. Callers hold e.mu.
func (e *Engine) maybeRefreshWorkspacesLocked(changes []herdr.Change) {
	if e.Herdr == nil || e.wsRefreshing {
		return
	}
	unknown := false
	for _, ch := range changes {
		if ch.Kind != herdr.Added && ch.Kind != herdr.Updated {
			continue
		}
		if _, ok := e.workspaces[ch.Agent.WorkspaceID]; !ok {
			unknown = true
			break
		}
		if _, ok := e.tabs[ch.Agent.TabID]; !ok && ch.Agent.TabID != "" {
			unknown = true
			break
		}
	}
	age := time.Since(e.wsRefreshedAt)
	if (unknown && age >= wsRetryInterval) || age >= wsMaxAge {
		e.startWorkspaceRefreshLocked()
	}
}

// startWorkspaceRefreshLocked runs one workspace.list in the background
// unless one is already in flight. Callers hold e.mu.
func (e *Engine) startWorkspaceRefreshLocked() {
	if e.Herdr == nil || e.wsRefreshing {
		return
	}
	e.wsRefreshing = true
	e.wsRefreshes++
	go e.refreshWorkspaces(context.Background())
}

// refreshWorkspaces replaces the workspace and tab label caches with herdr's
// lists and republishes agents whose workspace label or label changed (an
// agent's label can be its tab's).
func (e *Engine) refreshWorkspaces(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	wsList, err := e.Herdr.ListWorkspaces(ctx)
	var tabList []herdr.TabInfo
	var tabErr error
	if err == nil {
		tabList, tabErr = e.Herdr.ListTabs(ctx)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	e.wsRefreshing = false
	e.wsRefreshedAt = time.Now()

	if err != nil {
		if e.Logger != nil {
			e.Logger.Warn("refresh workspaces failed", "err", err)
		}
		return
	}

	labels := make(map[string]string, len(wsList))
	for _, ws := range wsList {
		labels[ws.WorkspaceID] = ws.Label
	}
	e.workspaces = labels

	if tabErr != nil {
		if e.Logger != nil {
			e.Logger.Warn("refresh tabs failed", "err", tabErr)
		}
	} else {
		tabs := make(map[string]string, len(tabList))
		for _, t := range tabList {
			label := strings.TrimSpace(t.Label)
			if label == strconv.Itoa(t.Number) { // herdr's default: only the tab's number
				label = ""
			}
			tabs[t.TabID] = label
		}
		e.tabs = tabs
	}

	if e.Logger != nil {
		e.Logger.Debug("refreshed workspaces", "count", len(wsList), "workspaces", labels, "tabs", len(tabList))
	}

	for _, st := range e.states {
		wsLabel := labels[st.info.WorkspaceID]
		name, title := agentNames(st.info)
		label := e.labelFor(st.info, name, title, st.public.CWD)
		if st.public.Workspace == wsLabel && st.public.Label == label {
			continue
		}
		st.public.Workspace = wsLabel
		st.public.Label = label
		if e.Relay != nil {
			e.Relay.Send(model.AgentUpdateMsg{
				Type:  model.WireAgentUpdate,
				Agent: st.public,
			})
		}
	}
}

// agentNames returns herdr's name for the agent and its terminal title, when
// the title describes a task: a title that only names the program is none.
func agentNames(info herdr.AgentInfo) (name, title string) {
	if info.Name != nil {
		name = strings.TrimSpace(*info.Name)
	}
	if info.TerminalTitleStripped != nil {
		t := strings.TrimSpace(*info.TerminalTitleStripped)
		if t != "" && !strings.HasPrefix(t, "agy --conversation") && t != "OpenCode" {
			title = t
		}
	}
	return name, title
}

// labelFor names the agent the way the owner did: herdr's agent name, else
// its tab's label (unless it is only the tab's number), else its terminal
// title, else the cwd's base name, else the pane id. Callers hold e.mu.
func (e *Engine) labelFor(info herdr.AgentInfo, name, title, cwd string) string {
	switch {
	case name != "":
		return name
	case e.tabs[info.TabID] != "":
		return e.tabs[info.TabID]
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
	if info.ForegroundCWD != nil && *info.ForegroundCWD != "" {
		cwd = *info.ForegroundCWD
	} else if info.CWD != nil && *info.CWD != "" {
		cwd = *info.CWD
	}

	paneName, taskTitle := agentNames(info)
	label := e.labelFor(info, paneName, taskTitle, cwd)

	wsLabel := ""
	if e.workspaces != nil {
		wsLabel = e.workspaces[info.WorkspaceID]
	}

	return model.AgentState{
		PaneID:         info.PaneID,
		Agent:          agentName,
		Label:          label,
		Name:           paneName,
		Title:          taskTitle,
		CWD:            cwd,
		WorkspaceID:    info.WorkspaceID,
		Workspace:      wsLabel,
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
