// Agent Watch safety guards for OpenCode (V2 plugin API).
// Auto-discovered from .opencode/plugins/. All logic lives in tools/guards/guards.py
// (shared with the Antigravity CLI hooks and the git pre-commit hook); this file only
// forwards each tool call to it and blocks the call when the guard says so.

import { Plugin } from "@opencode/plugin";
import { spawnSync } from "node:child_process";
import path from "node:path";

export default Plugin.define({
  id: "agent-watch-guards",
  async setup(ctx) {
    const root =
      ctx.location.project?.canonical ?? ctx.location.directory;
    const guard = path.join(root, "tools", "guards", "guards.py");

    const run = (mode, payload) =>
      spawnSync("python3", [guard, mode], {
        input: JSON.stringify(payload),
        encoding: "utf8",
        timeout: 15000,
        env: { ...process.env, AW_REPO_ROOT: root },
      });

    await ctx.tool.hook("execute.before", (event) => {
      const res = run("opencode-before", {
        tool: event.tool,
        args: event.input ?? {},
      });
      if (res.status === 2) {
        throw new Error((res.stderr || "BLOCKED by tools/guards/guards.py").trim());
      }
    });

    await ctx.tool.hook("execute.after", (event) => {
      // Keep Go files gofmt-formatted after edits (the pre-commit hook also checks it).
      const input = event.input ?? {};
      const file =
        typeof input === "object" && input !== null
          ? (input.filePath ?? input.path ?? input.file_path)
          : undefined;
      if (
        (event.tool === "edit" || event.tool === "write") &&
        typeof file === "string" &&
        file.endsWith(".go")
      ) {
        spawnSync("gofmt", ["-w", file], { timeout: 10000 });
      }
    });
  },
});
