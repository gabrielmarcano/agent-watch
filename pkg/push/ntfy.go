package push

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Ntfy implements Sender for Apple Watch via ntfy.sh.
type Ntfy struct {
	BaseURL string
	Topic   string
	Token   string
	Client  *http.Client
}

// Name implements Sender.
func (n *Ntfy) Name() string {
	return "ntfy"
}

// Send delivers m as a plain-text notification to the ntfy topic.
func (n *Ntfy) Send(ctx context.Context, m Message) error {
	base := strings.TrimRight(n.BaseURL, "/")
	topic := strings.TrimLeft(n.Topic, "/")
	if base == "" || topic == "" {
		return fmt.Errorf("ntfy missing BaseURL (%q) or Topic (set: %t)", base, topic != "")
	}

	target := fmt.Sprintf("%s/%s", base, topic)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(m.Body))
	if err != nil {
		return fmt.Errorf("new ntfy request: %w", err)
	}

	req.Header.Set("Title", m.Title)

	switch m.Event {
	case EventBlocked:
		req.Header.Set("Priority", "5")
		req.Header.Set("Tags", "warning")
	case EventDone:
		req.Header.Set("Priority", "3")
		req.Header.Set("Tags", "white_check_mark")
	case EventDigest:
		req.Header.Set("Priority", "4")
		req.Header.Set("Tags", "bell")
	default:
		req.Header.Set("Priority", "3")
	}

	if n.Token != "" {
		req.Header.Set("Authorization", "Bearer "+n.Token)
	}

	client := n.Client
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		// *url.Error prints the request URL, which contains the secret topic,
		// and the dispatcher logs this error.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = &url.Error{Op: ue.Op, URL: base + "/<topic>", Err: ue.Err}
		}
		return fmt.Errorf("ntfy post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ntfy status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}
