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

	mu          sync.RWMutex
	states      map[string]*paneState
	herdrOnline bool
	pong        herdr.Pong
	lastError   string
	cmdLocks    sync.Map // pane_id -> *sync.Mutex
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
	}
}

// OnHerdrOnline implements herdr.Listener.
func (e *Engine) OnHerdrOnline(online bool, pong herdr.Pong) {
	e.mu.Lock()
	e.herdrOnline = online
	e.pong = pong
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
					// Preserve existing prompt until resolved
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
			select {
			case <-ctx.Done():
				break
			case <-time.After(delay):
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
		item = agents.ScreenTurn(text)
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

	label := ""
	if info.Name != nil && strings.TrimSpace(*info.Name) != "" {
		label = strings.TrimSpace(*info.Name)
	} else if info.TerminalTitleStripped != nil && strings.TrimSpace(*info.TerminalTitleStripped) != "" {
		label = strings.TrimSpace(*info.TerminalTitleStripped)
	} else if cwd != "" && filepath.Base(cwd) != "/" && filepath.Base(cwd) != "." {
		label = filepath.Base(cwd)
	} else {
		label = info.PaneID
	}

	return model.AgentState{
		PaneID:         info.PaneID,
		Agent:          agentName,
		Label:          label,
		CWD:            cwd,
		WorkspaceID:    info.WorkspaceID,
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
	if e.Relay != nil {
		relayConnected = e.Relay.Connected()
	}

	return StatusFile{
		PID:            os.Getpid(),
		RelayConnected: relayConnected,
		HerdrOnline:    e.herdrOnline,
		Agents:         len(e.states),
		LastError:      e.lastError,
		UpdatedAt:      model.Now(),
	}
}

// StartStatusWriter starts a goroutine that periodically writes status.json.
func (e *Engine) StartStatusWriter(ctx context.Context, interval time.Duration) {
	if e.StatusPath == "" || interval <= 0 {
		return
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		// Write initial status immediately
		_ = WriteStatus(e.StatusPath, e.Status())

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := WriteStatus(e.StatusPath, e.Status()); err != nil {
					if e.Logger != nil {
						e.Logger.Warn("failed to write status file", "path", e.StatusPath, "err", err)
					}
				}
			}
		}
	}()
}

func (e *Engine) isHerdrOnline() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.herdrOnline
}

func (e *Engine) getPaneLock(paneID string) *sync.Mutex {
	v, _ := e.cmdLocks.LoadOrStore(paneID, &sync.Mutex{})
	return v.(*sync.Mutex)
}
