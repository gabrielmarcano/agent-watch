"""Unit tests for the session-launch exception: `herdr pane run` is allowed in a
pane of a tab labelled aw-session-*, with no agent and only a shell in the
foreground. herdr is faked; nothing talks to the live server."""
import importlib.util
import os
import sys
import unittest

sys.dont_write_bytecode = True  # keep tools/guards free of __pycache__

HERE = os.path.dirname(os.path.abspath(__file__))
spec = importlib.util.spec_from_file_location("guards", os.path.join(HERE, "guards.py"))
guards = importlib.util.module_from_spec(spec)
spec.loader.exec_module(guards)


def fake_herdr(tab_label="aw-session-ui", agent=None, fg=("zsh",), ws_label="agent-watch", resolvable=True):
    def herdr_json(args):
        if args[:2] == ["pane", "get"]:
            if not resolvable:
                return {"error": {"code": "pane_not_found"}}
            return {"result": {"pane": {"pane_id": args[2], "tab_id": "w1:t2", "workspace_id": "w1", "agent": agent}}}
        if args[:2] == ["agent", "get"]:
            return {"error": {"code": "agent_not_found"}}
        if args[:2] == ["tab", "list"]:
            return {"result": {"tabs": [{"tab_id": "w1:t1", "label": "1"}, {"tab_id": "w1:t2", "label": tab_label}]}}
        if args[:2] == ["pane", "process-info"]:
            return {"result": {"process_info": {"foreground_processes": [{"name": n} for n in fg]}}}
        if args[:2] == ["workspace", "list"]:
            return {"result": {"workspaces": [{"workspace_id": "w1", "label": ws_label}]}}
        return None
    return herdr_json


def run(tokens, **kw):
    guards.herdr_json = fake_herdr(**kw)
    guards.check_herdr(tokens)


class SessionLaunch(unittest.TestCase):
    RUN = ["herdr", "pane", "run", "w1:p2", "claude 'do the task'"]

    def test_fresh_session_tab_allows_pane_run(self):
        run(self.RUN)  # no Blocked

    def test_agent_already_there_is_blocked(self):
        with self.assertRaises(guards.Blocked):
            run(self.RUN, agent="claude")

    def test_non_shell_foreground_is_blocked(self):
        with self.assertRaises(guards.Blocked):
            run(self.RUN, fg=("node",))

    def test_unknown_foreground_fails_closed(self):
        with self.assertRaises(guards.Blocked):
            run(self.RUN, fg=())

    def test_other_tab_label_is_blocked(self):
        with self.assertRaises(guards.Blocked):
            run(self.RUN, tab_label="notes")

    def test_unresolvable_pane_is_blocked(self):
        with self.assertRaises(guards.Blocked):
            run(self.RUN, resolvable=False)

    def test_exception_covers_only_pane_run(self):
        for cmd in (["herdr", "agent", "prompt", "w1:p2", "hi"], ["herdr", "pane", "send-text", "w1:p2", "x"],
                    ["herdr", "agent", "send-keys", "w1:p2", "1"]):
            with self.assertRaises(guards.Blocked, msg=cmd):
                run(cmd)

    def test_sandbox_still_allowed(self):
        run(["herdr", "agent", "prompt", "w1:p2", "hi"], tab_label="notes", ws_label="aw-sandbox")


def fake_hc(labels=None, resolvable=True):
    def hc_json(args):
        if args[:1] == ["get"]:
            if not resolvable:
                return None
            return {"address": args[1], "labels": labels or {}}
        return None
    return hc_json


def run_hc(tokens, env=None, **kw):
    guards.hc_json = fake_hc(**kw)
    guards.check_hc(tokens, env or {})


class HcSandbox(unittest.TestCase):
    def test_sandbox_labeled_terminal_allowed(self):
        run_hc(["hc", "send", "t99", "hello"], labels={"aw": "sandbox"})
        run_hc(["hc", "keys", "t99", "1", "enter"], labels={"aw": "sandbox"})
        run_hc(["hc", "kill", "t99"], labels={"aw": "sandbox"})
        run_hc(["hc", "rm", "t99"], labels={"aw": "sandbox"})
        run_hc(["hc", "label", "t99", "foo=bar"], labels={"aw": "sandbox"})

    def test_owner_terminal_blocked(self):
        for cmd in (["hc", "send", "t2", "hi"], ["hc", "keys", "t2", "1"],
                    ["hc", "interrupt", "t2"], ["hc", "kill", "t2"],
                    ["hc", "rm", "t2"], ["hc", "keep", "t2", "--off"],
                    ["hc", "put", "t2", "a.png"], ["hc", "attach", "t2"]):
            with self.assertRaises(guards.Blocked, msg=cmd):
                run_hc(cmd, labels={"where": "agent-watch / 1"})

    def test_attach_observe_allowed_on_any_terminal(self):
        run_hc(["hc", "attach", "--observe", "t2"], labels={"where": "agent-watch / 1"})

    def test_setting_sandbox_label_on_existing_terminal_blocked(self):
        with self.assertRaises(guards.Blocked):
            run_hc(["hc", "label", "t2", "aw=sandbox"], labels={"aw": "sandbox"})

    def test_isolated_hc_session_allowed(self):
        run_hc(["hc", "send", "t2", "hi"], env={"HC_SESSION": "aw-sandbox"}, labels={})
        run_hc(["hc", "server", "stop"], env={"HC_SESSION": "aw-sandbox"}, labels={})


if __name__ == "__main__":
    unittest.main()
