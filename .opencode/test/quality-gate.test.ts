// Exercises the quality-gate plugin with a recording fake of Bun's `$`:
// node --experimental-strip-types .opencode/test/quality-gate.test.ts
import assert from "node:assert/strict";
import { mkdtempSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { QualityGate } from "../plugins/quality-gate.ts";

const calls: string[] = [];
let failTests = false;
const fake$ = (strings: TemplateStringsArray, ...vals: unknown[]) => {
  const cmd = String(vals[0] ?? strings.join(""));
  calls.push(cmd);
  const bad = failTests && cmd.includes("make test");
  const res = { exitCode: bad ? 1 : 0, stdout: Buffer.from(bad ? "--- FAIL: TestX" : "ok"), stderr: Buffer.from("") };
  const chain = {
    cwd: () => chain,
    quiet: () => chain,
    nothrow: () => chain,
    then: (f: (r: typeof res) => unknown, g?: (e: unknown) => unknown) => Promise.resolve(res).then(f, g),
  };
  return chain;
};

const dir = mkdtempSync(join(tmpdir(), "gate-"));
const hooks: any = await QualityGate({ $: fake$, directory: dir } as any);

// 1. editing a Go file: gofmt + vet + golangci-lint on its package, reported to the agent
const out1 = { output: "edited" };
await hooks["tool.execute.before"]({ tool: "edit", callID: "1" }, { args: { filePath: `${dir}/server/internal/race/engine.go` } });
await hooks["tool.execute.after"]({ tool: "edit", callID: "1" }, out1);
assert.ok(calls.some((c) => c.includes("gofmt -w") && c.includes("internal/race/engine.go")), "gofmt on the edited file");
assert.ok(calls.some((c) => c.includes("go vet ./internal/race/") && c.includes("golangci-lint run ./internal/race/")), "vet + lint");
assert.match(out1.output, /quality-gate: gofmt ✓/);

// 2. editing a TS file: prettier + eslint + tsc
await hooks["tool.execute.before"]({ tool: "write", callID: "2" }, { args: { filePath: `${dir}/web/src/app.ts` } });
await hooks["tool.execute.after"]({ tool: "write", callID: "2" }, { output: "" });
assert.ok(calls.some((c) => c.includes("prettier --write") && c.includes("src/app.ts")));
assert.ok(calls.some((c) => c.includes("eslint") && c.includes("tsc --noEmit")));

// 3. session idle: tests of the touched areas, verdict recorded
failTests = true;
await hooks.event({ event: { type: "session.idle" } });
assert.ok(calls.some((c) => c.includes("make test-go")) && calls.some((c) => c.includes("make test-web")));
assert.equal(JSON.parse(readFileSync(join(dir, ".opencode/gate.json"), "utf8")).ok, false);

// 4. commit refused while red
await assert.rejects(
  hooks["tool.execute.before"]({ tool: "bash", callID: "3" }, { args: { command: "git commit -m wip" } }),
  /tests are red/,
);

// 5. green again: commit allowed
failTests = false;
await hooks.event({ event: { type: "session.idle" } });
await hooks["tool.execute.before"]({ tool: "bash", callID: "4" }, { args: { command: "git commit -m ok" } });

// 6. docs edits trigger nothing
const n = calls.length;
await hooks["tool.execute.before"]({ tool: "edit", callID: "5" }, { args: { filePath: `${dir}/docs/MODELS.md` } });
await hooks["tool.execute.after"]({ tool: "edit", callID: "5" }, { output: "" });
assert.equal(calls.length, n);
console.log("quality-gate plugin: 6/6 checks passed");
