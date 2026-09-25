package push

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const (
	defaultFCMEndpoint     = "https://fcm.googleapis.com"
	defaultFCMRetryDelay   = time.Second
	defaultFCMTokenTimeout = 10 * time.Second
)

// FCM implements Sender for Wear OS via Firebase Cloud Messaging HTTP v1.
type FCM struct {
	ProjectID string
	Client    *http.Client
	Tokens    func() []string
	Endpoint  string
	// OnInvalidToken is called for each token FCM reports dead. Tokens are
	// sent concurrently, so it may be called from several goroutines at once.
	OnInvalidToken func(token string)
	Logger         *slog.Logger
	// RetryDelay is the pause before retrying a token after a transient
	// failure. Zero means 1s.
	RetryDelay time.Duration
	// TokenTimeout bounds the delivery to one token, retry included. Zero
	// means 10s. The caller's deadline still applies.
	TokenTimeout time.Duration
	// EnableResolved turns on "resolved" messages (contracts.md §4.1). Off by
	// default, and until then SendsResolved reports false.
	EnableResolved bool
}

// NewFCMFromCredentials parses Firebase service account credentials and initializes FCM.
func NewFCMFromCredentials(ctx context.Context, credsJSON []byte, tokens func() []string, onInvalidToken func(string)) (*FCM, error) {
	creds, err := google.CredentialsFromJSON(ctx, credsJSON, "https://www.googleapis.com/auth/firebase.messaging")
	if err != nil {
		return nil, fmt.Errorf("parse google credentials: %w", err)
	}
	return NewFCM(ctx, creds, tokens, onInvalidToken), nil
}

// NewFCM initializes FCM from parsed credentials: every request carries an
// OAuth token from creds.TokenSource, refreshed as needed.
func NewFCM(ctx context.Context, creds *google.Credentials, tokens func() []string, onInvalidToken func(string)) *FCM {
	return &FCM{
		ProjectID:      creds.ProjectID,
		Client:         oauth2.NewClient(ctx, creds.TokenSource),
		Tokens:         tokens,
		Endpoint:       defaultFCMEndpoint,
		OnInvalidToken: onInvalidToken,
	}
}

// Name implements Sender.
func (f *FCM) Name() string {
	return "fcm"
}

type fcmMessagePayload struct {
	Message fcmMessage `json:"message"`
}

type fcmMessage struct {
	Token   string            `json:"token"`
	Data    map[string]string `json:"data"`
	Android fcmAndroid        `json:"android"`
}

type fcmAndroid struct {
	Priority string `json:"priority"`
	TTL      string `json:"ttl"`
}

// Send delivers m to all registered FCM device tokens.
func (f *FCM) Send(ctx context.Context, m Message) error {
	if f.Tokens == nil || (m.Event == EventResolved && !f.EnableResolved) {
		return nil
	}

	tokens := f.Tokens()
	if len(tokens) == 0 {
		return nil
	}

	endpoint := f.Endpoint
	if endpoint == "" {
		endpoint = defaultFCMEndpoint
	}
	url := fmt.Sprintf("%s/v1/projects/%s/messages:send", strings.TrimRight(endpoint, "/"), f.ProjectID)

	// An approval is waiting: high priority, or Doze may hold the push.
	priority := "normal"
	if m.Event == EventBlocked || (m.Event == EventDigest && m.AnyBlocked) {
		priority = "high"
	}

	data := fcmData(m)

	client := f.Client
	if client == nil {
		client = http.DefaultClient
	}

	timeout := f.TokenTimeout
	if timeout <= 0 {
		timeout = defaultFCMTokenTimeout
	}

	// Every token is sent on its own, with its own timeout: one device that
	// never answers must not use up the budget of the others.
	sendErrors := make([]error, len(tokens))
	var wg sync.WaitGroup
	for i, token := range tokens {
		payload := fcmMessagePayload{
			Message: fcmMessage{
				Token: token,
				Data:  data,
				Android: fcmAndroid{
					Priority: priority,
					TTL:      "600s",
				},
			},
		}
		wg.Add(1)
		go func(i int, payload fcmMessagePayload) {
			defer wg.Done()
			tokenCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			sendErrors[i] = f.sendToken(tokenCtx, client, url, payload)
		}(i, payload)
	}
	wg.Wait()

	// Each token was already retried as needed: the dispatcher must not
	// retry the whole Send, or the tokens that succeeded get it twice.
	return NoRetry(errors.Join(sendErrors...))
}

