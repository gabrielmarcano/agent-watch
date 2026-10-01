package bridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gabrielmarcano/agent-monitor/pkg/agents"
	"github.com/gabrielmarcano/agent-monitor/pkg/herdr"
	"github.com/gabrielmarcano/agent-monitor/pkg/herdrtest"
	"github.com/gabrielmarcano/agent-monitor/pkg/model"
	"github.com/gabrielmarcano/agent-monitor/pkg/relayclient"
)

type testHarness struct {
	server       *herdrtest.Server
	herdrClient  *herdr.Client
	syncer       *herdr.Syncer
	registry     *agents.Registry
	relayServer  *httptest.Server
	relayClient  *relayclient.Client
	engine       *Engine
	relayMsgs    chan any
	activeWsConn *websocket.Conn
	connMu       sync.Mutex
	runCancel    context.CancelFunc
}

func newTestHarness(t *testing.T) *testHarness {
	hServer := herdrtest.New(t)
	hClient := &herdr.Client{
		SocketPath: hServer.SocketPath,
		Timeout:    2 * time.Second,
	}

	reg := agents.NewRegistry(agents.Config{})
	relayMsgs := make(chan any, 100)
	syncer := &herdr.Syncer{Client: hClient}

	h := &testHarness{
		server:      hServer,
		herdrClient: hClient,
		syncer:      syncer,
		registry:    reg,
		relayMsgs:   relayMsgs,
	}

	relaySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")

		h.connMu.Lock()
		h.activeWsConn = conn
		h.connMu.Unlock()

		for {
			typ, data, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			if typ == websocket.MessageText {
				msg, err := model.DecodeWire(data)
				if err == nil {
					relayMsgs <- msg
				}
			}
		}
	}))

	t.Cleanup(func() {
		relaySrv.Close()
	})

	wsURL := "ws" + strings.TrimPrefix(relaySrv.URL, "http") + "/v1/host"
	rClient := &relayclient.Client{
		URL:        wsURL,
		Token:      "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		MinBackoff: 20 * time.Millisecond,
	}

	engine := NewEngine(hClient, syncer, reg, rClient, "0.2.0", "test-host", "", nil)
	engine.RetryDelay = 10 * time.Millisecond
	engine.HistoryDelay = 10 * time.Millisecond
	syncer.Listener = engine

	rClient.OnConnect = engine.ConnectMessages
	rClient.OnMessage = engine.HandleRelayMessage

	runCtx, runCancel := context.WithCancel(context.Background())
	go func() { _ = rClient.Run(runCtx) }()

	t.Cleanup(func() {
		runCancel()
	})

	h.relayServer = relaySrv
	h.relayClient = rClient
	h.engine = engine
	h.runCancel = runCancel

	// Wait for client to connect to relay
	for i := 0; i < 50; i++ {
		h.connMu.Lock()
		conn := h.activeWsConn
		h.connMu.Unlock()
		if rClient.Connected() && conn != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	return h
}

func (h *testHarness) sendRelayToBridge(t *testing.T, msg any) {
	t.Helper()
	h.connMu.Lock()
	conn := h.activeWsConn
	h.connMu.Unlock()

	if conn == nil {
		t.Fatal("no active websocket connection from bridge to fake relay")
	}

	wireBytes, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("encode wire: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := conn.Write(ctx, websocket.MessageText, wireBytes); err != nil {
		t.Fatalf("write wire: %v", err)
	}
}

