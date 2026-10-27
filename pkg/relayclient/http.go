package relayclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

// HTTPSBase returns the HTTP/HTTPS base URL derived from a WebSocket relay URL.
// E.g. "wss://relay.example.com/v1/host" -> "https://relay.example.com".
func HTTPSBase(relayURL string) (string, error) {
	u, err := url.Parse(relayURL)
	if err != nil {
		return "", fmt.Errorf("invalid relay_url: %w", err)
	}

	var scheme string
	switch u.Scheme {
	case "wss":
		scheme = "https"
	case "ws":
		scheme = "http"
	default:
		return "", fmt.Errorf("unsupported scheme %q; expected ws or wss", u.Scheme)
	}

	return fmt.Sprintf("%s://%s", scheme, u.Host), nil
}

// NormalizeRelayURL ensures the WebSocket URL has the /v1/host path.
// E.g. "wss://relay.example.com" -> "wss://relay.example.com/v1/host".
func NormalizeRelayURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/v1/host"
	}
	return u.String()
}

// HTTPError describes a non-200 HTTP response from the relay.
type HTTPError struct {
	StatusCode int
	Detail     string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Detail)
}

// HostStatus calls GET /v1/host/status on the relay using the provided HTTP client.
func HostStatus(ctx context.Context, client *http.Client, relayURL, hostToken string) (json.RawMessage, error) {
	if client == nil {
		client = http.DefaultClient
	}
	base, err := HTTPSBase(relayURL)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/host/status", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+hostToken)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach the relay: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("relay answered HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if !json.Valid(body) {
		return nil, errors.New("relay answered invalid JSON")
	}
	return json.RawMessage(body), nil
}

// RequestPairCode calls POST /v1/host/pair-code on the relay using the provided HTTP client.
func RequestPairCode(ctx context.Context, client *http.Client, relayURL, hostToken string) (model.PairCodeResponse, error) {
	if client == nil {
		client = http.DefaultClient
	}
	base, err := HTTPSBase(relayURL)
	if err != nil {
		return model.PairCodeResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/host/pair-code", nil)
	if err != nil {
		return model.PairCodeResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+hostToken)

	resp, err := client.Do(req)
	if err != nil {
		return model.PairCodeResponse{}, fmt.Errorf("request a pairing code from %s: %w", base, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		var er model.ErrorResponse
		detail := strings.TrimSpace(string(body))
		if json.Unmarshal(body, &er) == nil && er.Error.Code != "" {
			detail = er.Error.Code + ": " + er.Error.Message
		}
		return model.PairCodeResponse{}, &HTTPError{StatusCode: resp.StatusCode, Detail: detail}
	}

	var pc model.PairCodeResponse
	if err := json.Unmarshal(body, &pc); err != nil || pc.Code == "" {
		return model.PairCodeResponse{}, fmt.Errorf("decode the relay's pairing code: %v", err)
	}
	return pc, nil
}
