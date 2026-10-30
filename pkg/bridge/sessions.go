package bridge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gabrielmarcano/agent-monitor/pkg/herdr"
	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

const (
	// defaultSessionTimeout bounds one session.create from its arrival. It is
	// longer than agent.start's 25 s startup wait plus the list/create calls,
	// and still below the relay's 40 s and the phone's 45 s.
	defaultSessionTimeout = 35 * time.Second
	// defaultFolderTimeout bounds session.options and folders.list, pure local
	// reads that must fit the relay's 7 s command budget.
	defaultFolderTimeout = 5 * time.Second
	// agentStartTimeoutMS is how long agent.start waits for the agent to
	// become interactive. herdr's own default is 30 s; 25 s leaves room for
	// the calls around it inside defaultSessionTimeout.
	agentStartTimeoutMS = 25000
	// maxSessionPath bounds a cwd or a folder path, in bytes.
	maxSessionPath = 4096
	// maxSessionLabel bounds a session name, in characters.
	maxSessionLabel = 64
	// maxFolderEntries bounds one folder listing. The phone shows a list, not
	// a filesystem.
	maxFolderEntries = 500
)

// DefaultSessionKinds are the agent kinds a bridge may start when its config
// does not name any (contracts.md §6).
var DefaultSessionKinds = []string{"claude", "agy", "opencode"}

// effectiveKinds returns the configured kinds, or the defaults.
func (e *Engine) effectiveKinds() []string {
	kinds := make([]string, 0, len(e.SessionKinds))
	seen := make(map[string]bool, len(e.SessionKinds))
	for _, k := range e.SessionKinds {
		k = strings.TrimSpace(k)
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		kinds = append(kinds, k)
	}
	if len(kinds) == 0 {
		return append([]string(nil), DefaultSessionKinds...)
	}
	return kinds
}

// resolvedRoots returns the configured session roots that exist as
// directories, with symlinks resolved, in configuration order. An empty list
// means session creation is off on this host.
func (e *Engine) resolvedRoots() []string {
	roots := make([]string, 0, len(e.SessionRoots))
	seen := make(map[string]bool, len(e.SessionRoots))
	for _, root := range e.SessionRoots {
		root = strings.TrimSpace(root)
		if root == "" || !filepath.IsAbs(root) {
			continue
		}
		resolved, err := filepath.EvalSymlinks(filepath.Clean(root))
		if err != nil || seen[resolved] {
			continue
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.IsDir() {
			continue
		}
		seen[resolved] = true
		roots = append(roots, resolved)
	}
	return roots
}

// insideRoots reports whether path is root itself or below one of roots,
// after resolving symlinks. It returns the resolved path.
func insideRoots(path string, roots []string) (string, bool) {
	if path == "" || !filepath.IsAbs(path) {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", false
	}
	for _, root := range roots {
		rel, err := filepath.Rel(root, resolved)
		if err != nil {
			continue
		}
		if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return resolved, true
		}
	}
	return "", false
}

// replyHost answers a host-level command, with an optional payload.
func (e *Engine) replyHost(cmd model.CommandMsg, ok bool, code, msg string, fill func(*model.CommandResultMsg)) {
	if e.Logger != nil {
		e.Logger.Info("bridge host request",
			"action", cmd.Action,
			"ok", ok,
			"code", code,
			"kind", cmd.Kind,
			"error", msg,
		)
	}
	res := model.CommandResultMsg{
		Type:      model.WireCommandResult,
		RequestID: cmd.RequestID,
		OK:        ok,
		ErrorCode: code,
		Message:   msg,
	}
	if fill != nil {
		fill(&res)
	}
	if e.Relay != nil {
		e.Relay.Send(res)
	}
}

// executeHostCommand handles the actions that are not tied to a pane.
func (e *Engine) executeHostCommand(ctx context.Context, cmd model.CommandMsg) {
	switch cmd.Action {
	case model.ActionSessionOptions:
		if e.guard.ObserveRequest(cmd.RequestID) {
			e.replyHost(cmd, false, string(model.ErrStaleState), "duplicate request_id", nil)
			return
		}
		e.executeSessionOptions(cmd)
	case model.ActionFoldersList:
		if e.guard.ObserveRequest(cmd.RequestID) {
			e.replyHost(cmd, false, string(model.ErrStaleState), "duplicate request_id", nil)
			return
		}
		e.executeFoldersList(cmd)
	case model.ActionSessionCreate:
		e.executeSessionCreate(ctx, cmd)
	default:
		e.replyHost(cmd, false, string(model.ErrInvalidRequest), fmt.Sprintf("unsupported host action: %q", cmd.Action), nil)
	}
}

// executeSessionOptions reports what this host allows for new sessions. It is
// pure configuration: no herdr call.
func (e *Engine) executeSessionOptions(cmd model.CommandMsg) {
	roots := e.resolvedRoots()
	if roots == nil {
		roots = []string{}
	}
	e.replyHost(cmd, true, "", "", func(res *model.CommandResultMsg) {
		res.Options = &model.SessionOptionsResponse{
			Kinds: e.effectiveKinds(),
			Roots: roots,
		}
	})
}