// The label is the name the owner gave: herdr's agent name, else the tab's
// label (unless it is only the tab's number), else the agent's own terminal
// title, else the cwd's base name, else the pane id. The title travels apart.
func TestEngine_AgentStateMapping(t *testing.T) {
	name := "custom-name"
	title := "stripped-title"
	cwd := "/Users/test/Code/repo"
	fgCwd := "/Users/test/Code/repo/subdir"
	agent := "claude"

	e := &Engine{tabs: map[string]string{"w1:t1": "tareas"}}

	// 1. herdr's agent name wins over the tab and the title.
	st1, resolvedCwd := e.buildAgentState(herdr.AgentInfo{
		PaneID:                "w1:p1",
		TabID:                 "w1:t1",
		Agent:                 &agent,
		Name:                  &name,
		TerminalTitleStripped: &title,
		CWD:                   &cwd,
		ForegroundCWD:         &fgCwd,
		AgentStatus:           "idle",
		StateChangeSeq:        10,
	})
	if st1.Label != "custom-name" || st1.Name != "custom-name" || st1.Title != "stripped-title" {
		t.Errorf("label/name/title = %q/%q/%q, want custom-name/custom-name/stripped-title", st1.Label, st1.Name, st1.Title)
	}
	if resolvedCwd != "/Users/test/Code/repo/subdir" || st1.CWD != "/Users/test/Code/repo/subdir" {
		t.Errorf("cwd = %q, want /Users/test/Code/repo/subdir (foreground preference)", st1.CWD)
	}

	// 2. No agent name: the tab's label wins over the title.
	st2, _ := e.buildAgentState(herdr.AgentInfo{
		PaneID:                "w1:p2",
		TabID:                 "w1:t1",
		Agent:                 &agent,
		TerminalTitleStripped: &title,
		AgentStatus:           "working",
	})
	if st2.Label != "tareas" || st2.Title != "stripped-title" {
		t.Errorf("label/title = %q/%q, want tareas/stripped-title", st2.Label, st2.Title)
	}

	// 3. A tab without a label of its own (not in the map): the title.
	st3, _ := e.buildAgentState(herdr.AgentInfo{
		PaneID:                "w1:p3",
		TabID:                 "w1:t2",
		Agent:                 &agent,
		TerminalTitleStripped: &title,
		CWD:                   &cwd,
		AgentStatus:           "idle",
	})
	if st3.Label != "stripped-title" {
		t.Errorf("label = %q, want stripped-title", st3.Label)
	}

	// 4. Titles that are only the program, not a task, are no title.
	for _, generic := range []string{"OpenCode", "agy --conversation 1234"} {
		g := generic
		st, _ := e.buildAgentState(herdr.AgentInfo{PaneID: "w1:p4", Agent: &agent, TerminalTitleStripped: &g, CWD: &cwd})
		if st.Label != "repo" || st.Title != "" {
			t.Errorf("title %q: label/title = %q/%q, want repo/\"\"", generic, st.Label, st.Title)
		}
	}

	// 5. PaneID fallback when everything else is empty
	st5, _ := e.buildAgentState(herdr.AgentInfo{
		PaneID:      "w1:p5",
		Agent:       &agent,
		AgentStatus: "idle",
	})
	if st5.Label != "w1:p5" {
		t.Errorf("label = %q, want w1:p5", st5.Label)
	}
}

// Tab labels come from tab.list with the workspace labels; a tab whose label
// is only its number has no name. A rename reaches the relay on the next
// refresh, as a new label for the agents in that tab.
func TestEngine_TabLabelNamesTheAgent(t *testing.T) {
	h := newTestHarness(t)
	h.server.SetTabs([]map[string]any{
		{"tab_id": "w1:t1", "workspace_id": "w1", "number": 1, "label": "tareas", "pane_count": 1},
		{"tab_id": "w1:t2", "workspace_id": "w1", "number": 2, "label": "2", "pane_count": 1},
	})
	h.engine.OnHerdrOnline(true, herdr.Pong{Version: "0.9.1", Protocol: 22})
	pollUntil(t, 3*time.Second, "tab labels", func() bool {
		h.engine.mu.RLock()
		defer h.engine.mu.RUnlock()
		return h.engine.tabs["w1:t1"] == "tareas" && !h.engine.wsRefreshing
	})
	h.engine.mu.RLock()
	numbered := h.engine.tabs["w1:t2"] != ""
	h.engine.mu.RUnlock()
	if numbered {
		t.Error("a tab labelled with its own number is kept as a name")
	}

	agent, title := "claude", "Claude's own title"
	h.engine.OnChanges([]herdr.Change{{Kind: herdr.Added, Agent: herdr.AgentInfo{
		PaneID: "w1:p1", WorkspaceID: "w1", TabID: "w1:t1", Agent: &agent,
		TerminalTitleStripped: &title, AgentStatus: "idle", StateChangeSeq: 1,
	}}})
	msg, _ := waitMsg(h, 3*time.Second, func(m any) bool {
		u, ok := m.(model.AgentUpdateMsg)
		return ok && u.Agent.PaneID == "w1:p1"
	})
	if msg == nil {
		t.Fatal("no agent_update for w1:p1")
	}
	if u := msg.(model.AgentUpdateMsg); u.Agent.Label != "tareas" || u.Agent.Title != title {
		t.Errorf("label/title = %q/%q, want tareas/%q", u.Agent.Label, u.Agent.Title, title)
	}

	// The tab is renamed: the next refresh republishes the agent.
	h.server.SetTabs([]map[string]any{
		{"tab_id": "w1:t1", "workspace_id": "w1", "number": 1, "label": "agenda", "pane_count": 1},
	})
	h.engine.mu.Lock()
	h.engine.startWorkspaceRefreshLocked()
	h.engine.mu.Unlock()
	if msg, _ := waitMsg(h, 3*time.Second, func(m any) bool {
		u, ok := m.(model.AgentUpdateMsg)
		return ok && u.Agent.PaneID == "w1:p1" && u.Agent.Label == "agenda"
	}); msg == nil {
		t.Error("the renamed tab never reached the relay")
	}
}

