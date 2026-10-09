// PIT WALL quality gate for OpenCode.
//
// - After every edit/write/patch: format the file, then lint + type-check the
//   area it belongs to (Go package, or the web project). The findings are
//   appended to the tool output, so the agent sees them immediately.
// - On session.idle: run the tests of every area touched in the session and
//   record the verdict in .opencode/gate.json.
// - Before `git commit`: refuse while the last gate is red, or if code was
//   edited since the last green gate ("tests before done").
import { mkdir, writeFile } from "node:fs/promises";
import type { Plugin } from "@opencode-ai/plugin";

type Area = "go" | "web";

export const QualityGate: Plugin = async ({ $, directory }) => {
  const pending = new Map<string, string>(); // callID -> edited file
  const dirty = new Set<Area>();
  let lastGate: { ok: boolean; at: number; summary: string } | null = null;

  const areaOf = (file: string): Area | null => {
    if (/(^|\/)server\/.+\.go$/.test(file)) return "go";
    if (/(^|\/)web\/(src|tests|e2e)\/.+\.(ts|css)$/.test(file) || /(^|\/)web\/[^/]+\.(ts|js|json)$/.test(file)) return "web";
    return null;
  };

  const run = async (cmd: string): Promise<{ ok: boolean; out: string }> => {
    const r = await $`bash -lc ${cmd}`.cwd(directory).quiet().nothrow();
    const out = (r.stdout.toString() + r.stderr.toString()).trim();
    return { ok: r.exitCode === 0, out: out.split("\n").slice(-40).join("\n") };
  };

  const checkFile = async (file: string): Promise<string> => {
    const area = areaOf(file);
    if (!area) return "";
    dirty.add(area);
    if (area === "go") {
      const pkg = file.replace(/^.*?server\//, "./").replace(/\/[^/]+$/, "/");
      await run(`cd server && gofmt -w ${JSON.stringify(file.replace(/^.*?server\//, ""))}`);
      const vet = await run(`cd server && go vet ${pkg} && golangci-lint run ${pkg}`);
      return vet.ok ? "quality-gate: gofmt ✓ vet ✓ golangci-lint ✓" : `quality-gate: Go issues\n${vet.out}`;
    }
    const rel = file.replace(/^.*?web\//, "");
    await run(`cd web && npx prettier --write ${JSON.stringify(rel)}`);
    const lint = await run(`cd web && npx eslint ${JSON.stringify(rel)} && npx tsc --noEmit`);
    return lint.ok ? "quality-gate: prettier ✓ eslint ✓ tsc ✓" : `quality-gate: web issues\n${lint.out}`;
  };

  const gate = async (): Promise<void> => {
    const parts: string[] = [];
    let ok = true;
    if (dirty.has("go")) {
      const r = await run("make test-go");
      ok &&= r.ok;
      parts.push(`go tests ${r.ok ? "✓" : "✗"}`);
    }
    if (dirty.has("web")) {
      const r = await run("make test-web");
      ok &&= r.ok;
      parts.push(`web tests ${r.ok ? "✓" : "✗"}`);
    }
    if (parts.length === 0) return;
    lastGate = { ok, at: Date.now(), summary: parts.join(" · ") };
    if (ok) dirty.clear();
    try {
      await mkdir(`${directory}/.opencode`, { recursive: true });
      await writeFile(`${directory}/.opencode/gate.json`, JSON.stringify(lastGate, null, 2));
    } catch {
      // the verdict file is informative only; the in-memory gate still applies
    }
  };

  return {
    "tool.execute.before": async (input, output) => {
      if (["edit", "write", "patch"].includes(input.tool)) {
        const f = (output.args as { filePath?: unknown }).filePath;
        if (typeof f === "string") pending.set(input.callID, f);
      }
      if (input.tool === "bash") {
        const cmd = String((output.args as { command?: unknown }).command ?? "");
        if (/\bgit\s+commit\b/.test(cmd)) {
          if (dirty.size > 0) await gate();
          if (lastGate && !lastGate.ok) {
            throw new Error(`quality-gate: tests are red (${lastGate.summary}). Fix them before committing — run /verify.`);
          }
        }
      }
    },
    "tool.execute.after": async (input, output) => {
      const f = pending.get(input.callID);
      if (!f) return;
      pending.delete(input.callID);
      const report = await checkFile(f);
      if (report) output.output = `${output.output ?? ""}\n\n${report}`;
    },
    event: async ({ event }) => {
      if (event.type === "session.idle") await gate();
    },
  };
};
