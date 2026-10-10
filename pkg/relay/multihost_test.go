package relay

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gabrielmarcano/agent-monitor/pkg/model"
	"github.com/gabrielmarcano/agent-monitor/pkg/push"
)

// twoHostServer is a relay with two hosts: "main" (AW_HOST_TOKEN, named Mac)
// and "linux" (registered, named Linux box), and one paired watch.
type twoHostServer struct {
	server     *Server
	ts         *httptest.Server
	linuxToken string
	devToken   string
}

func newTwoHostServer(t *testing.T) *twoHostServer {
	t.Helper()
	server, ts := setupTestServerWith(t, func(c *Config) { c.HostName = "Mac" })
	linuxToken, _ := GenerateDeviceToken()
	if _, err := server.AddHost("linux", "Linux box", Sha256Hex(linuxToken)); err != nil {
		t.Fatalf("AddHost: %v", err)
	}
	devToken, _ := GenerateDeviceToken()
	if _, err := server.Store().AddDevice("Watch", Sha256Hex(devToken)); err != nil {
		t.Fatalf("AddDevice: %v", err)
	}
	return &twoHostServer{server: server, ts: ts, linuxToken: linuxToken, devToken: devToken}
}

// do sends an authenticated request and returns the status and the error
// code (empty on success).
func (th *twoHostServer) do(t *testing.T, method, path, token string, body any) (int, string, []byte) {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		rdr = bytes.NewReader(data)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, th.ts.URL+path, rdr)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	var errResp model.ErrorResponse
	if resp.StatusCode != http.StatusOK {
		_ = json.Unmarshal(buf.Bytes(), &errResp)
	}
	return resp.StatusCode, errResp.Error.Code, buf.Bytes()
}

// waitSSE reads stream until an event named name whose data satisfies match.
func waitSSE(t *testing.T, stream *bufio.Reader, name string, match func(data string) bool) string {
	t.Helper()
	for {
		n, data := nextSSEEvent(t, stream)
		if n == name && (match == nil || match(data)) {
			return data
		}
	}
}

func writeWire(t *testing.T, ctx context.Context, conn *websocket.Conn, msg any) {
	t.Helper()
	data, _ := json.Marshal(msg)
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatalf("write %T: %v", msg, err)
	}
}

// expectNoCommand fails if cmds yields a command within a short wait.
func expectNoCommand(t *testing.T, cmds <-chan model.CommandMsg, who string) {
	t.Helper()
	select {
	case cmd := <-cmds:
		t.Fatalf("%s received a command meant for another host: %+v", who, cmd)
	case <-time.After(50 * time.Millisecond):
	}
}

