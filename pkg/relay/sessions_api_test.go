package relay

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

// hostResponder connects a fake host whose bridge answers every command with
// reply(cmd), and returns the commands it received.
func hostResponder(t *testing.T, ctx context.Context, server *Server, ts *httptest.Server, reply func(model.CommandMsg) model.CommandResultMsg) <-chan model.CommandMsg {
	t.Helper()
	conn := connectTestHost(t, ctx, server, ts, "w1:p1")
	out := make(chan model.CommandMsg, 16)
	go func() {
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			msg, err := model.DecodeWire(data)
			if err != nil {
				continue
			}
			cmd, ok := msg.(model.CommandMsg)
			if !ok {
				continue
			}
			out <- cmd
			res := reply(cmd)
			res.Type = model.WireCommandResult
			res.RequestID = cmd.RequestID
			data, _ = json.Marshal(res)
			_ = conn.Write(ctx, websocket.MessageText, data)
		}
	}()
	return out
}

// nextCommand waits for the next command the fake host received.
func nextCommand(t *testing.T, cmds <-chan model.CommandMsg) model.CommandMsg {
	t.Helper()
	select {
	case cmd := <-cmds:
		return cmd
	case <-time.After(2 * time.Second):
		t.Fatal("the host never received the command")
		return model.CommandMsg{}
	}
}

func TestAPI_SessionOptions(t *testing.T) {
	server, ts := setupTestServer(t)
	devToken, _ := GenerateDeviceToken()
	_, _ = server.Store().AddDevice("Phone", Sha256Hex(devToken))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	want := model.SessionOptionsResponse{
		Kinds: []string{"claude", "opencode"},
		Roots: []string{"/Users/me/Code"},
	}
	cmds := hostResponder(t, ctx, server, ts, func(model.CommandMsg) model.CommandResultMsg {
		return model.CommandResultMsg{OK: true, Options: &want}
	})

	req, _ := http.NewRequest("GET", ts.URL+"/v1/hosts/main/sessions/options", nil)
	req.Header.Set("Authorization", "Bearer "+devToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET sessions/options: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got model.SessionOptionsResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Kinds) != 2 || got.Kinds[0] != "claude" || len(got.Roots) != 1 || got.Roots[0] != "/Users/me/Code" {
		t.Errorf("options = %+v, want %+v", got, want)
	}

	cmd := nextCommand(t, cmds)
	if cmd.Action != model.ActionSessionOptions {
		t.Errorf("action = %q, want %q", cmd.Action, model.ActionSessionOptions)
	}
	if cmd.PaneID != "" {
		t.Errorf("pane_id = %q, want empty for a host-level action", cmd.PaneID)
	}
}

