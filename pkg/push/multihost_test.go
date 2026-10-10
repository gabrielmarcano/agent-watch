package push

import (
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

// onHost returns state a on host.
func onHost(host string, a model.AgentState) model.AgentState {
	a.Host = host
	return a
}

// hostEvents lists messages as "event:host/pane", sorted.
func hostEvents(msgs []Message) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, fmt.Sprintf("%s:%s/%s", m.Event, m.Host, m.PaneID))
	}
	sort.Strings(out)
	return out
}

// Two hosts number their panes the same way: every per-pane rule (debounce,
// the prompt already shown, resolved, the reply wait) is per (host, pane).
func TestDispatcher_SamePaneOnTwoHostsIsTwoAgents(t *testing.T) {
	fcm := newResolvedMock()
	d, _, timers := newTestDispatcher(fcm)
	d.ReplyWait = 3 * time.Second

	a := onHost("mac", promptState(10, "fp-a"))
	b := onHost("linux", promptState(10, "fp-a")) // same pane, seq and prompt
	d.OnAgentUpdate(nil, a)
	d.OnAgentUpdate(nil, b) // not the prompt a's notification shows, not debounced by it
	flushWindow(d, timers)
	if got, want := hostEvents(fcm.getMessages()), []string{"blocked:linux/p1", "blocked:mac/p1"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("pushes = %v, want %v", got, want)
	}

	// Answering on mac withdraws mac's notification only.
	answered := onHost("mac", agentAt("p1", "api", model.StatusWorking, 11))
	d.OnAgentUpdate(&a, answered)
	d.Wait()
	msgs := fcm.getMessages()
	last := msgs[len(msgs)-1]
	if last.Event != EventResolved || last.Host != "mac" || last.PaneID != "p1" {
		t.Fatalf("last push = %+v, want resolved for mac/p1", last)
	}
	d.mu.Lock()
	_, linuxShown := d.blockedShown[paneKey{host: "linux", pane: "p1"}]
	d.mu.Unlock()
	if !linuxShown {
		t.Fatal("linux/p1's blocked notification was forgotten when mac answered")
	}

	// A done waits for its own host's reply: the same pane's reply on the
	// other host does not release it.
	before := len(fcm.getMessages())
	d.OnAgentUpdate(&answered, onHost("mac", agentAt("p1", "api", model.StatusDone, 12)))
	d.OnHistoryItem(model.HistoryItem{Host: "linux", PaneID: "p1", Response: "linux reply"})
	d.Wait()
	if n := len(fcm.getMessages()); n != before {
		t.Fatalf("the other host's reply released the done push (%d pushes, want %d)", n, before)
	}
	d.OnHistoryItem(model.HistoryItem{Host: "mac", PaneID: "p1", Response: "mac reply"})
	flushWindow(d, timers)
	msgs = fcm.getMessages()
	last = msgs[len(msgs)-1]
	if last.Event != EventDone || last.Host != "mac" || last.Body != "mac reply" {
		t.Fatalf("last push = %+v, want mac's done with mac's reply", last)
	}
}

// Titles start with the host's name only when the relay knows several hosts;
// host_name is always set; a digest names no host.
func TestDispatcher_HostNameInTitles(t *testing.T) {
	cases := []struct {
		name      string
		hosts     int
		wantTitle string
	}{
		{"one host", 1, "api needs approval"},
		{"two hosts", 2, "Mac · api needs approval"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sender := &mockSender{name: "mock"}
			d, _, _ := newTestDispatcher(sender)
			d.HostName = func(host string) (string, int) {
				if host != "mac" {
					t.Errorf("HostName(%q), want mac", host)
				}
				return "Mac", tc.hosts
			}
			d.OnAgentUpdate(nil, onHost("mac", blockedState("w1:p1", "api")))
			d.Wait()
			got := sender.getMessages()
			if len(got) != 1 {
				t.Fatalf("pushes = %d, want 1", len(got))
			}
			if got[0].Title != tc.wantTitle || got[0].HostName != "Mac" || got[0].Host != "mac" {
				t.Fatalf("message = title %q, host %q, host name %q; want %q, mac, Mac", got[0].Title, got[0].Host, got[0].HostName, tc.wantTitle)
			}
		})
	}

	t.Run("done with two hosts", func(t *testing.T) {
		sender := &mockSender{name: "mock"}
		d, _, _ := newTestDispatcher(sender)
		d.HostName = func(string) (string, int) { return "Build box", 3 }
		working := onHost("linux", agentAt("w1:p1", "api", model.StatusWorking, 1))
		d.OnAgentUpdate(&working, onHost("linux", agentAt("w1:p1", "api", model.StatusDone, 2)))
		d.Wait()
		if got := sender.getMessages(); len(got) != 1 || got[0].Title != "Build box · api finished" {
			t.Fatalf("pushes = %+v, want one titled %q", got, "Build box · api finished")
		}
	})

	t.Run("digest", func(t *testing.T) {
		sender := &mockSender{name: "mock"}
		d, _, timers := newTestDispatcher(sender)
		d.HostName = func(string) (string, int) { return "Mac", 2 }
		for i, host := range []string{"mac", "linux", "mac", "linux"} {
			d.OnAgentUpdate(nil, onHost(host, blockedState(fmt.Sprintf("w1:p%d", i), fmt.Sprintf("a%d", i))))
		}
		flushWindow(d, timers)
		got := sender.getMessages()
		digest := got[len(got)-1]
		if digest.Event != EventDigest || digest.Host != "" || digest.HostName != "" || digest.Title != "3 agents need you" {
			t.Fatalf("last push = %+v, want a digest without host", digest)
		}
	})
}