func TestEngine_PromptResolution(t *testing.T) {
	h := newTestHarness(t)

	// Fixture screen from claude
	fixturePath := filepath.Join("..", "agents", "testdata", "claude", "permission-bash.txt")
	fixture, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture %s: %v", fixturePath, err)
	}

	h.server.SetAgents([]map[string]any{
		{
			"pane_id":          "w1:p1",
			"workspace_id":     "w1",
			"tab_id":           "t1",
			"agent":            "claude",
			"agent_status":     "blocked",
			"state_change_seq": 100,
		},
	})
	h.server.SetScreen("w1:p1", "visible", string(fixture))

	agentClaude := "claude"
	// Deliver change to Engine
	h.engine.OnHerdrOnline(true, herdr.Pong{Version: "0.9.1", Protocol: 22})
	h.engine.OnChanges([]herdr.Change{
		{
			Kind: herdr.Added,
			Agent: herdr.AgentInfo{
				PaneID:         "w1:p1",
				WorkspaceID:    "w1",
				TabID:          "t1",
				Agent:          &agentClaude,
				AgentStatus:    "blocked",
				StateChangeSeq: 100,
			},
		},
	})

	// Wait for prompt resolution and AgentUpdateMsg
	deadline := time.Now().Add(3 * time.Second)
	var gotUpdate *model.AgentUpdateMsg
	for time.Now().Before(deadline) {
		select {
		case msg := <-h.relayMsgs:
			if u, ok := msg.(model.AgentUpdateMsg); ok && u.Agent.PaneID == "w1:p1" {
				if u.Agent.Prompt != nil {
					gotUpdate = &u
					break
				}
			}
		case <-time.After(30 * time.Millisecond):
		}
		if gotUpdate != nil {
			break
		}
	}

	if gotUpdate == nil {
		t.Fatal("timed out waiting for AgentUpdateMsg with parsed prompt")
	}

	prompt := gotUpdate.Agent.Prompt
	if prompt.Kind != model.PromptPermission {
		t.Errorf("prompt.Kind = %q, want %q", prompt.Kind, model.PromptPermission)
	}
	if len(prompt.Options) != 4 {
		t.Fatalf("expected 4 options, got %d", len(prompt.Options))
	}
	if prompt.Options[0].Role != model.RoleAllowOnce {
		t.Errorf("option 0 role = %q, want %q", prompt.Options[0].Role, model.RoleAllowOnce)
	}
	if prompt.Options[1].Role != model.RoleAllowAlways {
		t.Errorf("option 1 role = %q, want %q", prompt.Options[1].Role, model.RoleAllowAlways)
	}
	if prompt.Options[2].Role != model.RoleAllowAlways {
		t.Errorf("option 2 role = %q, want %q", prompt.Options[2].Role, model.RoleAllowAlways)
	}
	if prompt.Options[3].Role != model.RoleDeny {
		t.Errorf("option 3 role = %q, want %q", prompt.Options[3].Role, model.RoleDeny)
	}
	if prompt.Fingerprint == "" {
		t.Error("prompt fingerprint is empty")
	}
}

