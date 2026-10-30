package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/bridge"
	"github.com/gabrielmarcano/agent-monitor/pkg/relayclient"
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pc, err := relayclient.RequestPairCode(ctx, a.http, cfg.RelayURL, cfg.HostToken)
	if err != nil {
		var httpErr *relayclient.HTTPError
		if errors.As(err, &httpErr) {
			if httpErr.StatusCode == http.StatusUnauthorized || httpErr.StatusCode == http.StatusForbidden {
				return fmt.Errorf("the relay rejected the host token (HTTP %d, %s); check host_token in %s", httpErr.StatusCode, httpErr.Detail, r.spec.ConfigPath)
			}
			return fmt.Errorf("the relay refused the pairing request (HTTP %d): %s", httpErr.StatusCode, httpErr.Detail)
		}
		return err
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
	return nil
}

// spacedCode renders "417293" as "4 1 7 · 2 9 3".
func spacedCode(code string) string {
	if len(code) != 6 {
		return code
	}
	return fmt.Sprintf("%c %c %c · %c %c %c", code[0], code[1], code[2], code[3], code[4], code[5])
}