func TestAPI_SessionOptionsErrors(t *testing.T) {
	server, ts := setupTestServer(t)
	devToken, _ := GenerateDeviceToken()
	_, _ = server.Store().AddDevice("Phone", Sha256Hex(devToken))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var reply model.CommandResultMsg
	hostResponder(t, ctx, server, ts, func(model.CommandMsg) model.CommandResultMsg { return reply })

	get := func(path string) (int, model.ErrorResponse) {
		req, _ := http.NewRequest("GET", ts.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+devToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		var errResp model.ErrorResponse
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		return resp.StatusCode, errResp
	}

	// A bridge that refuses the action (session creation not configured).
	reply = model.CommandResultMsg{OK: false, ErrorCode: string(model.ErrNotAllowed), Message: "session creation is not configured on this host"}
	if status, errResp := get("/v1/hosts/main/sessions/options"); status != http.StatusForbidden || errResp.Error.Code != string(model.ErrNotAllowed) {
		t.Errorf("not_allowed: status %d code %q, want 403 not_allowed", status, errResp.Error.Code)
	}

	// A bridge that claims success but sends no payload.
	reply = model.CommandResultMsg{OK: true}
	if status, errResp := get("/v1/hosts/main/sessions/options"); status != http.StatusInternalServerError || errResp.Error.Code != string(model.ErrInternal) {
		t.Errorf("no payload: status %d code %q, want 500 internal", status, errResp.Error.Code)
	}

	// An unknown host is never online.
	if status, _ := get("/v1/hosts/nohost/sessions/options"); status != http.StatusServiceUnavailable {
		t.Errorf("unknown host: status = %d, want 503", status)
	}

	// herdr offline on the host itself.
	server.State().SetHost("main", true, false)
	if status, errResp := get("/v1/hosts/main/sessions/options"); status != http.StatusServiceUnavailable || errResp.Error.Code != string(model.ErrHerdrOffline) {
		t.Errorf("herdr offline: status %d code %q, want 503 herdr_offline", status, errResp.Error.Code)
	}
}

func TestAPI_Folders(t *testing.T) {
	server, ts := setupTestServer(t)
	devToken, _ := GenerateDeviceToken()
	_, _ = server.Store().AddDevice("Phone", Sha256Hex(devToken))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	want := model.FolderListResponse{
		Path:   "/Users/me/Code/agent-watch",
		Parent: "/Users/me/Code",
		Roots:  []string{"/Users/me/Code"},
		Entries: []model.FolderEntry{
			{Name: "docs", Path: "/Users/me/Code/agent-watch/docs"},
		},
	}
	cmds := hostResponder(t, ctx, server, ts, func(model.CommandMsg) model.CommandResultMsg {
		return model.CommandResultMsg{OK: true, Folders: &want}
	})

	req, _ := http.NewRequest("GET", ts.URL+"/v1/hosts/main/folders?path=%2FUsers%2Fme%2FCode%2Fagent-watch", nil)
	req.Header.Set("Authorization", "Bearer "+devToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET folders: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got model.FolderListResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Path != want.Path || got.Parent != want.Parent || len(got.Entries) != 1 || got.Entries[0] != want.Entries[0] {
		t.Errorf("listing = %+v, want %+v", got, want)
	}

	cmd := nextCommand(t, cmds)
	if cmd.Action != model.ActionFoldersList {
		t.Errorf("action = %q, want %q", cmd.Action, model.ActionFoldersList)
	}
	if cmd.Path != want.Path {
		t.Errorf("path = %q, want %q", cmd.Path, want.Path)
	}

	// An over-long path is rejected before any host call.
	long := "/" + strings.Repeat("a", maxSessionPath)
	req, _ = http.NewRequest("GET", ts.URL+"/v1/hosts/main/folders?path="+long, nil)
	req.Header.Set("Authorization", "Bearer "+devToken)
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET long path: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Errorf("long path: status = %d, want 400", resp2.StatusCode)
	}
	select {
	case cmd := <-cmds:
		t.Errorf("host received %+v for an over-long path", cmd)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestAPI_CreateSession(t *testing.T) {
	server, ts := setupTestServer(t)
	devToken, _ := GenerateDeviceToken()
	_, _ = server.Store().AddDevice("Phone", Sha256Hex(devToken))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	want := model.SessionCreated{
		PaneID:      "w2:p1",
		WorkspaceID: "w2",
		TabID:       "t1",
		Kind:        "claude",
		Label:       "agent-watch",
	}
	cmds := hostResponder(t, ctx, server, ts, func(model.CommandMsg) model.CommandResultMsg {
		return model.CommandResultMsg{OK: true, Session: &want}
	})

	body := `{"kind":"claude","cwd":"/Users/me/Code/agent-watch","label":"agent-watch"}`
	req, _ := http.NewRequest("POST", ts.URL+"/v1/hosts/main/sessions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+devToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST sessions: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got model.SessionCreated
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.PaneID != want.PaneID || got.WorkspaceID != want.WorkspaceID || got.TabID != want.TabID || got.Kind != "claude" || got.Label != "agent-watch" {
		t.Errorf("session = %+v, want %+v", got, want)
	}
	if got.Host != "main" {
		t.Errorf("host = %q, want the relay's host id", got.Host)
	}

	cmd := nextCommand(t, cmds)
	if cmd.Action != model.ActionSessionCreate {
		t.Errorf("action = %q, want %q", cmd.Action, model.ActionSessionCreate)
	}
	if cmd.Kind != "claude" || cmd.Cwd != "/Users/me/Code/agent-watch" || cmd.Label != "agent-watch" {
		t.Errorf("command = %+v, want the request's kind, cwd and label", cmd)
	}
}

func TestAPI_CreateSessionErrors(t *testing.T) {
	server, ts := setupTestServer(t)
	devToken, _ := GenerateDeviceToken()
	_, _ = server.Store().AddDevice("Phone", Sha256Hex(devToken))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var reply model.CommandResultMsg
	cmds := hostResponder(t, ctx, server, ts, func(model.CommandMsg) model.CommandResultMsg { return reply })

	post := func(body string) (int, model.ErrorResponse) {
		req, _ := http.NewRequest("POST", ts.URL+"/v1/hosts/main/sessions", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+devToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST sessions: %v", err)
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		var errResp model.ErrorResponse
		_ = json.Unmarshal(data, &errResp)
		return resp.StatusCode, errResp
	}

	cases := []struct {
		name   string
		body   string
		status int
		code   model.ErrorCode
	}{
		{"malformed body", `{"kind":`, http.StatusBadRequest, model.ErrInvalidRequest},
		{"missing kind", `{"cwd":"/tmp/x"}`, http.StatusBadRequest, model.ErrInvalidRequest},
		{"blank kind", `{"kind":"  ","cwd":"/tmp/x"}`, http.StatusBadRequest, model.ErrInvalidRequest},
		{"missing cwd", `{"kind":"claude"}`, http.StatusBadRequest, model.ErrInvalidRequest},
		{"over-long cwd", `{"kind":"claude","cwd":"/` + strings.Repeat("a", maxSessionPath) + `"}`, http.StatusBadRequest, model.ErrInvalidRequest},
	}
	for _, tc := range cases {
		status, errResp := post(tc.body)
		if status != tc.status || errResp.Error.Code != string(tc.code) {
			t.Errorf("%s: status %d code %q, want %d %s", tc.name, status, errResp.Error.Code, tc.status, tc.code)
		}
	}
	// None of the rejected requests reached the host.
	select {
	case cmd := <-cmds:
		t.Fatalf("host received %+v for a rejected request", cmd)
	case <-time.After(100 * time.Millisecond):
	}

	// A bridge refusal keeps its code: not_allowed is 403, not 500.
	reply = model.CommandResultMsg{OK: false, ErrorCode: string(model.ErrNotAllowed), Message: "folder is outside the configured session roots"}
	if status, errResp := post(`{"kind":"claude","cwd":"/etc"}`); status != http.StatusForbidden || errResp.Error.Code != string(model.ErrNotAllowed) {
		t.Errorf("not_allowed: status %d code %q, want 403 not_allowed", status, errResp.Error.Code)
	}
	<-cmds

	// A bridge that returns no session is an internal error.
	reply = model.CommandResultMsg{OK: true}
	if status, errResp := post(`{"kind":"claude","cwd":"/tmp"}`); status != http.StatusInternalServerError || errResp.Error.Code != string(model.ErrInternal) {
		t.Errorf("no session: status %d code %q, want 500 internal", status, errResp.Error.Code)
	}
}

// The session.create round trip gets the session budget, not the command one.
func TestAPI_CreateSessionTimesOutOnSessionBudget(t *testing.T) {
	server, ts := setupTestServer(t)
	server.Hub().SetCommandTimeout(50 * time.Millisecond)
	server.Hub().SetSessionTimeout(200 * time.Millisecond)
	devToken, _ := GenerateDeviceToken()
	_, _ = server.Store().AddDevice("Phone", Sha256Hex(devToken))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn := connectTestHost(t, ctx, server, ts, "w1:p1")
	received := readCommands(ctx, conn)

	start := time.Now()
	body := `{"kind":"claude","cwd":"/tmp"}`
	req, _ := http.NewRequest("POST", ts.URL+"/v1/hosts/main/sessions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+devToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST sessions: %v", err)
	}
	defer resp.Body.Close()

	var errResp model.ErrorResponse
	_ = json.NewDecoder(resp.Body).Decode(&errResp)
	if resp.StatusCode != http.StatusGatewayTimeout || errResp.Error.Code != string(model.ErrTimeout) {
		t.Fatalf("silent host: status %d code %q, want 504 timeout", resp.StatusCode, errResp.Error.Code)
	}
	if elapsed := time.Since(start); elapsed < 150*time.Millisecond {
		t.Errorf("timed out after %v, want the 200 ms session budget (not the 50 ms command budget)", elapsed)
	}
	select {
	case <-received:
	case <-ctx.Done():
		t.Fatal("the host never received the command")
	}
}