// executeFoldersList lists one folder (or the roots) for the phone's picker.
func (e *Engine) executeFoldersList(cmd model.CommandMsg) {
	roots := e.resolvedRoots()
	if len(roots) == 0 {
		e.replyHost(cmd, false, string(model.ErrNotAllowed), "session creation is not configured on this host", nil)
		return
	}

	path := strings.TrimSpace(cmd.Path)
	if path == "" {
		entries := make([]model.FolderEntry, 0, len(roots))
		for _, root := range roots {
			name := filepath.Base(root)
			if name == "/" || name == "." {
				name = root
			}
			entries = append(entries, model.FolderEntry{Name: name, Path: root})
		}
		e.replyHost(cmd, true, "", "", func(res *model.CommandResultMsg) {
			res.Folders = &model.FolderListResponse{
				Path:    "",
				Roots:   roots,
				Entries: entries,
			}
		})
		return
	}
	if len(path) > maxSessionPath {
		e.replyHost(cmd, false, string(model.ErrInvalidRequest), "path is too long", nil)
		return
	}

	dir, ok := insideRoots(path, roots)
	if !ok {
		e.replyHost(cmd, false, string(model.ErrNotAllowed), "folder is outside the configured session roots", nil)
		return
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		e.replyHost(cmd, false, string(model.ErrInvalidRequest), "not a directory", nil)
		return
	}
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		e.replyHost(cmd, false, string(model.ErrInternal), fmt.Sprintf("read folder: %v", err), nil)
		return
	}

	entries := make([]model.FolderEntry, 0, len(dirEntries))
	truncated := false
	for _, d := range dirEntries {
		if !d.IsDir() {
			continue
		}
		if len(entries) == maxFolderEntries {
			truncated = true
			break
		}
		entries = append(entries, model.FolderEntry{
			Name: d.Name(),
			Path: filepath.Join(dir, d.Name()),
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

	parent := ""
	if candidate := filepath.Dir(dir); candidate != dir {
		if resolved, ok := insideRoots(candidate, roots); ok && resolved != dir {
			parent = resolved
		}
	}

	e.replyHost(cmd, true, "", "", func(res *model.CommandResultMsg) {
		res.Folders = &model.FolderListResponse{
			Path:      dir,
			Parent:    parent,
			Roots:     roots,
			Entries:   entries,
			Truncated: truncated,
		}
	})
}

// executeSessionCreate starts one agent in cwd, in the folder's workspace and
// a tab of its own.
func (e *Engine) executeSessionCreate(ctx context.Context, cmd model.CommandMsg) {
	roots := e.resolvedRoots()
	if len(roots) == 0 {
		e.replyHost(cmd, false, string(model.ErrNotAllowed), "session creation is not configured on this host", nil)
		return
	}

	kind := strings.TrimSpace(cmd.Kind)
	if !containsString(e.effectiveKinds(), kind) {
		e.replyHost(cmd, false, string(model.ErrNotAllowed), fmt.Sprintf("agent kind %q is not allowed on this host", kind), nil)
		return
	}
	if len(cmd.Cwd) > maxSessionPath {
		e.replyHost(cmd, false, string(model.ErrInvalidRequest), "cwd is too long", nil)
		return
	}
	cwd, ok := insideRoots(strings.TrimSpace(cmd.Cwd), roots)
	if !ok {
		e.replyHost(cmd, false, string(model.ErrNotAllowed), "folder is outside the configured session roots", nil)
		return
	}
	info, err := os.Stat(cwd)
	if err != nil || !info.IsDir() {
		e.replyHost(cmd, false, string(model.ErrInvalidRequest), "cwd is not a directory", nil)
		return
	}

	base := strings.TrimSpace(cmd.Label)
	if base == "" {
		base = filepath.Base(cwd)
	}
	if base == "" || base == "." || base == string(filepath.Separator) {
		base = "session"
	}
	if utf8.RuneCountInString(base) > maxSessionLabel {
		e.replyHost(cmd, false, string(model.ErrInvalidRequest), fmt.Sprintf("label must be at most %d characters", maxSessionLabel), nil)
		return
	}
	if strings.ContainsAny(base, "\r\n\t") {
		e.replyHost(cmd, false, string(model.ErrInvalidRequest), "label must be one line", nil)
		return
	}

	if !e.isHerdrOnline() || e.Herdr == nil {
		e.replyHost(cmd, false, string(model.ErrHerdrOffline), "herdr is offline", nil)
		return
	}

	unlock, duplicate, err := e.guard.AcquireSession(ctx, cmd.RequestID)
	if err != nil {
		e.replyHost(cmd, false, string(model.ErrTimeout), "waited too long behind another session", nil)
		return
	}
	if duplicate {
		e.replyHost(cmd, false, string(model.ErrStaleState), "duplicate request_id", nil)
		return
	}
	defer unlock()
	if !e.isHerdrOnline() {
		e.replyHost(cmd, false, string(model.ErrHerdrOffline), "herdr is offline", nil)
		return
	}

	workspaceID, wsLabel, name, err := e.planSession(ctx, cwd, base)
	if err != nil {
		code, msg := sessionError(ctx, err)
		e.replyHost(cmd, false, code, msg, nil)
		return
	}

	startCtx, cancel := context.WithTimeout(ctx, time.Duration(agentStartTimeoutMS+2000)*time.Millisecond)
	started, err := e.Herdr.RunTerminal(startCtx, herdr.RunParams{
		Argv:     []string{kind},
		CWD:      cwd,
		Name:     name,
		Keep:     true,
		NoParent: true,
		NoNotify: true,
		Labels: map[string]string{
			"herdr.space": workspaceID,
			"where":       wsLabel + " / " + name,
		},
	})
	cancel()
	if err != nil {
		code, msg := sessionError(ctx, err)
		e.replyHost(cmd, false, code, "start agent: "+msg, nil)
		return
	}
	paneID := started.Address
	if started.Labels != nil && started.Labels["herdr.space"] != "" {
		workspaceID = started.Labels["herdr.space"]
	}
	tabID := ""
	if started.Labels != nil {
		tabID = started.Labels["herdr.tab"]
	}

	// The phone opens the chat right away; let the syncer publish the pane.
	e.refreshSoon()
	e.replyHost(cmd, true, "", "", func(res *model.CommandResultMsg) {
		res.Session = &model.SessionCreated{
			PaneID:      paneID,
			WorkspaceID: workspaceID,
			TabID:       tabID,
			Kind:        kind,
			Label:       name,
		}
	})
}

// planSession finds an existing space serving cwd (or allocates a new space ID
// that Herdr.app will adopt into a new space) and returns the space ID, space
// label, and unique session name.
func (e *Engine) planSession(ctx context.Context, cwd, base string) (workspaceID, wsLabel, name string, err error) {
	callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	agentsList, err := e.Herdr.ListAgents(callCtx)
	cancel()
	if err != nil {
		return "", "", "", err
	}

	workspaceID, wsLabel = workspaceForFolder(cwd, agentsList)
	if workspaceID == "" {
		workspaceID = newSpaceID()
		wsLabel = uniqueName(filepath.Base(cwd), workspaceLabelsFromAgents(agentsList))
		return workspaceID, wsLabel, uniqueName(base, map[string]bool{}), nil
	}
	if wsLabel == "" {
		wsLabel = filepath.Base(cwd)
	}
	name = uniqueName(base, agentNamesIn(agentsList, workspaceID))
	return workspaceID, wsLabel, name, nil
}

func newSpaceID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("sp%x", time.Now().UnixNano())
	}
	return "sp" + hex.EncodeToString(b[:])
}

