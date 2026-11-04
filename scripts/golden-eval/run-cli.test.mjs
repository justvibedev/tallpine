import assert from "node:assert/strict";
import { chmodSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";
import { sh } from "./eval-repo.mjs";

// The CLI entry (run.mjs) as a subprocess: module wiring bugs (an import cycle
// with the entry's top-level await made --setup exit silently) are invisible to
// tests that import the modules directly.
const run = fileURLToPath(new URL("./run.mjs", import.meta.url));

function fixture(t) {
	const root = mkdtempSync(join(tmpdir(), "golden-cli-"));
	t.after(() => rmSync(root, { recursive: true, force: true }));
	const src = join(root, "src");
	assert.equal(sh("git", ["init", "-q", "-b", "main", src]).code, 0);
	const g = (...a) => sh("git", ["-C", src, "-c", "user.email=t@t", "-c", "user.name=t", ...a]);
	writeFileSync(join(src, "a.txt"), "a");
	g("add", "-A");
	g("commit", "-q", "-m", "one");
	const base = g("rev-parse", "HEAD").stdout.trim();
	writeFileSync(join(src, "b.txt"), "b");
	g("add", "-A");
	g("commit", "-q", "-m", "two");
	const golden = join(root, "golden.json");
	writeFileSync(golden, JSON.stringify({ items: [{ ref: "B1", kind: "single", baseCommit: base, title: "T", note: "n", doneWhen: "d", answers: [], decisions: "x" }] }));
	// A fake tallpine: logs its arguments; `project get` says the project is unknown.
	const bin = join(root, "bin");
	sh("mkdir", ["-p", bin]);
	const log = join(root, "calls.log");
	writeFileSync(join(bin, "tallpine"), `#!/bin/sh\necho "$@" >> "${log}"\n[ "$1 $2" = "project get" ] && exit 1\nexit 0\n`);
	chmodSync(join(bin, "tallpine"), 0o755);
	const gitDir = dirname(sh("which", ["git"]).stdout.trim());
	const env = { PATH: `${bin}:${gitDir}:${dirname(process.execPath)}`, HOME: root, TMPDIR: root };
	return { root, src, base, golden, log, env };
}

test("--setup builds the pruned repos and registers the eval projects through tallpine", (t) => {
	const { root, src, golden, log, env } = fixture(t);
	const evalRoot = join(root, "eval");
	const r = spawnSync(process.execPath, [run, "--setup", "--golden", golden, "--src", src, "--eval-root", evalRoot, "--worker-model", "w", "--planner-model", "p", "--arm", "in-place", "--rep", "1"], { encoding: "utf8", env });
	assert.equal(r.status, 0, r.stderr + r.stdout);
	assert.match(r.stdout, /registered 1 eval projects: eval-b1-in-place-r1/);
	const calls = readFileSync(log, "utf8");
	assert.match(calls, /project add --path .*B1-in-place-r1 --id eval-b1-in-place-r1/);
	assert.match(calls, /project set-config eval-b1-in-place-r1 --config-json .*"planInPlace":true/);
	assert.equal(sh("git", ["-C", join(evalRoot, "B1-in-place-r1"), "remote"]).stdout.trim(), "", "the run clone has no remote");
});

test("--live without --eval-root fails with a message, not silently", (t) => {
	const { golden, env } = fixture(t);
	const r = spawnSync(process.execPath, [run, "--live", "--golden", golden], { encoding: "utf8", env });
	assert.equal(r.status, 2);
	assert.match(r.stderr, /--eval-root/);
});
