package bridge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/herdr"
	"github.com/gabrielmarcano/agent-monitor/pkg/herdrtest"
	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

// runHostCmd sends a host-level command through the relay link and returns its
// result. Other wire messages that arrive meanwhile are dropped.
func runHostCmd(t *testing.T, h *testHarness, cmd model.CommandMsg) model.CommandResultMsg {
	t.Helper()
	if cmd.Type == "" {
		cmd.Type = model.WireCommand
	}
	h.sendRelayToBridge(t, cmd)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case msg := <-h.relayMsgs:
			if res, ok := msg.(model.CommandResultMsg); ok && res.RequestID == cmd.RequestID {
				return res
			}
		case <-time.After(20 * time.Millisecond):
		}
	}
	t.Fatalf("timed out waiting for %s result (request_id=%s)", cmd.Action, cmd.RequestID)
	return model.CommandResultMsg{}
}

// newFolder creates name under a temp dir and returns both its path as written
// and its symlink-resolved path, which is what the bridge reports.
func newFolder(t *testing.T, base, name string) (string, string) {
	t.Helper()
	dir := filepath.Join(base, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("resolve %s: %v", dir, err)
	}
	return dir, resolved
}

// callsFrom returns the calls after index n whose method is method.
func callsFrom(calls []herdrtest.Call, n int, method string) []herdrtest.Call {
	var out []herdrtest.Call
	for _, c := range calls[n:] {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSessionOptions_DefaultsAndConfiguredRoots(t *testing.T) {
	h := newTestHarness(t)
	base := t.TempDir()
	rootA, resolvedA := newFolder(t, base, "work")
	_, resolvedB := newFolder(t, base, "personal")

	// No configuration: kinds default, roots empty (session creation is off).
	res := runHostCmd(t, h, model.CommandMsg{
		RequestID: "req-options-off",
		Action:    model.ActionSessionOptions,
	})
	if !res.OK {
		t.Fatalf("session.options failed: code=%s msg=%s", res.ErrorCode, res.Message)
	}
	if res.Options == nil {
		t.Fatal("session.options returned no options payload")
	}
	if len(res.Options.Roots) != 0 {
		t.Errorf("roots = %v, want empty", res.Options.Roots)
	}
	if !equalStrings(res.Options.Kinds, DefaultSessionKinds) {
		t.Errorf("kinds = %v, want %v", res.Options.Kinds, DefaultSessionKinds)
	}

	// Configured: duplicates, relative and missing roots are dropped; symlinks
	// resolve; kinds are trimmed and deduplicated.
	h.engine.SessionRoots = []string{
		rootA,
		resolvedB,
		rootA,
		"relative/root",
		filepath.Join(base, "does-not-exist"),
	}
	h.engine.SessionKinds = []string{" claude ", "claude", "", "opencode"}

	res = runHostCmd(t, h, model.CommandMsg{
		RequestID: "req-options-on",
		Action:    model.ActionSessionOptions,
	})
	if !res.OK || res.Options == nil {
		t.Fatalf("session.options failed: ok=%v code=%s", res.OK, res.ErrorCode)
	}
	if want := []string{resolvedA, resolvedB}; !equalStrings(res.Options.Roots, want) {
		t.Errorf("roots = %v, want %v", res.Options.Roots, want)
	}
	if want := []string{"claude", "opencode"}; !equalStrings(res.Options.Kinds, want) {
		t.Errorf("kinds = %v, want %v", res.Options.Kinds, want)
	}

	// A file configured as a root is not a folder: it is dropped too.
	file := filepath.Join(base, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	h.engine.SessionRoots = []string{file, rootA}
	res = runHostCmd(t, h, model.CommandMsg{
		RequestID: "req-options-file",
		Action:    model.ActionSessionOptions,
	})
	if !res.OK || res.Options == nil {
		t.Fatalf("session.options failed: ok=%v code=%s", res.OK, res.ErrorCode)
	}
	if want := []string{resolvedA}; !equalStrings(res.Options.Roots, want) {
		t.Errorf("roots = %v, want %v (the file must be dropped)", res.Options.Roots, want)
	}
}

func TestFoldersList_RootsChildrenAndParent(t *testing.T) {
	h := newTestHarness(t)
	base := t.TempDir()
	root, resolvedRoot := newFolder(t, base, "work")
	_, resolvedAlpha := newFolder(t, root, "alpha")
	_, resolvedBeta := newFolder(t, root, "beta")
	_, resolvedNested := newFolder(t, filepath.Join(root, "alpha"), "nested")
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	h.engine.SessionRoots = []string{root}

	// Listing the roots: no path yet.
	res := runHostCmd(t, h, model.CommandMsg{
		RequestID: "req-folders-roots",
		Action:    model.ActionFoldersList,
	})
	if !res.OK || res.Folders == nil {
		t.Fatalf("folders.list failed: ok=%v code=%s msg=%s", res.OK, res.ErrorCode, res.Message)
	}
	if res.Folders.Path != "" {
		t.Errorf("path = %q, want empty when listing roots", res.Folders.Path)
	}
	wantEntries := []model.FolderEntry{{Name: "work", Path: resolvedRoot}}
	if len(res.Folders.Entries) != 1 || res.Folders.Entries[0] != wantEntries[0] {
		t.Errorf("root entries = %+v, want %+v", res.Folders.Entries, wantEntries)
	}

	// Listing a root: only folders, sorted, no parent (it sits above the root).
	res = runHostCmd(t, h, model.CommandMsg{
		RequestID: "req-folders-root",
		Action:    model.ActionFoldersList,
		Path:      root,
	})
	if !res.OK || res.Folders == nil {
		t.Fatalf("folders.list failed: ok=%v code=%s msg=%s", res.OK, res.ErrorCode, res.Message)
	}
	if res.Folders.Path != resolvedRoot {
		t.Errorf("path = %q, want %q", res.Folders.Path, resolvedRoot)
	}
	wantEntries = []model.FolderEntry{
		{Name: "alpha", Path: resolvedAlpha},
		{Name: "beta", Path: resolvedBeta},
	}
	if len(res.Folders.Entries) != 2 || res.Folders.Entries[0] != wantEntries[0] || res.Folders.Entries[1] != wantEntries[1] {
		t.Errorf("entries = %+v, want %+v", res.Folders.Entries, wantEntries)
	}
	if res.Folders.Parent != "" {
		t.Errorf("parent = %q, want empty above the root", res.Folders.Parent)
	}
	if res.Folders.Truncated {
		t.Error("truncated = true, want false")
	}

	// Listing a child: parent points back inside the root.
	res = runHostCmd(t, h, model.CommandMsg{
		RequestID: "req-folders-child",
		Action:    model.ActionFoldersList,
		Path:      resolvedAlpha,
	})
	if !res.OK || res.Folders == nil {
		t.Fatalf("folders.list failed: ok=%v code=%s", res.OK, res.ErrorCode)
	}
	if res.Folders.Parent != resolvedRoot {
		t.Errorf("parent = %q, want %q", res.Folders.Parent, resolvedRoot)
	}
	if len(res.Folders.Entries) != 1 || res.Folders.Entries[0].Path != resolvedNested {
		t.Errorf("entries = %+v, want nested", res.Folders.Entries)
	}
}

func TestFoldersList_Refusals(t *testing.T) {
	h := newTestHarness(t)
	base := t.TempDir()
	root, resolvedRoot := newFolder(t, base, "work")
	_, outside := newFolder(t, base, "outside")
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	h.engine.SessionRoots = []string{root}

	// A symlink inside the root that points outside must not be followed.
	escape := filepath.Join(root, "escape")
	if err := os.Symlink(outside, escape); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	cases := []struct {
		name string
		path string
		code string
	}{
		{"outside the roots", outside, string(model.ErrNotAllowed)},
		{"symlink escape", escape, string(model.ErrNotAllowed)},
		{"not a directory", filepath.Join(resolvedRoot, "notes.txt"), string(model.ErrInvalidRequest)},
		{"missing folder", filepath.Join(resolvedRoot, "nope"), string(model.ErrNotAllowed)},
		{"path too long", "/" + strings.Repeat("a", maxSessionPath), string(model.ErrInvalidRequest)},
	}
	for i, tc := range cases {
		res := runHostCmd(t, h, model.CommandMsg{
			RequestID: "req-folders-bad-" + tc.name,
			Action:    model.ActionFoldersList,
			Path:      tc.path,
		})
		if res.OK || res.ErrorCode != tc.code {
			t.Errorf("case %d (%s): ok=%v code=%s, want %s", i, tc.name, res.OK, res.ErrorCode, tc.code)
		}
	}

	// No roots configured: the feature is off.
	h.engine.SessionRoots = nil
	res := runHostCmd(t, h, model.CommandMsg{
		RequestID: "req-folders-off",
		Action:    model.ActionFoldersList,
		Path:      resolvedRoot,
	})
	if res.OK || res.ErrorCode != string(model.ErrNotAllowed) {
		t.Errorf("unconfigured host: ok=%v code=%s, want not_allowed", res.OK, res.ErrorCode)
	}
}

func TestSessionCreate_NewWorkspace(t *testing.T) {
	h := newTestHarness(t)
	base := t.TempDir()
	root, _ := newFolder(t, base, "work")
	_, resolvedProject := newFolder(t, root, "project")
	h.engine.SessionRoots = []string{root}
	h.engine.OnHerdrOnline(true, herdr.Pong{Version: "0.1.14"})

	before := len(h.server.Calls())
	res := runHostCmd(t, h, model.CommandMsg{
		RequestID: "req-create-new",
		Action:    model.ActionSessionCreate,
		Kind:      "claude",
		Cwd:       resolvedProject,
	})
	if !res.OK {
		t.Fatalf("session.create failed: code=%s msg=%s", res.ErrorCode, res.Message)
	}
	if res.Session == nil {
		t.Fatal("session.create returned no session payload")
	}
	s := res.Session
	if s.Kind != "claude" {
		t.Errorf("kind = %q, want claude", s.Kind)
	}
	if s.Label != "project" {
		t.Errorf("label = %q, want the folder name", s.Label)
	}
	if s.PaneID == "" {
		t.Errorf("incomplete session: %+v", s)
	}

	calls := h.server.Calls()
	started := callsFrom(calls, before, "terminal.run")
	if len(started) != 1 {
		t.Fatalf("terminal.run calls = %d, want 1", len(started))
	}
	argv, _ := started[0].Params["argv"].([]any)
	if len(argv) != 1 || argv[0] != "claude" {
		t.Errorf("terminal.run argv = %v, want [claude]", argv)
	}
	if got, _ := started[0].Params["cwd"].(string); got != resolvedProject {
		t.Errorf("terminal.run cwd = %q, want %q", got, resolvedProject)
	}
	if got, _ := started[0].Params["name"].(string); got != "project" {
		t.Errorf("terminal.run name = %q, want project", got)
	}
	if keep, _ := started[0].Params["keep"].(bool); !keep {
		t.Errorf("terminal.run keep = %v, want true", started[0].Params["keep"])
	}
	if noParent, _ := started[0].Params["no_parent"].(bool); !noParent {
		t.Errorf("terminal.run no_parent = %v, want true", started[0].Params["no_parent"])
	}
	if noNotify, _ := started[0].Params["no_notify"].(bool); !noNotify {
		t.Errorf("terminal.run no_notify = %v, want true", started[0].Params["no_notify"])
	}
}

func TestSessionCreate_ReusesWorkspaceAndSuffixesName(t *testing.T) {
	h := newTestHarness(t)
	base := t.TempDir()
	root, _ := newFolder(t, base, "work")
	_, resolvedProject := newFolder(t, root, "project")
	h.engine.SessionRoots = []string{root}
	h.engine.OnHerdrOnline(true, herdr.Pong{Version: "0.1.14"})

	h.server.SetAgents([]map[string]any{
		{
			"pane_id": "w1:p1", "workspace_id": "w1", "workspace_label": "project",
			"tab_id": "t1", "cwd": resolvedProject,
			"agent": "claude", "agent_status": "idle", "state_change_seq": 1,
			"name": "project",
		},
	})

	before := len(h.server.Calls())
	res := runHostCmd(t, h, model.CommandMsg{
		RequestID: "req-create-reuse",
		Action:    model.ActionSessionCreate,
		Kind:      "opencode",
		Cwd:       resolvedProject,
	})
	if !res.OK {
		t.Fatalf("session.create failed: code=%s msg=%s", res.ErrorCode, res.Message)
	}
	if res.Session == nil {
		t.Fatal("session.create returned no session payload")
	}
	if res.Session.Label != "project 2" {
		t.Errorf("label = %q, want %q", res.Session.Label, "project 2")
	}
	if res.Session.WorkspaceID != "w1" {
		t.Errorf("workspace = %q, want the folder's existing workspace w1", res.Session.WorkspaceID)
	}

	calls := h.server.Calls()
	started := callsFrom(calls, before, "terminal.run")
	if len(started) != 1 {
		t.Fatalf("terminal.run calls = %d, want 1", len(started))
	}
	if got, _ := started[0].Params["name"].(string); got != "project 2" {
		t.Errorf("terminal.run name = %q, want %q", got, "project 2")
	}
	labels, _ := started[0].Params["labels"].(map[string]any)
	if got, _ := labels["herdr.space"].(string); got != "w1" {
		t.Errorf("terminal.run labels[herdr.space] = %q, want w1", got)
	}
	if got, _ := labels["where"].(string); got != "project / project 2" {
		t.Errorf("terminal.run labels[where] = %q, want %q", got, "project / project 2")
	}
}

func TestSessionCreate_LabelAndKindOverrides(t *testing.T) {
	h := newTestHarness(t)
	base := t.TempDir()
	root, _ := newFolder(t, base, "work")
	_, resolvedProject := newFolder(t, root, "project")
	h.engine.SessionRoots = []string{root}
	h.engine.SessionKinds = []string{"agy"}
	h.engine.OnHerdrOnline(true, herdr.Pong{Version: "0.9.1", Protocol: 22})

	res := runHostCmd(t, h, model.CommandMsg{
		RequestID: "req-create-label",
		Action:    model.ActionSessionCreate,
		Kind:      "agy",
		Cwd:       resolvedProject,
		Label:     "  review docs  ",
	})
	if !res.OK || res.Session == nil {
		t.Fatalf("session.create failed: ok=%v code=%s msg=%s", res.OK, res.ErrorCode, res.Message)
	}
	if res.Session.Label != "review docs" {
		t.Errorf("label = %q, want the trimmed request label", res.Session.Label)
	}
	if res.Session.Kind != "agy" {
		t.Errorf("kind = %q, want agy", res.Session.Kind)
	}
}

func TestSessionCreate_RefusalsNeverTouchHerdr(t *testing.T) {
	h := newTestHarness(t)
	base := t.TempDir()
	root, _ := newFolder(t, base, "work")
	_, resolvedProject := newFolder(t, root, "project")
	_, outside := newFolder(t, base, "outside")
	h.engine.SessionRoots = []string{root}
	h.engine.SessionKinds = []string{"claude"}
	h.engine.OnHerdrOnline(true, herdr.Pong{Version: "0.9.1", Protocol: 22})

	longLabel := strings.Repeat("x", maxSessionLabel+1)
	cases := []struct {
		name  string
		cmd   model.CommandMsg
		code  string
		setup func()
	}{
		{
			name: "kind not allowed",
			cmd:  model.CommandMsg{Kind: "opencode", Cwd: resolvedProject},
			code: string(model.ErrNotAllowed),
		},
		{
			name: "folder outside the roots",
			cmd:  model.CommandMsg{Kind: "claude", Cwd: outside},
			code: string(model.ErrNotAllowed),
		},
		{
			name: "empty cwd",
			cmd:  model.CommandMsg{Kind: "claude"},
			code: string(model.ErrNotAllowed),
		},
		{
			name: "label with a newline",
			cmd:  model.CommandMsg{Kind: "claude", Cwd: resolvedProject, Label: "a\nb"},
			code: string(model.ErrInvalidRequest),
		},
		{
			name: "label too long",
			cmd:  model.CommandMsg{Kind: "claude", Cwd: resolvedProject, Label: longLabel},
			code: string(model.ErrInvalidRequest),
		},
		{
			name: "cwd too long",
			cmd:  model.CommandMsg{Kind: "claude", Cwd: resolvedProject + strings.Repeat("/x", maxSessionPath)},
			code: string(model.ErrInvalidRequest),
		},
		{
			name: "no roots configured",
			cmd:  model.CommandMsg{Kind: "claude", Cwd: resolvedProject},
			code: string(model.ErrNotAllowed),
			setup: func() {
				h.engine.SessionRoots = nil
			},
		},
	}

	for i, tc := range cases {
		if tc.setup != nil {
			tc.setup()
		}
		before := len(h.server.Calls())
		cmd := tc.cmd
		cmd.RequestID = "req-create-refused-" + string(rune('a'+i))
		cmd.Action = model.ActionSessionCreate
		res := runHostCmd(t, h, cmd)
		if res.OK || res.ErrorCode != tc.code {
			t.Errorf("%s: ok=%v code=%s, want %s", tc.name, res.OK, res.ErrorCode, tc.code)
		}
		for _, method := range []string{"terminal.list", "terminal.run"} {
			if got := callsFrom(h.server.Calls(), before, method); len(got) != 0 {
				t.Errorf("%s: herdr %s was called %d times on a refused request", tc.name, method, len(got))
			}
		}
	}
}

func TestSessionCreate_HerdrOffline(t *testing.T) {
	h := newTestHarness(t)
	base := t.TempDir()
	root, _ := newFolder(t, base, "work")
	_, resolvedProject := newFolder(t, root, "project")
	h.engine.SessionRoots = []string{root}
	h.engine.OnHerdrOnline(false, herdr.Pong{})

	before := len(h.server.Calls())
	res := runHostCmd(t, h, model.CommandMsg{
		RequestID: "req-create-offline",
		Action:    model.ActionSessionCreate,
		Kind:      "claude",
		Cwd:       resolvedProject,
	})
	if res.OK || res.ErrorCode != string(model.ErrHerdrOffline) {
		t.Fatalf("ok=%v code=%s, want herdr_offline", res.OK, res.ErrorCode)
	}
	if got := callsFrom(h.server.Calls(), before, "terminal.run"); len(got) != 0 {
		t.Errorf("terminal.run called while herdr was offline")
	}
}

func TestSessionCreate_DuplicateRequestRunsOnce(t *testing.T) {
	h := newTestHarness(t)
	base := t.TempDir()
	root, _ := newFolder(t, base, "work")
	_, resolvedProject := newFolder(t, root, "project")
	h.engine.SessionRoots = []string{root}
	h.engine.OnHerdrOnline(true, herdr.Pong{Version: "0.1.14"})

	before := len(h.server.Calls())
	cmd := model.CommandMsg{
		RequestID: "req-create-dup",
		Action:    model.ActionSessionCreate,
		Kind:      "claude",
		Cwd:       resolvedProject,
	}
	if res := runHostCmd(t, h, cmd); !res.OK {
		t.Fatalf("first create failed: code=%s msg=%s", res.ErrorCode, res.Message)
	}
	second := runHostCmd(t, h, cmd)
	if second.OK || second.ErrorCode != string(model.ErrStaleState) {
		t.Errorf("replay: ok=%v code=%s, want stale_state", second.OK, second.ErrorCode)
	}
	if got := callsFrom(h.server.Calls(), before, "terminal.run"); len(got) != 1 {
		t.Errorf("terminal.run calls = %d, want 1 for a replayed request", len(got))
	}
}

func TestSessionCreate_AgentStartFailureIsInternal(t *testing.T) {
	h := newTestHarness(t)
	base := t.TempDir()
	root, _ := newFolder(t, base, "work")
	_, resolvedProject := newFolder(t, root, "project")
	h.engine.SessionRoots = []string{root}
	h.engine.OnHerdrOnline(true, herdr.Pong{Version: "0.1.14"})
	h.server.FailNext("terminal.run", "invalid_params", "kind not installed")

	res := runHostCmd(t, h, model.CommandMsg{
		RequestID: "req-create-start-fails",
		Action:    model.ActionSessionCreate,
		Kind:      "claude",
		Cwd:       resolvedProject,
	})
	if res.OK || res.ErrorCode != string(model.ErrInternal) {
		t.Fatalf("ok=%v code=%s, want internal", res.OK, res.ErrorCode)
	}
	if !strings.Contains(res.Message, "kind not installed") {
		t.Errorf("message = %q, want herdr's reason", res.Message)
	}
}

func TestSessionCreate_Timeout(t *testing.T) {
	h := newTestHarness(t)
	base := t.TempDir()
	root, _ := newFolder(t, base, "work")
	_, resolvedProject := newFolder(t, root, "project")
	h.engine.SessionRoots = []string{root}
	h.engine.SessionTimeout = 200 * time.Millisecond
	h.engine.OnHerdrOnline(true, herdr.Pong{Version: "0.1.14"})

	hold := h.server.HoldNext("terminal.list")
	t.Cleanup(hold.Release)

	res := runHostCmd(t, h, model.CommandMsg{
		RequestID: "req-create-timeout",
		Action:    model.ActionSessionCreate,
		Kind:      "claude",
		Cwd:       resolvedProject,
	})
	if res.OK || res.ErrorCode != string(model.ErrTimeout) {
		t.Fatalf("ok=%v code=%s, want timeout", res.OK, res.ErrorCode)
	}
}

func TestCommandBudget_HostActions(t *testing.T) {
	e := NewEngine(nil, nil, nil, nil, "test", "host", "", nil)

	if got := e.commandBudget(model.ActionSessionCreate); got != defaultSessionTimeout {
		t.Errorf("session.create budget = %v, want %v", got, defaultSessionTimeout)
	}
	if got := e.commandBudget(model.ActionSessionOptions); got != defaultFolderTimeout {
		t.Errorf("session.options budget = %v, want %v", got, defaultFolderTimeout)
	}
	if got := e.commandBudget(model.ActionFoldersList); got != defaultFolderTimeout {
		t.Errorf("folders.list budget = %v, want %v", got, defaultFolderTimeout)
	}
	if got := e.commandBudget(model.ActionPrompt); got != commandTimeout {
		t.Errorf("prompt budget = %v, want %v", got, commandTimeout)
	}

	e.SessionTimeout = 3 * time.Second
	e.FolderTimeout = time.Second
	if got := e.commandBudget(model.ActionSessionCreate); got != 3*time.Second {
		t.Errorf("configured session budget = %v, want 3s", got)
	}
	if got := e.commandBudget(model.ActionFoldersList); got != time.Second {
		t.Errorf("configured folder budget = %v, want 1s", got)
	}
}

func TestUniqueName(t *testing.T) {
	taken := map[string]bool{"proj": true, "proj 2": true}
	if got := uniqueName("proj", taken); got != "proj 3" {
		t.Errorf("uniqueName = %q, want %q", got, "proj 3")
	}
	if got := uniqueName("other", taken); got != "other" {
		t.Errorf("uniqueName = %q, want other", got)
	}
}

func TestWorkspaceForFolder(t *testing.T) {
	dir := t.TempDir()
	other := t.TempDir()
	list := []herdr.AgentInfo{
		{PaneID: "w1:p1", WorkspaceID: "w1", WorkspaceLabel: "Work", CWD: &other},
		{PaneID: "w2:p1", WorkspaceID: "w2", WorkspaceLabel: "Empty"},             // no cwd
		{PaneID: "w3:p1", WorkspaceID: "w3", WorkspaceLabel: "Target", CWD: &dir}, // a match
	}
	wsID, wsLabel := workspaceForFolder(dir, list)
	if wsID != "w3" || wsLabel != "Target" {
		t.Errorf("workspaceForFolder = (%q, %q), want (w3, Target)", wsID, wsLabel)
	}
	if id, label := workspaceForFolder(filepath.Join(dir, "sub"), list); id != "" || label != "" {
		t.Errorf("workspaceForFolder for an unknown folder = (%q, %q), want empty", id, label)
	}
}
