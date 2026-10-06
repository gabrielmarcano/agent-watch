package bridge

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParsePresence(t *testing.T) {
	hid := readFixture(t, "ioreg_hid.txt")
	unlocked := readFixture(t, "ioreg_root_unlocked.txt")
	locked := readFixture(t, "ioreg_root_locked.txt")
	cases := []struct {
		name       string
		hid, root  []byte
		wantOK     bool
		wantLocked bool
	}{
		{"unlocked", hid, unlocked, true, false},
		{"locked", hid, locked, true, true},
		{"empty hid", nil, unlocked, false, false},
		{"garbage hid", []byte(`"HIDIdleTime" = banana`), unlocked, false, false},
		{"empty root", hid, nil, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, ok := parsePresence(tc.hid, tc.root)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && p.Locked != tc.wantLocked {
				t.Fatalf("Locked = %v, want %v", p.Locked, tc.wantLocked)
			}
		})
	}
}

func TestParsePresence_IdleValue(t *testing.T) {
	hid := []byte("  |   \"HIDIdleTime\" = 90000000000\n")
	root := []byte(`"IOConsoleUsers" = ({"kCGSSessionOnConsoleKey"=Yes})`)
	p, ok := parsePresence(hid, root)
	if !ok || p.Idle != 90*time.Second {
		t.Fatalf("parsePresence = %+v, %v; want 90s", p, ok)
	}
}

func TestConnectMessages_IncludesPresence(t *testing.T) {
	e := NewEngine(nil, nil, nil, nil, "test", "host", "", nil)
	e.Presence = func(context.Context) (Presence, bool) {
		return Presence{Idle: 75 * time.Second, Locked: true}, true
	}
	msgs := e.ConnectMessages(context.Background())
	if len(msgs) != 3 {
		t.Fatalf("ConnectMessages = %d messages, want hello, snapshot, host_presence", len(msgs))
	}
	p, ok := msgs[2].(model.HostPresenceMsg)
	if !ok || p.Type != model.WireHostPresence || p.IdleSeconds != 75 || !p.Locked {
		t.Fatalf("third message = %#v", msgs[2])
	}
}

func TestConnectMessages_NoPresenceReader(t *testing.T) {
	e := NewEngine(nil, nil, nil, nil, "test", "host", "", nil)
	if n := len(e.ConnectMessages(context.Background())); n != 2 {
		t.Fatalf("ConnectMessages = %d messages, want 2", n)
	}
	e.Presence = func(context.Context) (Presence, bool) { return Presence{}, false }
	if n := len(e.ConnectMessages(context.Background())); n != 2 {
		t.Fatalf("with a failed read: %d messages, want 2", n)
	}
}