// Two hosts whose herdrs both number a pane w1:p1: each is its own agent in
// the state, the SSE stream and history, and commands reach the right host.
func TestMultiHost_SamePaneOnTwoHosts(t *testing.T) {
	th := newTwoHostServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", th.ts.URL+"/v1/events", nil)
	req.Header.Set("Authorization", "Bearer "+th.devToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/events: %v", err)
	}
	defer resp.Body.Close()
	stream := bufio.NewReader(resp.Body)

	// Both hosts are listed before either connects, by name.
	var snap model.AgentsSnapshot
	_ = json.Unmarshal([]byte(waitSSE(t, stream, "snapshot", nil)), &snap)
	want := []model.HostInfo{{ID: "linux", Name: "Linux box"}, {ID: "main", Name: "Mac"}}
	if snap.HostOnline || snap.HerdrOnline || !sameHosts(snap.Hosts, want) {
		t.Fatalf("first snapshot = online %v herdr %v hosts %+v; want offline, hosts %+v", snap.HostOnline, snap.HerdrOnline, snap.Hosts, want)
	}

	mac := connectTestHost(t, ctx, th.server, th.ts, "w1:p1")
	linux := connectTestHostWithToken(t, ctx, th.server, th.ts, th.linuxToken, "w1:p1", "w1:p2")
	macCmds := answerCommands(ctx, mac)
	linuxCmds := answerCommands(ctx, linux)

	// One host's snapshot never wipes the other's agents.
	snap = th.server.State().Snapshot()
	if got := agentKeys(snap.Agents); strings.Join(got, ",") != "linux/w1:p1,linux/w1:p2,main/w1:p1" {
		t.Fatalf("agents = %v", got)
	}
	want = []model.HostInfo{{ID: "linux", Name: "Linux box", Online: true, HerdrOnline: true}, {ID: "main", Name: "Mac", Online: true, HerdrOnline: true}}
	if !snap.HostOnline || !snap.HerdrOnline || !sameHosts(snap.Hosts, want) {
		t.Fatalf("snapshot = online %v herdr %v hosts %+v; want both online", snap.HostOnline, snap.HerdrOnline, snap.Hosts)
	}

	// Commands by host go to that host only.
	cancelBody := model.CancelRequest{ExpectedSeq: 10}
	for _, tc := range []struct {
		path     string
		to, idle <-chan model.CommandMsg
		toName   string
		idleName string
	}{
		{"/v1/hosts/linux/agents/w1%3Ap1/cancel", linuxCmds, macCmds, "linux", "mac"},
		{"/v1/hosts/main/agents/w1%3Ap1/cancel", macCmds, linuxCmds, "mac", "linux"},
		{"/v1/agents/w1%3Ap2/cancel", linuxCmds, macCmds, "linux", "mac"}, // only linux has w1:p2
	} {
		if code, errCode, _ := th.do(t, "POST", tc.path, th.devToken, cancelBody); code != http.StatusOK {
			t.Fatalf("POST %s = %d %s, want 200", tc.path, code, errCode)
		}
		select {
		case cmd := <-tc.to:
			if cmd.Action != "cancel" || cmd.PaneID != "w1:p1" && cmd.PaneID != "w1:p2" {
				t.Fatalf("%s got %+v", tc.toName, cmd)
			}
		case <-ctx.Done():
			t.Fatalf("POST %s never reached %s", tc.path, tc.toName)
		}
		expectNoCommand(t, tc.idle, tc.idleName)
	}

	// The old path cannot tell which w1:p1; unknown panes and hosts are unknown_pane.
	for _, tc := range []struct {
		path    string
		status  int
		errCode model.ErrorCode
	}{
		{"/v1/agents/w1%3Ap1/cancel", http.StatusConflict, model.ErrHostRequired},
		{"/v1/agents/w9%3Ap9/cancel", http.StatusNotFound, model.ErrUnknownPane},
		{"/v1/hosts/nohost/agents/w1%3Ap1/cancel", http.StatusNotFound, model.ErrUnknownPane},
		{"/v1/hosts/main/agents/w1%3Ap2/cancel", http.StatusNotFound, model.ErrUnknownPane},
	} {
		if code, errCode, _ := th.do(t, "POST", tc.path, th.devToken, cancelBody); code != tc.status || errCode != string(tc.errCode) {
			t.Fatalf("POST %s = %d %s, want %d %s", tc.path, code, errCode, tc.status, tc.errCode)
		}
	}
	expectNoCommand(t, macCmds, "mac")
	expectNoCommand(t, linuxCmds, "linux")

	// SSE events carry the host the relay stamped, whatever the bridge says.
	writeWire(t, ctx, linux, model.AgentUpdateMsg{Type: model.WireAgentUpdate, Agent: model.AgentState{PaneID: "w1:p1", Host: "main", Agent: "claude", Status: model.StatusWorking, StateChangeSeq: 11}})
	var upd model.AgentState
	_ = json.Unmarshal([]byte(waitSSE(t, stream, "agent", nil)), &upd)
	if upd.Host != "linux" || upd.PaneID != "w1:p1" || upd.Status != model.StatusWorking {
		t.Fatalf("agent event = %+v, want linux/w1:p1 working", upd)
	}
	if a, _ := th.server.State().Get("main", "w1:p1"); a.Status != model.StatusBlocked {
		t.Fatalf("mac's w1:p1 changed with linux's update: %+v", a)
	}
	writeWire(t, ctx, linux, model.AgentRemovedMsg{Type: model.WireAgentRemoved, PaneID: "w1:p2"})
	var removed model.AgentRemovedEvent
	_ = json.Unmarshal([]byte(waitSSE(t, stream, "agent_removed", nil)), &removed)
	if removed != (model.AgentRemovedEvent{Host: "linux", PaneID: "w1:p2"}) {
		t.Fatalf("agent_removed = %+v, want linux/w1:p2", removed)
	}

	// History is kept per (host, pane): the same item id on both hosts is two items.
	now := model.Now()
	for _, conn := range []*websocket.Conn{mac, linux} {
		writeWire(t, ctx, conn, model.HistoryItemMsg{Type: model.WireHistoryItem, Item: model.HistoryItem{ID: "same-id", PaneID: "w1:p1", Response: "r", CompletedAt: now}})
		waitSSE(t, stream, "history", nil)
	}
	for _, tc := range []struct {
		query string
		want  []string
	}{
		{"?host=linux&pane_id=w1:p1", []string{"linux"}},
		{"?host=main&pane_id=w1:p1", []string{"main"}},
		{"?pane_id=w1:p1", []string{"linux", "main"}},
		{"?host=main", []string{"main"}},
		{"", []string{"linux", "main"}},
	} {
		_, _, body := th.do(t, "GET", "/v1/history"+tc.query, th.devToken, nil)
		var hist model.HistoryResponse
		_ = json.Unmarshal(body, &hist)
		var hosts []string
		for _, it := range hist.Items {
			hosts = append(hosts, it.Host)
		}
		sort.Strings(hosts)
		if strings.Join(hosts, ",") != strings.Join(tc.want, ",") {
			t.Fatalf("GET /v1/history%s hosts = %v, want %v", tc.query, hosts, tc.want)
		}
	}

	// Each host's status is its own; devices are relay-wide.
	_, _, body := th.do(t, "GET", "/v1/host/status", th.linuxToken, nil)
	var st model.HostStatusResponse
	_ = json.Unmarshal(body, &st)
	if st != (model.HostStatusResponse{Host: "linux", HostOnline: true, HerdrOnline: true, Devices: 1, Agents: 1}) {
		t.Fatalf("linux status = %+v", st)
	}

	// A new snapshot from mac replaces mac's agents only.
	writeWire(t, ctx, mac, model.SnapshotMsg{Type: model.WireSnapshot, Agents: []model.AgentState{{PaneID: "w1:p3", Agent: "claude", Status: model.StatusIdle}}})
	_ = json.Unmarshal([]byte(waitSSE(t, stream, "snapshot", nil)), &snap)
	if got := agentKeys(snap.Agents); strings.Join(got, ",") != "linux/w1:p1,main/w1:p3" {
		t.Fatalf("agents after mac's new snapshot = %v", got)
	}

	// mac's herdr stops: the aggregate herdr flag is off, linux's own stays on.
	writeWire(t, ctx, mac, model.HerdrStatusMsg{Type: model.WireHerdrStatus, HerdrOnline: false})
	var hostEv model.HostEvent
	_ = json.Unmarshal([]byte(waitSSE(t, stream, "host", nil)), &hostEv)
	want = []model.HostInfo{{ID: "linux", Name: "Linux box", Online: true, HerdrOnline: true}, {ID: "main", Name: "Mac", Online: true}}
	if !hostEv.HostOnline || hostEv.HerdrOnline || !sameHosts(hostEv.Hosts, want) {
		t.Fatalf("host event = %+v, want herdr_online false and hosts %+v", hostEv, want)
	}
	if code, errCode, _ := th.do(t, "POST", "/v1/hosts/main/agents/w1%3Ap3/cancel", th.devToken, cancelBody); code != http.StatusServiceUnavailable || errCode != string(model.ErrHerdrOffline) {
		t.Fatalf("command to mac without herdr = %d %s, want 503 herdr_offline", code, errCode)
	}

	// linux goes offline: mac stays online, linux's agents stay (greyed), and
	// commands to linux fail with host_offline.
	_ = linux.Close(websocket.StatusNormalClosure, "bye")
	_ = json.Unmarshal([]byte(waitSSE(t, stream, "host", nil)), &hostEv)
	want = []model.HostInfo{{ID: "linux", Name: "Linux box"}, {ID: "main", Name: "Mac", Online: true}}
	if !hostEv.HostOnline || hostEv.HerdrOnline || !sameHosts(hostEv.Hosts, want) {
		t.Fatalf("host event after linux left = %+v, want hosts %+v", hostEv, want)
	}
	if !th.server.State().HasPane("linux", "w1:p1") {
		t.Fatal("linux's agents were dropped when it went offline")
	}
	if code, errCode, _ := th.do(t, "POST", "/v1/hosts/linux/agents/w1%3Ap1/cancel", th.devToken, cancelBody); code != http.StatusServiceUnavailable || errCode != string(model.ErrHostOffline) {
		t.Fatalf("command to offline linux = %d %s, want 503 host_offline", code, errCode)
	}
}

