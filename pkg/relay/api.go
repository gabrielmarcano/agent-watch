package relay

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

const (
	// maxPromptChars is the prompt limit from contracts.md §2.2, in Unicode
	// code points (what a person counts as characters), not bytes.
	maxPromptChars = 4000
	// maxPromptBodyBytes fits maxPromptChars in the worst JSON encoding: an
	// astral character escaped as a surrogate pair (two backslash-u escapes) is 12
	// bytes, and some encoders (Gson) escape even '<' or '=' as 6 bytes.
	maxPromptBodyBytes = 64 << 10
)

// writeError serializes an ErrorResponse with the appropriate HTTP status code.
func writeError(w http.ResponseWriter, code model.ErrorCode, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code.HTTPStatus())
	resp := model.ErrorResponse{
		Error: model.ErrorBody{
			Code:    string(code),
			Message: msg,
		},
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// writeJSON serializes data with Content-Type: application/json and status.
func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

type loggingResponseWriter struct {
	http.ResponseWriter
	status int
}

func (lrw *loggingResponseWriter) WriteHeader(status int) {
	lrw.status = status
	lrw.ResponseWriter.WriteHeader(status)
}

func (lrw *loggingResponseWriter) Flush() {
	if f, ok := lrw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// FlushError is what http.ResponseController.Flush calls; unlike Flush it
// reports a failed write (e.g. an expired write deadline) to the SSE handler.
func (lrw *loggingResponseWriter) FlushError() error {
	return http.NewResponseController(lrw.ResponseWriter).Flush()
}

// Unwrap lets http.ResponseController reach the underlying connection
// (the SSE handler sets write deadlines through it).
func (lrw *loggingResponseWriter) Unwrap() http.ResponseWriter {
	return lrw.ResponseWriter
}

func (lrw *loggingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := lrw.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, errors.New("hijack unsupported")
}

// routes registers all HTTP handlers using Go 1.22 ServeMux routing patterns.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /v1/healthz", s.healthz)
	mux.HandleFunc("POST /v1/pair", s.pair)
	mux.HandleFunc("GET /v1/host", s.hub.ServeHost)
	mux.Handle("POST /v1/host/pair-code", s.auth.HostAuthMiddleware(http.HandlerFunc(s.pairCode)))
	mux.Handle("GET /v1/host/status", s.auth.HostAuthMiddleware(http.HandlerFunc(s.hostStatus)))
	mux.Handle("GET /v1/agents", s.auth.DeviceAuthMiddleware(http.HandlerFunc(s.agents)))
	mux.Handle("GET /v1/events", s.auth.DeviceAuthMiddleware(http.HandlerFunc(s.events)))
	mux.Handle("GET /v1/history", s.auth.DeviceAuthMiddleware(http.HandlerFunc(s.history)))
	mux.Handle("POST /v1/hosts/{host}/agents/{pane_id}/prompt", s.auth.DeviceAuthMiddleware(http.HandlerFunc(s.prompt)))
	mux.Handle("POST /v1/hosts/{host}/agents/{pane_id}/answer", s.auth.DeviceAuthMiddleware(http.HandlerFunc(s.answer)))
	mux.Handle("POST /v1/hosts/{host}/agents/{pane_id}/cancel", s.auth.DeviceAuthMiddleware(http.HandlerFunc(s.cancel)))
	// The paths that predate hosts: the relay finds the pane's host.
	mux.Handle("POST /v1/agents/{pane_id}/prompt", s.auth.DeviceAuthMiddleware(http.HandlerFunc(s.prompt)))
	mux.Handle("POST /v1/agents/{pane_id}/answer", s.auth.DeviceAuthMiddleware(http.HandlerFunc(s.answer)))
	mux.Handle("POST /v1/agents/{pane_id}/cancel", s.auth.DeviceAuthMiddleware(http.HandlerFunc(s.cancel)))
	mux.Handle("POST /v1/push/register", s.auth.DeviceAuthMiddleware(http.HandlerFunc(s.pushRegister)))

	// Access logging wrapper
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lrw := &loggingResponseWriter{
			ResponseWriter: w,
			status:         http.StatusOK,
		}

		// The device middleware authenticates on a derived request, so it
		// reports the device back through this entry.
		entry := &accessLogEntry{}
		mux.ServeHTTP(lrw, r.WithContext(context.WithValue(r.Context(), accessLogCtxKey, entry)))

		dur := time.Since(start)
		slog.Info("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", lrw.status,
			"duration_ms", dur.Milliseconds(),
			"device_id", entry.deviceID,
		)
	})
}

