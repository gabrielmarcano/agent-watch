package bridge

import "time"

// StaleAfter is how old status.json may get while the bridge process is
// alive before readers call it stale. The daemon writes it every 5 s.
const StaleAfter = 15 * time.Second

// LocalStatus is what `agent-watch-bridge status --json --local` prints: the
// bridge's health from local files only (service definition, config,
// status.json and a pid check), with no network call. The macOS menu bar
// polls it. Every key is always present.
type LocalStatus struct {
	Installed       bool   `json:"installed"`        // a service definition exists
	DefinitionError string `json:"definition_error"` // it exists but cannot be read
	Configured      bool   `json:"configured"`       // the config loads and validates
	ConfigError     string `json:"config_error"`
	Running         bool   `json:"running"` // status.json names a live pid
	Stale           bool   `json:"stale"`   // running, but status.json is older than StaleAfter
	RelayConnected  bool   `json:"relay_connected"`
	HerdrOnline     bool   `json:"herdr_online"`
	Agents          int    `json:"agents"`
	Blocked         int    `json:"blocked"`
	LastError       string `json:"last_error"`
	RelayError      string `json:"relay_error"`
	HerdrError      string `json:"herdr_error"`
	RelayHost       string `json:"relay_host"` // host[:port] of relay_url; never a token
	PID             int    `json:"pid"`
	UpdatedAt       string `json:"updated_at"`
	AgeSeconds      int    `json:"age_seconds"`    // age of status.json; -1 when unknown
	Version         string `json:"version"`        // this CLI, e.g. "0.3.0 (c8aa72e)"
	DaemonVersion   string `json:"daemon_version"` // the bridge that wrote status.json
	RelayVersion    string `json:"relay_version"`  // the relay the running bridge last connected to; "" when unknown
	Service         string `json:"service"`        // launchd | systemd
	DefinitionPath  string `json:"definition_path"`
	Binary          string `json:"binary"`
	ConfigPath      string `json:"config_path"`
	StateDir        string `json:"state_dir"`
	StatusPath      string `json:"status_path"`
	LogPath         string `json:"log_path"`
}

// ApplyStatusFile fills the runtime fields from the daemon's status file (nil
// when there is none). alive reports whether a pid is a live process.
//
// The bridge counts as running only while its pid is alive. A pid of 0 is a
// clean exit or a failed start, whose last_error is kept. While not running,
// nothing is reported as connected.
func (ls *LocalStatus) ApplyStatusFile(st *StatusFile, now time.Time, alive func(pid int) bool) {
	ls.AgeSeconds = -1
	if st == nil {
		return
	}
	ls.PID = st.PID
	ls.UpdatedAt = st.UpdatedAt
	ls.DaemonVersion = st.Version
	ls.LastError = st.LastError
	ls.RelayError = st.RelayError
	ls.HerdrError = st.HerdrError

	age := time.Duration(-1)
	if t, err := time.Parse(time.RFC3339, st.UpdatedAt); err == nil {
		age = now.Sub(t)
		if age < 0 {
			age = 0 // clock skew: treat as just written
		}
		ls.AgeSeconds = int(age / time.Second)
	}

	ls.Running = st.PID > 0 && alive != nil && alive(st.PID)
	if !ls.Running {
		return
	}
	ls.Stale = age < 0 || age > StaleAfter
	ls.RelayConnected = st.RelayConnected
	ls.RelayVersion = st.RelayVersion
	ls.HerdrOnline = st.HerdrOnline
	ls.Agents = st.Agents
	ls.Blocked = st.Blocked
}