func agentKeys(agents []model.AgentState) []string {
	out := make([]string, 0, len(agents))
	for _, a := range agents {
		out = append(out, a.Host+"/"+a.PaneID)
	}
	sort.Strings(out)
	return out
}

func sameHosts(got, want []model.HostInfo) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// The same host's token connecting again replaces its connection; another
// host's connection is untouched.
func TestMultiHost_ReplaceIsPerHost(t *testing.T) {
	th := newTwoHostServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mac1 := connectTestHost(t, ctx, th.server, th.ts, "w1:p1")
	linux := connectTestHostWithToken(t, ctx, th.server, th.ts, th.linuxToken, "w1:p1")
	linuxCmds := answerCommands(ctx, linux)
	connectTestHost(t, ctx, th.server, th.ts, "w1:p1")

	_, _, err := mac1.Read(ctx)
	if websocket.CloseStatus(err) != wsCloseCodeReplaced {
		t.Fatalf("first mac connection close = %v, want 4000", websocket.CloseStatus(err))
	}
	if code, errCode, _ := th.do(t, "POST", "/v1/hosts/linux/agents/w1%3Ap1/cancel", th.devToken, model.CancelRequest{ExpectedSeq: 10}); code != http.StatusOK {
		t.Fatalf("command to linux after mac reconnected = %d %s, want 200", code, errCode)
	}
	select {
	case <-linuxCmds:
	case <-ctx.Done():
		t.Fatal("linux never got its command")
	}
}