var _ ResolvedSender = (*FCM)(nil)

// SendsResolved implements ResolvedSender: once EnableResolved is set, the
// Wear OS app withdraws the notification of a pane that is no longer blocked.
// It stays off until the installed watch app handles "resolved": older apps
// show it as a bogus "needs you" over the real approval.
func (f *FCM) SendsResolved() bool {
	return f.EnableResolved
}

// fcmData is the data map of m (contracts.md §4.1). All values are strings,
// empty when unknown, and state_change_seq is always a number, "0" included:
// the app parses it. A resolved message carries only what the app needs to
// find the notification to withdraw.
func fcmData(m Message) map[string]string {
	seq := strconv.FormatUint(m.StateChangeSeq, 10)
	if m.Event == EventResolved {
		return map[string]string{
			"event":            string(m.Event),
			"pane_id":          m.PaneID,
			"state_change_seq": seq,
		}
	}
	return map[string]string{
		"event":            string(m.Event),
		"pane_id":          m.PaneID,
		"agent":            m.Agent,
		"label":            m.Label,
		"title":            m.Title,
		"body":             m.Body,
		"state_change_seq": seq,
		"fingerprint":      m.Fingerprint,
		"allow_option_id":  m.AllowOptionID,
		"deny_option_id":   m.DenyOptionID,
	}
}

// sendToken delivers one message and retries it once after a transient
// failure (network error, 429 or 5xx). Other failures are final.
func (f *FCM) sendToken(ctx context.Context, client *http.Client, url string, payload fcmMessagePayload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal fcm payload: %w", err)
	}

	delay := f.RetryDelay
	if delay <= 0 {
		delay = defaultFCMRetryDelay
	}

	retryable, err := f.post(ctx, client, url, payload.Message.Token, body)
	if err == nil || !retryable {
		return err
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return err
	case <-timer.C:
	}

	_, retryErr := f.post(ctx, client, url, payload.Message.Token, body)
	if retryErr != nil {
		return fmt.Errorf("%w (after retry; first attempt: %v)", retryErr, err)
	}
	return nil
}

// post makes one FCM request. retryable reports whether the failure is
// transient: a network error, 429 or 5xx.
func (f *FCM) post(ctx context.Context, client *http.Client, url, token string, body []byte) (retryable bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return false, fmt.Errorf("new fcm request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		// A canceled or expired context is not worth a retry.
		return ctx.Err() == nil, fmt.Errorf("fcm post: %w", err)
	}
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return false, nil
	}

	if isDeadTokenResponse(resp.StatusCode, respBody) && f.OnInvalidToken != nil {
		f.OnInvalidToken(token)
	}

	retryable = resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
	return retryable, fmt.Errorf("fcm status %d: %s", resp.StatusCode, string(respBody))
}

// fcmErrorResponse is the FCM v1 error body (a google.rpc.Status).
type fcmErrorResponse struct {
	Error struct {
		Status  string `json:"status"`
		Details []struct {
			ErrorCode       string `json:"errorCode"` // google.firebase.fcm.v1.FcmError
			FieldViolations []struct {
				Field string `json:"field"`
			} `json:"fieldViolations"` // google.rpc.BadRequest
		} `json:"details"`
	} `json:"error"`
}

// isDeadTokenResponse reports whether an FCM error response means the device
// token itself is dead, as opposed to a payload, project or server error. Only
// two answers qualify:
//   - FCM's UNREGISTERED error code, on a 404 or a 400;
//   - a 400 INVALID_ARGUMENT whose field violation is message.token.
//
// A status alone never does: FCM returns 400 INVALID_ARGUMENT for payload
// errors too (bad ttl, oversize data…), and a 404 without UNREGISTERED means
// a wrong project id or a proxy in the way, which would wipe every token.
func isDeadTokenResponse(status int, body []byte) bool {
	if status != http.StatusNotFound && status != http.StatusBadRequest {
		return false
	}

	var er fcmErrorResponse
	if err := json.Unmarshal(body, &er); err != nil {
		return false
	}

	invalidArgument := er.Error.Status == "INVALID_ARGUMENT"
	tokenField := false
	for _, d := range er.Error.Details {
		switch d.ErrorCode {
		case "UNREGISTERED":
			return true
		case "INVALID_ARGUMENT":
			invalidArgument = true
		}
		for _, fv := range d.FieldViolations {
			if fv.Field == "message.token" {
				tokenField = true
			}
		}
	}
	return status == http.StatusBadRequest && invalidArgument && tokenField
}
