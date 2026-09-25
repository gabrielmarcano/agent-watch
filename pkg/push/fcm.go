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
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const (
	defaultFCMEndpoint   = "https://fcm.googleapis.com"
	defaultFCMRetryDelay = time.Second
)

// FCM implements Sender for Wear OS via Firebase Cloud Messaging HTTP v1.
type FCM struct {
	ProjectID      string
	Client         *http.Client
	Tokens         func() []string
	Endpoint       string
	OnInvalidToken func(token string)
	Logger         *slog.Logger
	// RetryDelay is the pause before retrying a token after a transient
	// failure. Zero means 1s.
	RetryDelay time.Duration
}

// NewFCMFromCredentials parses Firebase service account credentials and initializes FCM.
func NewFCMFromCredentials(ctx context.Context, credsJSON []byte, tokens func() []string, onInvalidToken func(string)) (*FCM, error) {
	creds, err := google.CredentialsFromJSON(ctx, credsJSON, "https://www.googleapis.com/auth/firebase.messaging")
	if err != nil {
		return nil, fmt.Errorf("parse google credentials: %w", err)
	}

	client := oauth2.NewClient(ctx, creds.TokenSource)
	return &FCM{
		ProjectID:      creds.ProjectID,
		Client:         client,
		Tokens:         tokens,
		Endpoint:       defaultFCMEndpoint,
		OnInvalidToken: onInvalidToken,
	}, nil
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
	if f.Tokens == nil {
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

	priority := "normal"
	if m.Event == EventBlocked {
		priority = "high"
	}

	seqStr := ""
	if m.StateChangeSeq > 0 {
		seqStr = strconv.FormatUint(m.StateChangeSeq, 10)
	}

	// All data values must be strings, with empty strings when unknown
	data := map[string]string{
		"event":            string(m.Event),
		"pane_id":          m.PaneID,
		"agent":            m.Agent,
		"label":            m.Label,
		"title":            m.Title,
		"body":             m.Body,
		"state_change_seq": seqStr,
		"fingerprint":      m.Fingerprint,
		"allow_option_id":  m.AllowOptionID,
		"deny_option_id":   m.DenyOptionID,
	}

	client := f.Client
	if client == nil {
		client = http.DefaultClient
	}

	var sendErrors []error
	for _, token := range tokens {
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
		if err := f.sendToken(ctx, client, url, payload); err != nil {
			sendErrors = append(sendErrors, err)
		}
	}

	// Each token was already retried as needed: the dispatcher must not
	// retry the whole Send, or the tokens that succeeded get it twice.
	return NoRetry(errors.Join(sendErrors...))
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
// token itself is dead, as opposed to a payload or server error. FCM returns
// 400 INVALID_ARGUMENT for payload errors too (bad ttl, oversize data…), so
// that status alone must never unregister a token.
func isDeadTokenResponse(status int, body []byte) bool {
	if status == http.StatusNotFound {
		return true
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
	return invalidArgument && tokenField
}