// Revoking a host closes its connection, removes its agents from the watch's
// list and rejects its token; AW_HOST_TOKEN's host cannot be revoked there.
func TestMultiHost_RevokeHost(t *testing.T) {
	th := newTwoHostServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	connectTestHost(t, ctx, th.server, th.ts, "w1:p1")
	linux := connectTestHostWithToken(t, ctx, th.server, th.ts, th.linuxToken, "w1:p1")
	sub, ch := th.server.State().Subscribe()
	defer th.server.State().Unsubscribe(sub)

	if found, err := th.server.RevokeHost("linux"); !found || err != nil {
		t.Fatalf("RevokeHost(linux) = %v, %v", found, err)
	}
	_, _, err := linux.Read(ctx)
	if websocket.CloseStatus(err) != wsCloseCodeRevoked {
		t.Fatalf("revoked host's close status = %v, want %v", websocket.CloseStatus(err), wsCloseCodeRevoked)
	}
	var snap model.AgentsSnapshot
	_ = json.Unmarshal(waitEvent(t, ch, "snapshot", 5*time.Second).Data, &snap)
	if got := agentKeys(snap.Agents); strings.Join(got, ",") != "main/w1:p1" || len(snap.Hosts) != 1 || snap.Hosts[0].ID != "main" {
		t.Fatalf("snapshot after revoke = agents %v hosts %+v", got, snap.Hosts)
	}

	wsURL := strings.Replace(th.ts.URL, "http://", "ws://", 1) + "/v1/host"
	_, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + th.linuxToken}},
	})
	if err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked token handshake = %v, %v; want 401", resp, err)
	}
	if code, _, _ := th.do(t, "GET", "/v1/host/status", th.linuxToken, nil); code != http.StatusUnauthorized {
		t.Fatalf("revoked token on /v1/host/status = %d, want 401", code)
	}

	if _, err := th.server.RevokeHost("main"); !errors.Is(err, ErrHostIsEnv) {
		t.Fatalf("RevokeHost(main) = %v, want ErrHostIsEnv", err)
	}
	if _, err := th.server.AddHost("main", "", Sha256Hex("x")); !errors.Is(err, ErrHostIsEnv) {
		t.Fatalf("AddHost(main) = %v, want ErrHostIsEnv", err)
	}
	if found, err := th.server.RevokeHost("linux"); found || err != nil {
		t.Fatalf("RevokeHost(linux) again = %v, %v; want not found", found, err)
	}
}