// workspaceForFolder returns the workspace ID and label of an agent whose cwd matches path.
func workspaceForFolder(path string, agents []herdr.AgentInfo) (string, string) {
	want := filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(want); err == nil {
		want = resolved
	}
	for _, a := range agents {
		if a.CWD == nil || *a.CWD == "" || a.WorkspaceID == "" {
			continue
		}
		got := filepath.Clean(*a.CWD)
		if resolved, err := filepath.EvalSymlinks(got); err == nil {
			got = resolved
		}
		if got == want {
			return a.WorkspaceID, a.WorkspaceLabel
		}
	}
	return "", ""
}

func workspaceLabelsFromAgents(agents []herdr.AgentInfo) map[string]bool {
	out := make(map[string]bool, len(agents))
	for _, a := range agents {
		if a.WorkspaceLabel != "" {
			out[a.WorkspaceLabel] = true
		}
	}
	return out
}

// agentNamesIn is the set of agent names already used in workspaceID.
func agentNamesIn(agents []herdr.AgentInfo, workspaceID string) map[string]bool {
	out := make(map[string]bool, len(agents))
	for _, a := range agents {
		if a.WorkspaceID == workspaceID && a.Name != nil && *a.Name != "" {
			out[*a.Name] = true
		}
	}
	return out
}

// uniqueName returns base, or base with the first free " N" suffix.
func uniqueName(base string, taken map[string]bool) string {
	if !taken[base] {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s %d", base, i)
		if !taken[candidate] {
			return candidate
		}
	}
}

func containsString(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// sessionError maps a failed herdr call to a contract code and message.
func sessionError(ctx context.Context, err error) (string, string) {
	switch {
	case errors.Is(err, herdr.ErrUnavailable):
		return string(model.ErrHerdrOffline), "herdr is offline"
	case ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded):
		return string(model.ErrTimeout), "the host ran out of time"
	default:
		return string(model.ErrInternal), err.Error()
	}
}