// accessLogEntry collects what inner handlers know for the access log line.
// It is only touched by the request's own goroutine.
type accessLogEntry struct {
	deviceID string
}

// noteAccessLogDevice records the authenticated device for the access log.
func noteAccessLogDevice(ctx context.Context, deviceID string) {
	if entry, ok := ctx.Value(accessLogCtxKey).(*accessLogEntry); ok {
		entry.deviceID = deviceID
	}
}

// GET /v1/healthz
func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// POST /v1/pair
func (s *Server) pair(w http.ResponseWriter, r *http.Request) {
	clientIP := s.auth.ClientIP(r)
	if !s.auth.CheckAndRecordAttempt(clientIP) {
		writeError(w, model.ErrRateLimited, "too many pairing attempts")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var req model.PairRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, model.ErrInvalidRequest, "malformed request body")
		return
	}

	code := strings.TrimSpace(req.Code)
	if len(code) != 6 {
		writeError(w, model.ErrInvalidRequest, "pairing code must be 6 digits")
		return
	}

	if !s.auth.ConsumePairCode(code) {
		writeError(w, model.ErrPairCodeInvalid, "invalid or expired pairing code")
		return
	}

	deviceToken, err := GenerateDeviceToken()
	if err != nil {
		writeError(w, model.ErrInternal, "failed to generate device token")
		return
	}

	name := strings.TrimSpace(req.DeviceName)
	if name == "" {
		name = "Unknown Device"
	}

	tokenHash := Sha256Hex(deviceToken)
	dev, err := s.store.AddDevice(name, tokenHash)
	if err != nil {
		writeError(w, model.ErrInternal, "failed to store device")
		return
	}

	writeJSON(w, http.StatusOK, model.PairResponse{
		DeviceID:    dev.ID,
		DeviceToken: deviceToken,
	})
}

// POST /v1/host/pair-code
func (s *Server) pairCode(w http.ResponseWriter, r *http.Request) {
	code, expiresAt, err := s.auth.GeneratePairCode()
	if err != nil {
		writeError(w, model.ErrInternal, "failed to generate pairing code")
		return
	}

	writeJSON(w, http.StatusOK, model.PairCodeResponse{
		Code:      code,
		ExpiresAt: expiresAt.UTC().Format(time.RFC3339),
	})
}

// GET /v1/host/status: the calling host's view; devices are relay-wide.
func (s *Server) hostStatus(w http.ResponseWriter, r *http.Request) {
	host, ok := HostFromContext(r.Context())
	if !ok {
		writeError(w, model.ErrUnauthorized, "unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, model.HostStatusResponse{
		Host:        host.ID,
		HostOnline:  s.state.HostOnline(host.ID),
		HerdrOnline: s.state.HerdrOnline(host.ID),
		Devices:     len(s.store.ListDevices()),
		Agents:      s.state.AgentCount(host.ID),
	})
}

// GET /v1/agents
func (s *Server) agents(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.state.Snapshot())
}

// GET /v1/history?host=&pane_id=&limit=
func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	host := r.URL.Query().Get("host")
	paneID := r.URL.Query().Get("pane_id")
	limitStr := r.URL.Query().Get("limit")

	limit := 20
	if limitStr != "" {
		if n, err := strconv.Atoi(limitStr); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 200 {
		limit = 200
	}

	items := s.store.GetHistory(host, paneID, limit)
	if items == nil {
		items = []model.HistoryItem{}
	}

	writeJSON(w, http.StatusOK, model.HistoryResponse{
		Items: items,
	})
}

// commandTarget is the agent a command names: its host and pane.
type commandTarget struct {
	host string
	pane string
}