func TestEngine_PromptRetryThenUnknown(t *testing.T) {
	h := newTestHarness(t)

	// Screen that has no menu at all
	noMenu := "Some regular terminal output without any prompt\nLine 2\nLine 3\n"
	h.server.SetScreen("w1:p1", "visible", noMenu)

	agentClaude := "claude"
	h.engine.OnHerdrOnline(true, herdr.Pong{Version: "0.9.1", Protocol: 22})
	h.engine.OnChanges([]herdr.Change{
		{
			Kind: herdr.Added,
			Agent: herdr.AgentInfo{
				PaneID:         "w1:p1",
				WorkspaceID:    "w1",
				TabID:          "t1",
				Agent:          &agentClaude,
				AgentStatus:    "blocked",
				StateChangeSeq: 101,
			},
		},
	})

	// Wait for AgentUpdateMsg with Kind == unknown
	deadline := time.Now().Add(3 * time.Second)
	var gotUpdate *model.AgentUpdateMsg
	for time.Now().Before(deadline) {
		select {
		case msg := <-h.relayMsgs:
			if u, ok := msg.(model.AgentUpdateMsg); ok && u.Agent.PaneID == "w1:p1" {
				if u.Agent.Prompt != nil {
					gotUpdate = &u
					break
				}
			}
		case <-time.After(30 * time.Millisecond):
		}
		if gotUpdate != nil {
			break
		}
	}

	if gotUpdate == nil {
		t.Fatal("timed out waiting for unknown AgentUpdateMsg")
	}

	if gotUpdate.Agent.Prompt.Kind != model.PromptUnknown {
		t.Errorf("prompt.Kind = %q, want %q", gotUpdate.Agent.Prompt.Kind, model.PromptUnknown)
	}
	if gotUpdate.Agent.Prompt.RawTail == "" {
		t.Error("expected non-empty RawTail for unknown prompt")
	}
}

func TestEngine_HistoryCaptureAndDeduplication(t *testing.T) {
	h := newTestHarness(t)

	agentClaude := "claude"
	screenText := "user: run the tests\nassistant: All tests passed successfully."
	h.server.SetScreen("w1:p1", "recent_unwrapped", screenText)

	h.engine.OnHerdrOnline(true, herdr.Pong{Version: "0.9.1", Protocol: 22})

	// Initial working state
	h.engine.OnChanges([]herdr.Change{
		{
			Kind: herdr.Added,
			Agent: herdr.AgentInfo{
				PaneID:         "w1:p1",
				WorkspaceID:    "w1",
				TabID:          "t1",
				Agent:          &agentClaude,
				AgentStatus:    "working",
				StateChangeSeq: 10,
			},
		},
	})

	// Transition working -> done
	prevWorking := herdr.AgentInfo{
		PaneID:         "w1:p1",
		WorkspaceID:    "w1",
		TabID:          "t1",
		Agent:          &agentClaude,
		AgentStatus:    "working",
		StateChangeSeq: 10,
	}
	currDone := herdr.AgentInfo{
		PaneID:         "w1:p1",
		WorkspaceID:    "w1",
		TabID:          "t1",
		Agent:          &agentClaude,
		AgentStatus:    "done",
		StateChangeSeq: 11,
	}

	h.engine.OnChanges([]herdr.Change{
		{
			Kind:  herdr.Updated,
			Agent: currDone,
			Prev:  &prevWorking,
		},
	})

	// Check HistoryItemMsg received
	deadline := time.Now().Add(3 * time.Second)
	var histMsg *model.HistoryItemMsg
	for time.Now().Before(deadline) {
		select {
		case msg := <-h.relayMsgs:
			if hm, ok := msg.(model.HistoryItemMsg); ok {
				histMsg = &hm
				break
			}
		case <-time.After(30 * time.Millisecond):
		}
		if histMsg != nil {
			break
		}
	}

	if histMsg == nil {
		t.Fatal("timed out waiting for HistoryItemMsg")
	}

	if histMsg.Item.PaneID != "w1:p1" {
		t.Errorf("history item pane_id = %q, want w1:p1", histMsg.Item.PaneID)
	}
	if histMsg.Item.Source != "screen" {
		t.Errorf("history item source = %q, want screen", histMsg.Item.Source)
	}

	// Trigger working -> done again with the exact same content: should be deduplicated
	h.engine.OnChanges([]herdr.Change{
		{
			Kind:  herdr.Updated,
			Agent: currDone,
			Prev:  &prevWorking,
		},
	})

	var secondHist atomic.Bool
	go func() {
		for {
			select {
			case msg := <-h.relayMsgs:
				if _, ok := msg.(model.HistoryItemMsg); ok {
					secondHist.Store(true)
				}
			case <-time.After(200 * time.Millisecond):
				return
			}
		}
	}()

	time.Sleep(300 * time.Millisecond)
	if secondHist.Load() {
		t.Error("duplicate history item was sent, but should have been deduplicated")
	}
}
