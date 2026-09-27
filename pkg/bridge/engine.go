package bridge

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	// CommandTimeout bounds a command from its arrival, including the wait
	// for the pane lock (defaults to 9s, below the relay's 10s wait).
	CommandTimeout time.Duration

	mu            sync.RWMutex
	states        map[string]*paneState
	workspaces    map[string]string // workspace_id -> label ("" when unlabeled); from the last workspace.list
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
		states:       make(map[string]*paneState),
		workspaces:   make(map[string]string),
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

			st.public = pub
			if e.Relay != nil {
				e.Relay.Send(model.AgentUpdateMsg{
					Type:  model.WireAgentUpdate,
					Agent: pub,
				})
			}

			// History trigger: working -> done|idle
			if prevStatus == string(model.StatusWorking) &&
				(pub.Status == model.StatusDone || pub.Status == model.StatusIdle) {
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

func (e *Engine) captureHistory(paneID, cwd, agent string, info herdr.AgentInfo) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ref := info.TrustedSession()
	ad := e.Agents.For(agent)

	var item *model.HistoryItem
	var err error

	if ref != nil {
		readCtx, readCancel := context.WithTimeout(ctx, 3*time.Second)
		item, err = ad.LastTurn(readCtx, agents.SessionRef{
			Agent: ref.Agent,
			Kind:  ref.Kind,
			Value: ref.Value,
			CWD:   cwd,
		})
		readCancel()
		if (err != nil || item == nil) && e.Logger != nil {
			// Never log the transcript itself; the error names the cause.
			e.Logger.Warn("LastTurn failed; falling back to a screen capture for history",
				"pane_id", paneID, "agent", agent, "session_kind", ref.Kind, "err", err)
		}
	}

	if err != nil || item == nil {
		readCtx, readCancel := context.WithTimeout(ctx, 3*time.Second)
		text, readErr := e.Herdr.Read(readCtx, paneID, herdr.SourceRecentUnwrapped, 200)
		readCancel()
		if readErr != nil {
			if e.Logger != nil {
				e.Logger.Warn("screen read fallback failed for history", "pane_id", paneID, "err", readErr)
			}
			return
		}
		item = agents.ScreenTurnFor(e.Agents.For(agent), text)
	}

	if item == nil {
		return
	}

	sessionValue := ""
	if ref != nil {
		sessionValue = ref.Value
	}

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
			Item: *item,
		})
	}
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

// refreshWorkspaces replaces the workspace label cache with herdr's list and
// republishes agents whose workspace label changed.
func (e *Engine) refreshWorkspaces(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	wsList, err := e.Herdr.ListWorkspaces(ctx)

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

	if e.Logger != nil {
		e.Logger.Debug("refreshed workspaces", "count", len(wsList), "workspaces", labels)
	}

	for _, st := range e.states {
		wsLabel := labels[st.info.WorkspaceID]
		if st.public.Workspace == wsLabel {
			continue
		}
		st.public.Workspace = wsLabel
		if e.Relay != nil {
			e.Relay.Send(model.AgentUpdateMsg{
				Type:  model.WireAgentUpdate,
				Agent: st.public,
			})
		}
	}
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

	taskTitle := ""
	if info.TerminalTitleStripped != nil {
		t := strings.TrimSpace(*info.TerminalTitleStripped)
		if t != "" && !strings.HasPrefix(t, "agy --conversation") && t != "OpenCode" {
			taskTitle = t
		}
	}

	paneName := ""
	if info.Name != nil && strings.TrimSpace(*info.Name) != "" {
		paneName = strings.TrimSpace(*info.Name)
	}

	label := ""
	if taskTitle != "" {
		label = taskTitle
	} else if paneName != "" {
		label = paneName
	} else if cwd != "" && filepath.Base(cwd) != "/" && filepath.Base(cwd) != "." {
		label = filepath.Base(cwd)
	} else {
		label = info.PaneID
	}

	wsLabel := ""
	if e.workspaces != nil {
		wsLabel = e.workspaces[info.WorkspaceID]
	}

	return model.AgentState{
		PaneID:         info.PaneID,
		Agent:          agentName,
		Label:          label,
		Name:           paneName,
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

	return []any{hello, snapshot}
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