// targetFromPath reads {host} and {pane_id}. Without {host} (the paths that
// predate hosts) the command goes to the one host whose agents include the
// pane: host_required when several do, unknown_pane when none does.
func (s *Server) targetFromPath(r *http.Request) (commandTarget, model.ErrorCode, string) {
	paneID := r.PathValue("pane_id")
	if paneID == "" {
		return commandTarget{}, model.ErrInvalidRequest, "missing pane_id"
	}
	host := r.PathValue("host")
	if host != "" {
		if !s.state.HasPane(host, paneID) {
			return commandTarget{}, model.ErrUnknownPane, "unknown pane"
		}
		return commandTarget{host: host, pane: paneID}, "", ""
	}
	switch hosts := s.state.HostsWithPane(paneID); len(hosts) {
	case 0:
		return commandTarget{}, model.ErrUnknownPane, "unknown pane"
	case 1:
		return commandTarget{host: hosts[0], pane: paneID}, "", ""
	default:
		return commandTarget{}, model.ErrHostRequired, "several hosts have this pane: use /v1/hosts/{host}/agents/{pane_id}/..."
	}
}

// runCommand checks the target, sends cmd to its host and writes the outcome.
func (s *Server) runCommand(w http.ResponseWriter, r *http.Request, cmd model.CommandMsg) {
	target, code, msg := s.targetFromPath(r)
	if code != "" {
		writeError(w, code, msg)
		return
	}
	if !s.state.HostOnline(target.host) {
		writeError(w, model.ErrHostOffline, "host offline")
		return
	}
	if !s.state.HerdrOnline(target.host) {
		writeError(w, model.ErrHerdrOffline, "herdr offline")
		return
	}

	cmd.PaneID = target.pane
	res, err := s.hub.Command(r.Context(), target.host, cmd)
	if err != nil {
		writeError(w, model.ErrInternal, err.Error())
		return
	}
	if !res.OK {
		code := model.ErrorCode(res.ErrorCode)
		if code == "" {
			code = model.ErrInternal
		}
		writeError(w, code, res.Message)
		return
	}

	writeJSON(w, http.StatusOK, model.CommandResponse{OK: true})
}

// POST /v1/hosts/{host}/agents/{pane_id}/prompt and /v1/agents/{pane_id}/prompt
func (s *Server) prompt(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("pane_id") == "" {
		writeError(w, model.ErrInvalidRequest, "missing pane_id")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxPromptBodyBytes)
	var req model.PromptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, model.ErrInvalidRequest, "malformed request body")
		return
	}

	if n := utf8.RuneCountInString(req.Text); n == 0 || n > maxPromptChars {
		writeError(w, model.ErrInvalidRequest, fmt.Sprintf("text must be 1 to %d characters", maxPromptChars))
		return
	}

	s.runCommand(w, r, model.CommandMsg{
		Action:      "prompt",
		ExpectedSeq: req.ExpectedSeq,
		Text:        req.Text,
	})
}

// POST /v1/hosts/{host}/agents/{pane_id}/answer and /v1/agents/{pane_id}/answer
func (s *Server) answer(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("pane_id") == "" {
		writeError(w, model.ErrInvalidRequest, "missing pane_id")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var req model.AnswerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, model.ErrInvalidRequest, "malformed request body")
		return
	}

	if strings.TrimSpace(req.OptionID) == "" {
		writeError(w, model.ErrInvalidRequest, "option_id is required")
		return
	}

	s.runCommand(w, r, model.CommandMsg{
		Action:      "answer",
		ExpectedSeq: req.ExpectedSeq,
		OptionID:    req.OptionID,
		Fingerprint: req.Fingerprint,
	})
}

// POST /v1/hosts/{host}/agents/{pane_id}/cancel and /v1/agents/{pane_id}/cancel
func (s *Server) cancel(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("pane_id") == "" {
		writeError(w, model.ErrInvalidRequest, "missing pane_id")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var req model.CancelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, model.ErrInvalidRequest, "malformed request body")
		return
	}

	s.runCommand(w, r, model.CommandMsg{
		Action:      "cancel",
		ExpectedSeq: req.ExpectedSeq,
		Fingerprint: req.Fingerprint,
	})
}

// POST /v1/push/register
func (s *Server) pushRegister(w http.ResponseWriter, r *http.Request) {
	dev, ok := DeviceFromContext(r.Context())
	if !ok {
		writeError(w, model.ErrUnauthorized, "unauthorized")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var req model.PushRegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, model.ErrInvalidRequest, "malformed request body")
		return
	}

	if req.Platform != "fcm" || strings.TrimSpace(req.Token) == "" {
		writeError(w, model.ErrInvalidRequest, "invalid push platform or token")
		return
	}

	s.store.UpdateDeviceFCMToken(dev.ID, strings.TrimSpace(req.Token))
	writeJSON(w, http.StatusOK, model.CommandResponse{OK: true})
}
