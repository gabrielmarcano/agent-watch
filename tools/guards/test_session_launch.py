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


if __name__ == "__main__":
    unittest.main()
