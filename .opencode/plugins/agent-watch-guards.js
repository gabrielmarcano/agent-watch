// Agent Watch safety guards for OpenCode.
// Auto-discovered from .opencode/plugins/. All logic lives in tools/guards/guards.py
// (shared with the Antigravity CLI hooks and the git pre-commit hook); this file only
// forwards each tool call to it and blocks the call when the guard says so.

import { spawnSync } from "node:child_process";
import path from "node:path";

// Single default export on purpose: OpenCode may register every export as a plugin.
export default async function AgentWatchGuards({ worktree, directory }) {
  const root = worktree || directory;
  const guard = path.join(root, "tools", "guards", "guards.py");

  const run = (mode, payload) =>
    spawnSync("python3", [guard, mode], {
      input: JSON.stringify(payload),
      encoding: "utf8",
      timeout: 15000,
      env: { ...process.env, AW_REPO_ROOT: root },
    });

  return {
    "tool.execute.before": async (input, output) => {
      const res = run("opencode-before", { tool: input.tool, args: output.args ?? {} });
      if (res.status === 2) {
        throw new Error((res.stderr || "BLOCKED by tools/guards/guards.py").trim());
      }
    },
    "tool.execute.after": async (input, output) => {
      // Keep Go files gofmt-formatted after edits (the pre-commit hook also checks it).
      const file = output?.args?.filePath ?? input?.args?.filePath;
      if (["edit", "write"].includes(input.tool) && typeof file === "string" && file.endsWith(".go")) {
        spawnSync("gofmt", ["-w", file], { timeout: 10000 });
      }
    },
  };
}
