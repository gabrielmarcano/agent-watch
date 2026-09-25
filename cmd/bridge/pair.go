package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/bridge"
	"github.com/gabrielmarcano/agent-monitor/pkg/herdr"
	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

// pairOutput is what `pair --json` prints (the menu bar reads it).
type pairOutput struct {
	Code             string `json:"code"`
	ExpiresAt        string `json:"expires_at"`
	ExpiresInSeconds int    `json:"expires_in_seconds"`
	RelayHost        string `json:"relay_host"`
}

// cmdPair asks the relay for a pairing code and shows it with its real
// expiry. It needs only the config and the relay, not a running bridge.
func (a *app) cmdPair(args []string) error {
	fs := a.flagSet("pair")
	asJSON := fs.Bool("json", false, "print JSON")
	configPath := fs.String("config", "", "config file (default: the installed service's)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	r := a.resolveForRead(ServiceSpec{ConfigPath: *configPath})
	cfg, err := bridge.CheckConfig(r.spec.ConfigPath)
	if err != nil {
		return err
	}
	base, err := bridge.HTTPSBase(cfg.RelayURL)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/host/pair-code", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.HostToken)
	resp, err := a.http.Do(req)
	if err != nil {
		return fmt.Errorf("request a pairing code from %s: %w", base, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))

	if resp.StatusCode != http.StatusOK {
		var er model.ErrorResponse
		detail := strings.TrimSpace(string(body))
		if json.Unmarshal(body, &er) == nil && er.Error.Code != "" {
			detail = er.Error.Code + ": " + er.Error.Message
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return fmt.Errorf("the relay rejected the host token (HTTP %d, %s); check host_token in %s", resp.StatusCode, detail, r.spec.ConfigPath)
		}
		return fmt.Errorf("the relay refused the pairing request (HTTP %d): %s", resp.StatusCode, detail)
	}

	var pc model.PairCodeResponse
	if err := json.Unmarshal(body, &pc); err != nil || pc.Code == "" {
		return fmt.Errorf("decode the relay's pairing code: %v", err)
	}

	out := pairOutput{Code: pc.Code, ExpiresAt: pc.ExpiresAt}
	if u, err := url.Parse(cfg.RelayURL); err == nil {
		out.RelayHost = u.Host
	}
	expires, expErr := time.Parse(time.RFC3339, pc.ExpiresAt)
	var remaining time.Duration
	if expErr == nil {
		remaining = expires.Sub(a.now()).Round(time.Second)
		if remaining < 0 {
			remaining = 0
		}
		out.ExpiresInSeconds = int(remaining / time.Second)
	}

	if *asJSON {
		if err := json.NewEncoder(a.stdout).Encode(out); err != nil {
			return err
		}
	} else {
		fmt.Fprintln(a.stdout)
		fmt.Fprintf(a.stdout, "Pairing code:  %s\n", spacedCode(pc.Code))
		if expErr == nil {
			fmt.Fprintf(a.stdout, "Expires at:    %s (in %s)\n", expires.Local().Format("15:04:05"), remaining)
		} else {
			fmt.Fprintf(a.stdout, "Expires at:    %s\n", pc.ExpiresAt)
		}
		fmt.Fprintf(a.stdout, "Relay URL:     %s\n", cfg.RelayURL)
		fmt.Fprintln(a.stdout)
	}

	// A herdr action's stdout may not be visible: also show a notification.
	notice := "Enter it on your watch."
	if expErr == nil {
		notice = fmt.Sprintf("Enter it on your watch. Expires at %s (in %s).",
			expires.Local().Format("15:04"), humanMinutes(remaining))
	}
	nctx, ncancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer ncancel()
	_ = herdr.NewClient(r.spec.SocketPath).Call(nctx, "notification.show", map[string]any{
		"title": "Agent Watch pairing code " + pc.Code,
		"body":  notice,
		"sound": "request",
	}, nil)
	return nil
}

// spacedCode renders "417293" as "4 1 7 · 2 9 3".
func spacedCode(code string) string {
	if len(code) != 6 {
		return code
	}
	return fmt.Sprintf("%c %c %c · %c %c %c", code[0], code[1], code[2], code[3], code[4], code[5])
}

// humanMinutes renders a short duration for a notification: "5 min", "40 s".
func humanMinutes(d time.Duration) string {
	if d >= time.Minute {
		return fmt.Sprintf("%d min", int((d+30*time.Second)/time.Minute))
	}
	return fmt.Sprintf("%d s", int(d/time.Second))
}