// Without AW_HOST_TOKEN the relay needs a registered host; AW_HOST_ID may not
// name one of them; with registered hosts only, it starts and the legacy host
// is not listed.
func TestMultiHost_HostTokenRules(t *testing.T) {
	t.Run("no token, no host", func(t *testing.T) {
		_, err := NewServer(&Config{ListenAddr: ":0", DataDir: t.TempDir()})
		if err == nil || !strings.Contains(err.Error(), "AW_HOST_TOKEN is required") {
			t.Fatalf("NewServer = %v, want AW_HOST_TOKEN is required", err)
		}
	})

	t.Run("registered hosts only", func(t *testing.T) {
		dir := t.TempDir()
		st, _ := NewStore(dir)
		if _, err := st.AddHost("linux", "", Sha256Hex("tok")); err != nil {
			t.Fatalf("AddHost: %v", err)
		}
		_ = st.Close()
		s, err := NewServer(&Config{ListenAddr: ":0", DataDir: dir})
		if err != nil {
			t.Fatalf("NewServer with a registered host and no AW_HOST_TOKEN: %v", err)
		}
		defer s.Close()
		if hosts := s.State().Hosts(); len(hosts) != 1 || hosts[0] != (model.HostInfo{ID: "linux", Name: "linux"}) {
			t.Fatalf("hosts = %+v, want only linux (name = id)", hosts)
		}
		if _, ok := s.Auth().VerifyHostToken(""); ok {
			t.Fatal("an empty token authenticates when AW_HOST_TOKEN is unset")
		}
		if id, ok := s.Auth().VerifyHostToken("tok"); !ok || id.ID != "linux" {
			t.Fatalf("VerifyHostToken(tok) = %+v, %v", id, ok)
		}
	})

	t.Run("AW_HOST_ID names a registered host", func(t *testing.T) {
		dir := t.TempDir()
		st, _ := NewStore(dir)
		_, _ = st.AddHost("mac", "", Sha256Hex("tok"))
		_ = st.Close()
		_, err := NewServer(&Config{ListenAddr: ":0", DataDir: dir, HostToken: testHostToken, HostID: "mac"})
		if err == nil || !strings.Contains(err.Error(), "both AW_HOST_ID") {
			t.Fatalf("NewServer = %v, want the AW_HOST_ID conflict", err)
		}
	})

	t.Run("AW_HOST_ID and AW_HOST_NAME", func(t *testing.T) {
		s, _ := setupTestServerWith(t, func(c *Config) { c.HostID = "mac"; c.HostName = "My Mac" })
		if id, ok := s.Auth().VerifyHostToken(testHostToken); !ok || id != (HostIdentity{ID: "mac", Name: "My Mac", Legacy: true}) {
			t.Fatalf("VerifyHostToken(AW_HOST_TOKEN) = %+v, %v", id, ok)
		}
		if hosts := s.State().Hosts(); len(hosts) != 1 || hosts[0].ID != "mac" || hosts[0].Name != "My Mac" {
			t.Fatalf("hosts = %+v", hosts)
		}
	})
}