// Presence is one state for every host: any host's report holds back every
// host's pushes, and only the host that sent the last report ends it by going
// offline.
func TestPresence_OneStateAcrossHosts(t *testing.T) {
	d, _, timers, sender, states := presenceDispatcher(t)
	var asked []string
	d.Current = func(host, pane string) (model.AgentState, bool) {
		asked = append(asked, host+"/"+pane)
		s, ok := states[host+"/"+pane]
		return s, ok
	}
	blockOn := func(host, pane string, seq uint64) {
		cur := onHost(host, promptState(seq, fmt.Sprintf("fp%d", seq)))
		cur.PaneID = pane
		states[host+"/"+pane] = cur
		d.OnAgentUpdate(nil, cur)
	}

	d.OnHostPresence("mac", 30*time.Second, false)
	blockOn("linux", "w1:p1", 10) // another host's prompt: held back too
	if n := pushed(d, sender); n != 0 {
		t.Fatalf("pushes while the owner is at mac = %d, want 0", n)
	}

	d.OnHostOffline("linux") // did not send the report: presence stays
	blockOn("mac", "w1:p1", 11)
	if n := pushed(d, sender); n != 0 {
		t.Fatalf("pushes after another host went offline = %d, want 0", n)
	}
	d.mu.Lock()
	quiet := len(d.presence.quiet)
	d.mu.Unlock()
	if quiet != 2 {
		t.Fatalf("held-back panes = %d, want 2 (same pane id on two hosts)", quiet)
	}

	// The reporting host goes offline: presence ends without a catch-up.
	d.OnHostOffline("mac")
	if n := pushed(d, sender); n != 0 {
		t.Fatalf("catch-up on offline = %d pushes, want 0", n)
	}

	// The next report, from any host, says the owner is away: both held-back
	// panes are caught up, read from their own host.
	d.OnHostPresence("mac", 20*time.Minute, false)
	flushWindow(d, timers)
	sort.Strings(asked)
	if want := []string{"linux/w1:p1", "mac/w1:p1"}; fmt.Sprint(asked) != fmt.Sprint(want) {
		t.Fatalf("Current asked for %v, want %v", asked, want)
	}
	if got, want := hostEvents(sender.getMessages()), []string{"blocked:linux/w1:p1", "blocked:mac/w1:p1"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("catch-up pushes = %v, want %v", got, want)
	}
}

// A report from a second host takes over: the first host going offline no
// longer ends presence.
func TestPresence_LastReporterOwnsIt(t *testing.T) {
	d, _, _, sender, states := presenceDispatcher(t)
	d.OnHostPresence("mac", time.Second, false)
	d.OnHostPresence("mac2", time.Second, false)
	d.OnHostOffline("mac")

	block(d, states, "w1:p1", 10)
	if n := pushed(d, sender); n != 0 {
		t.Fatalf("pushes = %d, want 0: mac2 still reports the owner present", n)
	}
	d.OnHostOffline("mac2")
	block(d, states, "w1:p2", 20)
	if n := pushed(d, sender); n != 1 {
		t.Fatalf("pushes after the reporting host left = %d, want 1", n)
	}
}
