package bridge

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfig_Validation(t *testing.T) {
	validToken := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	tests := []struct {
		name    string
		cfg     *Config
		wantErr bool
	}{
		{
			name: "valid wss",
			cfg: &Config{
				RelayURL:  "wss://relay.example.com/v1/host",
				HostToken: validToken,
			},
			wantErr: false,
		},
		{
			name: "valid ws localhost",
			cfg: &Config{
				RelayURL:  "ws://localhost:8080/v1/host",
				HostToken: validToken,
			},
			wantErr: false,
		},
		{
			name: "valid ws 127.0.0.1",
			cfg: &Config{
				RelayURL:  "ws://127.0.0.1:8080/v1/host",
				HostToken: validToken,
			},
			wantErr: false,
		},
		{
			name: "invalid ws remote",
			cfg: &Config{
				RelayURL:  "ws://relay.example.com/v1/host",
				HostToken: validToken,
			},
			wantErr: true,
		},
		{
			name: "invalid scheme http",
			cfg: &Config{
				RelayURL:  "https://relay.example.com",
				HostToken: validToken,
			},
			wantErr: true,
		},
		{
			name: "token too short",
			cfg: &Config{
				RelayURL:  "wss://relay.example.com",
				HostToken: "0123456789abcdef",
			},
			wantErr: true,
		},
		{
			name: "token non-hex",
			cfg: &Config{
				RelayURL:  "wss://relay.example.com",
				HostToken: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdeg",
			},
			wantErr: true,
		},
		{
			name:    "nil config",
			cfg:     nil,
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateConfig(tc.cfg)
			if (err != nil) != tc.wantErr {
				t.Errorf("ValidateConfig() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestConfig_SaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "sub", "config.toml")

	validToken := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	orig := &Config{
		RelayURL:         "wss://relay.example.com",
		HostToken:        validToken,
		HostName:         "test-host",
		ClaudeConfigDirs: []string{"/custom/claude"},
	}

	if err := SaveConfig(cfgPath, orig); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}

	fi, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatalf("Stat config failed: %v", err)
	}
	// Check mode 0600 (ignoring extra OS permission bits if any)
	perm := fi.Mode().Perm()
	if perm != 0o600 {
		t.Errorf("config file mode = %o, want 0600", perm)
	}

	loaded, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if loaded.RelayURL != orig.RelayURL || loaded.HostToken != orig.HostToken || loaded.HostName != orig.HostName {
		t.Errorf("loaded config does not match orig: %+v vs %+v", loaded, orig)
	}
	if len(loaded.ClaudeConfigDirs) != 1 || loaded.ClaudeConfigDirs[0] != "/custom/claude" {
		t.Errorf("ClaudeConfigDirs mismatch: %v", loaded.ClaudeConfigDirs)
	}
}

func TestConfig_HTTPSBase(t *testing.T) {
	tests := []struct {
		url     string
		want    string
		wantErr bool
	}{
		{"wss://relay.example.com/v1/host", "https://relay.example.com", false},
		{"wss://relay.example.com:8443/v1/host", "https://relay.example.com:8443", false},
		{"ws://localhost:8080/v1/host", "http://localhost:8080", false},
		{"ftp://example.com", "", true},
	}

	for _, tc := range tests {
		got, err := HTTPSBase(tc.url)
		if (err != nil) != tc.wantErr {
			t.Errorf("HTTPSBase(%q) err = %v, wantErr %v", tc.url, err, tc.wantErr)
			continue
		}
		if got != tc.want {
			t.Errorf("HTTPSBase(%q) = %q, want %q", tc.url, got, tc.want)
		}
	}
}

func TestConfig_StatusFile(t *testing.T) {
	tmpDir := t.TempDir()
	statusPath := filepath.Join(tmpDir, "state", "status.json")

	s := StatusFile{
		PID:            1234,
		RelayConnected: true,
		HerdrOnline:    true,
		Agents:         5,
		LastError:      "",
		UpdatedAt:      "2026-09-24T12:00:00Z",
	}

	if err := WriteStatus(statusPath, s); err != nil {
		t.Fatalf("WriteStatus failed: %v", err)
	}

	read, err := ReadStatus(statusPath)
	if err != nil {
		t.Fatalf("ReadStatus failed: %v", err)
	}

	if read != s {
		t.Errorf("ReadStatus mismatch: got %+v, want %+v", read, s)
	}
}