// A version 1 store.json (before hosts) is migrated on open: its history
// belongs to the host of AW_HOST_TOKEN, and the file is version 2 at once.
func TestStore_MigratesVersion1(t *testing.T) {
	dir := t.TempDir()
	now := model.Now()
	v1 := `{"version":1,
		"devices":[{"id":"d1","name":"Watch","token_hash":"h","created_at":"` + now + `","last_seen":"` + now + `"}],
		"history":{"w1:p1":[{"id":"a","pane_id":"w1:p1","agent":"claude","label":"x","response":"r","source":"transcript","completed_at":"` + now + `"}]}}`
	if err := os.WriteFile(filepath.Join(dir, "store.json"), []byte(v1), 0600); err != nil {
		t.Fatalf("write v1: %v", err)
	}

	st, err := OpenStore(dir, "mac")
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	items := st.GetHistory("mac", "w1:p1", 10)
	if len(items) != 1 || items[0].ID != "a" || items[0].Host != "mac" {
		t.Fatalf("migrated history = %+v, want item a on host mac", items)
	}
	if _, ok := st.FindDeviceByTokenHash("h"); !ok {
		t.Fatal("device lost in the migration")
	}

	data, _ := os.ReadFile(filepath.Join(dir, "store.json"))
	var raw struct {
		Version int                          `json:"version"`
		Hosts   []Host                       `json:"hosts"`
		History map[string][]json.RawMessage `json:"history"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("decode migrated file: %v", err)
	}
	if raw.Version != 2 || raw.Hosts == nil || len(raw.History["mac/w1:p1"]) != 1 {
		t.Fatalf("migrated file = version %d hosts %v history keys %v; want version 2, hosts [], mac/w1:p1", raw.Version, raw.Hosts, raw.History)
	}
	_ = st.Close()

	// Reopened as version 2, with another legacy id: the history keeps its host.
	st2, err := OpenStore(dir, "main")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st2.Close()
	if items := st2.GetHistory("", "w1:p1", 10); len(items) != 1 || items[0].Host != "mac" {
		t.Fatalf("reopened history = %+v, want it still on mac", items)
	}
}

func TestStore_RefusesNewerOrBrokenFiles(t *testing.T) {
	for name, content := range map[string]string{
		"newer version":       `{"version":3,"devices":[],"history":{}}`,
		"history key no host": `{"version":2,"hosts":[],"devices":[],"history":{"w1:p1":[]}}`,
		"invalid host id":     `{"version":2,"hosts":[{"id":"Bad ID","name":"x","token_hash":"h"}],"devices":[],"history":{}}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "store.json"), []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := NewStore(dir); err == nil {
				t.Fatalf("NewStore accepted %s", content)
			}
		})
	}
}

