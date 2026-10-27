package model

import "net/http"

// PairRequest is sent by a watch to pair with the relay.
type PairRequest struct {
	Code       string `json:"code"`        // 6 digits
	DeviceName string `json:"device_name"` // e.g. "Pixel Watch 2"
}

// PairResponse contains the issued device credentials.
type PairResponse struct {
	DeviceID    string `json:"device_id"`
	DeviceToken string `json:"device_token"` // 64 hex chars; shown once, stored hashed
}

// PairCodeResponse is returned to the host when generating a new pairing code.
type PairCodeResponse struct {
	Code      string `json:"code"`
	ExpiresAt string `json:"expires_at"` // now + 5 minutes (RFC 3339 UTC)
}

// HostStatusResponse summarizes the host and relay status.
type HostStatusResponse struct {
	Host        string `json:"host,omitempty"` // the calling host's id
	HostOnline  bool   `json:"host_online"`    // the calling host
	HerdrOnline bool   `json:"herdr_online"`   // the calling host
	Devices     int    `json:"devices"`        // every paired device
	Agents      int    `json:"agents"`         // the calling host's agents
}

// AgentRemovedEvent is the data of the SSE "agent_removed" event.
type AgentRemovedEvent struct {
	Host   string `json:"host,omitempty"`
	PaneID string `json:"pane_id"`
}

// HostEvent is the data of the SSE "host" event: the snapshot's aggregate
// flags and the full host list.
type HostEvent struct {
	HostOnline  bool       `json:"host_online"`
	HerdrOnline bool       `json:"herdr_online"`
	Hosts       []HostInfo `json:"hosts,omitempty"`
}

// HistoryResponse contains a list of history turns.
type HistoryResponse struct {
	Items []HistoryItem `json:"items"`
}

// PromptRequest delivers user input to an agent pane.
type PromptRequest struct {
	Text        string `json:"text"` // 1..4000 characters (Unicode code points, not bytes)
	ExpectedSeq uint64 `json:"expected_seq"`
}

// AnswerRequest selects an option in a prompt.
type AnswerRequest struct {
	OptionID    string `json:"option_id"`
	ExpectedSeq uint64 `json:"expected_seq"`
	Fingerprint string `json:"fingerprint"`
}

// CancelRequest dismisses a prompt or interrupts an agent. Fingerprint is the
// prompt the watch showed; when empty the bridge checks the published prompt.
type CancelRequest struct {
	ExpectedSeq uint64 `json:"expected_seq"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

// PushRegisterRequest registers a device push token with the relay.
type PushRegisterRequest struct {
	Platform string `json:"platform"` // e.g. "fcm"
	Token    string `json:"token"`
}

// SessionOptionsResponse is what a host allows for new agent sessions: the
// agent kinds its bridge may start and the root folders they may use. Empty
// roots mean session creation is not configured on that host.
type SessionOptionsResponse struct {
	Kinds []string `json:"kinds"`
	Roots []string `json:"roots"`
}

// FolderEntry is one subdirectory in a FolderListResponse.
type FolderEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// FolderListResponse lists the subdirectories of one folder on a host, for
// the phone's folder picker.
type FolderListResponse struct {
	Path      string        `json:"path"`             // the resolved directory listed; "" when listing the roots
	Parent    string        `json:"parent,omitempty"` // the next folder up, when it is still inside a root
	Roots     []string      `json:"roots"`            // every configured root, resolved
	Entries   []FolderEntry `json:"entries"`          // subdirectories sorted by name; the roots when path is ""
	Truncated bool          `json:"truncated,omitempty"`
}

// SessionCreateRequest asks a host's bridge to start one new agent session in
// cwd. The bridge re-validates kind and cwd against its configuration.
type SessionCreateRequest struct {
	Kind  string `json:"kind"`
	Cwd   string `json:"cwd"`
	Label string `json:"label,omitempty"` // tab and agent name; default: the folder's name
}

// SessionCreated identifies the agent pane a host started.
type SessionCreated struct {
	Host        string `json:"host,omitempty"` // stamped by the relay
	PaneID      string `json:"pane_id"`
	WorkspaceID string `json:"workspace_id"`
	TabID       string `json:"tab_id"`
	Kind        string `json:"kind"`
	Label       string `json:"label"` // the name actually used, possibly suffixed to stay unique
}

// The Android apps the relay can publish updates for (contracts.md §2.1).
const (
	UpdateAppPhone = "phone"
	UpdateAppWatch = "watch"
)

// ValidUpdateApp reports whether app is one of the update endpoints' closed set.
func ValidUpdateApp(app string) bool {
	return app == UpdateAppPhone || app == UpdateAppWatch
}

// UpdateInfo is one Android app's published build (contracts.md §2.2).
type UpdateInfo struct {
	App         string `json:"app"` // "phone" | "watch"
	VersionCode int    `json:"version_code"`
	VersionName string `json:"version_name"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"` // bytes
}

// CommandResponse indicates successful command receipt.
type CommandResponse struct {
	OK bool `json:"ok"` // always true on 200
}

// ErrorBody describes the reason for an API failure.
type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ErrorResponse wraps ErrorBody in non-200 responses.
type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

// ErrorCode identifies canonical error reasons across the system.
type ErrorCode string

const (
	ErrInvalidRequest    ErrorCode = "invalid_request"
	ErrUnauthorized      ErrorCode = "unauthorized"
	ErrUnknownPane       ErrorCode = "unknown_pane"
	ErrUpdateUnavailable ErrorCode = "update_not_available"
	ErrStaleState        ErrorCode = "stale_state"
	ErrPromptChanged     ErrorCode = "prompt_changed"
	ErrAgentBusy         ErrorCode = "agent_busy"
	ErrAgentBlocked      ErrorCode = "agent_blocked"
	ErrAgentStateUnknown ErrorCode = "agent_state_unknown"
	ErrUnknownOption     ErrorCode = "unknown_option"
	ErrPairCodeInvalid   ErrorCode = "pair_code_invalid"
	ErrRateLimited       ErrorCode = "rate_limited"
	ErrHostOffline       ErrorCode = "host_offline"
	ErrHostRequired      ErrorCode = "host_required"
	ErrHerdrOffline      ErrorCode = "herdr_offline"
	ErrNotAllowed        ErrorCode = "not_allowed"
	ErrTimeout           ErrorCode = "timeout"
	ErrInternal          ErrorCode = "internal"
)

// HTTPStatus returns the HTTP status code for an ErrorCode as defined in contracts.md §2.4.
func (c ErrorCode) HTTPStatus() int {
	switch c {
	case ErrInvalidRequest:
		return http.StatusBadRequest // 400
	case ErrUnauthorized:
		return http.StatusUnauthorized // 401
	case ErrPairCodeInvalid, ErrNotAllowed:
		return http.StatusForbidden // 403
	case ErrUnknownPane, ErrUpdateUnavailable:
		return http.StatusNotFound // 404
	case ErrStaleState, ErrPromptChanged, ErrAgentBusy, ErrAgentBlocked, ErrAgentStateUnknown, ErrUnknownOption, ErrHostRequired:
		return http.StatusConflict // 409
	case ErrRateLimited:
		return http.StatusTooManyRequests // 429
	case ErrHostOffline, ErrHerdrOffline:
		return http.StatusServiceUnavailable // 503
	case ErrTimeout:
		return http.StatusGatewayTimeout // 504
	default:
		return http.StatusInternalServerError // 500
	}
}
