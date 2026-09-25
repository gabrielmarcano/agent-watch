package bridge

import (
	"encoding/json"
	"testing"
	"time"
)

func TestLocalStatus_ApplyStatusFile(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	at := func(ago time.Duration) string { return now.Add(-ago).Format(time.RFC3339) }
	alive := func(pid int) bool { return pid == 4242 }

	running := StatusFile{
		PID: 4242, RelayConnected: true, HerdrOnline: true, Agents: 5, Blocked: 2,
		Version: "0.2.0", UpdatedAt: at(3 * time.Second),
	}

	cases := []struct {
		name        string
		st          *StatusFile
		wantRunning bool
		wantStale   bool
		wantAge     int
		wantBlocked int
		wantRelay   bool
		wantErr     string
	}{
		{name: "no status file", st: nil, wantAge: -1},
		{name: "fresh and alive", st: &running, wantRunning: true, wantAge: 3, wantBlocked: 2, wantRelay: true},
		{name: "alive but not writing", st: func() *StatusFile {
			s := running
			s.UpdatedAt = at(20 * time.Second)
			return &s
		}(), wantRunning: true, wantStale: true, wantAge: 20, wantBlocked: 2, wantRelay: true},
		{name: "clean exit (pid 0) keeps the reason", st: &StatusFile{
			PID: 0, LastError: "config missing", UpdatedAt: at(time.Minute),
		}, wantAge: 60, wantErr: "config missing"},
		{name: "dead pid reports nothing connected", st: func() *StatusFile {
			s := running
			s.PID = 999
			return &s
		}(), wantAge: 3},
		{name: "unreadable timestamp while alive is stale", st: func() *StatusFile {
			s := running
			s.UpdatedAt = "garbage"
			return &s
		}(), wantRunning: true, wantStale: true, wantAge: -1, wantBlocked: 2, wantRelay: true},
		{name: "clock skew counts as fresh", st: func() *StatusFile {
			s := running
			s.UpdatedAt = now.Add(10 * time.Second).Format(time.RFC3339)
			return &s
		}(), wantRunning: true, wantAge: 0, wantBlocked: 2, wantRelay: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var ls LocalStatus
			ls.ApplyStatusFile(tc.st, now, alive)
			if ls.Running != tc.wantRunning || ls.Stale != tc.wantStale || ls.AgeSeconds != tc.wantAge {
				t.Errorf("running=%v stale=%v age=%d, want %v %v %d", ls.Running, ls.Stale, ls.AgeSeconds, tc.wantRunning, tc.wantStale, tc.wantAge)
			}
			if ls.Blocked != tc.wantBlocked || ls.RelayConnected != tc.wantRelay {
				t.Errorf("blocked=%d relay=%v, want %d %v", ls.Blocked, ls.RelayConnected, tc.wantBlocked, tc.wantRelay)
			}
			if ls.LastError != tc.wantErr {
				t.Errorf("last_error=%q, want %q", ls.LastError, tc.wantErr)
			}
		})
	}
}

// The menu bar decodes these keys; they must always be present.
func TestLocalStatus_JSONKeys(t *testing.T) {
	data, err := json.Marshal(LocalStatus{})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{
		"installed", "configured", "running", "stale", "relay_connected", "herdr_online",
		"agents", "blocked", "last_error", "relay_host", "updated_at", "version", "binary",
	} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing key %q", k)
		}
	}
}