func TestStore_Hosts(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewStore(dir)

	for _, tc := range []struct {
		id, name string
		want     error
	}{
		{"linux-1", "Build box", nil},
		{"linux-1", "", ErrHostExists},
		{"Linux", "", ErrInvalidHostID},
		{"", "", ErrInvalidHostID},
		{strings.Repeat("a", 33), "", ErrInvalidHostID},
		{"pi", strings.Repeat("n", 65), ErrInvalidHostName},
		{"pi", "tab\there", ErrInvalidHostName},
		{"pi", "  ", nil}, // name defaults to the id
	} {
		if _, err := st.AddHost(tc.id, tc.name, Sha256Hex(tc.id+tc.name)); !errors.Is(err, tc.want) {
			t.Fatalf("AddHost(%q, %q) = %v, want %v", tc.id, tc.name, err, tc.want)
		}
	}
	if h, ok := st.FindHostByTokenHash(Sha256Hex("linux-1Build box")); !ok || h.ID != "linux-1" || h.Name != "Build box" {
		t.Fatalf("FindHostByTokenHash = %+v, %v", h, ok)
	}
	st.TouchHost("pi")
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st2, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	hosts := st2.ListHosts()
	if len(hosts) != 2 || hosts[1].ID != "pi" || hosts[1].Name != "pi" || hosts[1].LastSeen == "" {
		t.Fatalf("reloaded hosts = %+v", hosts)
	}
	if _, ok := st2.RevokeHost("linux-1"); !ok {
		t.Fatal("RevokeHost(linux-1) found nothing")
	}
	if _, ok := st2.FindHostByTokenHash(Sha256Hex("linux-1Build box")); ok {
		t.Fatal("a revoked host's token still matches")
	}
	data, _ := os.ReadFile(filepath.Join(dir, "store.json"))
	if bytes.Contains(data, []byte("linux-1Build box")) {
		t.Fatal("store.json holds a raw token")
	}
}

// History per (host, pane): the 20-per-pane cap is per host, the 200 cap is
// global, and a pane-only query never returns more than 20.
func TestStore_HistoryPerHost(t *testing.T) {
	st, _ := NewStore(t.TempDir())
	defer st.Close()
	base := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < 25; i++ {
		for _, host := range []string{"a", "b"} {
			st.AddHistory(model.HistoryItem{ID: host + string(rune('A'+i)), Host: host, PaneID: "w1:p1", CompletedAt: base.Add(time.Duration(i) * time.Second).Format(time.RFC3339)})
		}
	}
	if n := len(st.GetHistory("a", "w1:p1", 200)); n != maxHistoryPane {
		t.Fatalf("host a's pane = %d items, want %d", n, maxHistoryPane)
	}
	if n := len(st.GetHistory("", "w1:p1", 200)); n != maxHistoryPane {
		t.Fatalf("pane on every host = %d items, want at most %d", n, maxHistoryPane)
	}
	if n := len(st.GetHistory("", "", 200)); n != 2*maxHistoryPane {
		t.Fatalf("all history = %d items, want %d", n, 2*maxHistoryPane)
	}
}

// With two hosts, a push names the agent's host in its title; the relay
// stamps the host on the update the push is built from.
func TestServer_PushNamesTheHost(t *testing.T) {
	fcm := &recordingSender{msgs: make(chan push.Message, 8)}
	prevNewFCM := newFCMSender
	newFCMSender = func(context.Context, []byte, func() []string, func(string), bool) (push.Sender, string, error) {
		return fcm, "test-project", nil
	}
	t.Cleanup(func() { newFCMSender = prevNewFCM })
	credsPath := filepath.Join(t.TempDir(), "fcm.json")
	if err := os.WriteFile(credsPath, []byte("fake-credentials"), 0600); err != nil {
		t.Fatalf("write creds: %v", err)
	}
	server, ts := setupTestServerWith(t, func(c *Config) {
		c.FCMCredentials = credsPath
		c.HostName = "Mac"
	})
	linuxToken, _ := GenerateDeviceToken()
	if _, err := server.AddHost("linux", "Linux box", Sha256Hex(linuxToken)); err != nil {
		t.Fatalf("AddHost: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	linux := connectTestHostWithToken(t, ctx, server, ts, linuxToken)
	writeWire(t, ctx, linux, model.AgentUpdateMsg{Type: model.WireAgentUpdate, Agent: model.AgentState{
		PaneID: "w1:p1", Agent: "claude", Label: "my-app", Status: model.StatusBlocked, StateChangeSeq: 3,
	}})
	select {
	case m := <-fcm.msgs:
		if m.Host != "linux" || m.HostName != "Linux box" || m.Title != "Linux box · my-app needs approval" {
			t.Fatalf("push = host %q, host name %q, title %q", m.Host, m.HostName, m.Title)
		}
	case <-ctx.Done():
		t.Fatal("no push")
	}
}
