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

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const defaultFCMEndpoint = "https://fcm.googleapis.com"

// FCM implements Sender for Wear OS via Firebase Cloud Messaging HTTP v1.
type FCM struct {
	ProjectID      string
	Client         *http.Client
	Tokens         func() []string
	Endpoint       string
	OnInvalidToken func(token string)
	Logger         *slog.Logger
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

		bodyBytes, err := json.Marshal(payload)
		if err != nil {
			sendErrors = append(sendErrors, fmt.Errorf("marshal fcm payload: %w", err))
			continue
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
		if err != nil {
			sendErrors = append(sendErrors, fmt.Errorf("new fcm request: %w", err))
			continue
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			sendErrors = append(sendErrors, fmt.Errorf("fcm post: %w", err))
			continue
		}

		respBody, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			continue
		}

		// Detect dead / invalid tokens: 404, or 400 with UNREGISTERED or INVALID_ARGUMENT
		respStr := string(respBody)
		if resp.StatusCode == http.StatusNotFound ||
			(resp.StatusCode == http.StatusBadRequest && (strings.Contains(respStr, "UNREGISTERED") || strings.Contains(respStr, "INVALID_ARGUMENT"))) {
			if f.OnInvalidToken != nil {
				f.OnInvalidToken(token)
			}
		}

		sendErrors = append(sendErrors, fmt.Errorf("fcm status %d: %s", resp.StatusCode, respStr))
	}

	return errors.Join(sendErrors...)
}
