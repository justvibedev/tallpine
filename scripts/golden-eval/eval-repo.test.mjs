import assert from "node:assert/strict";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import { buildBaseRepo, checkBaseRepo, cloneRunRepo, sh } from "./eval-repo.mjs";

function fixture(t) {
	const root = mkdtempSync(join(tmpdir(), "golden-repo-"));
	t.after(() => rmSync(root, { recursive: true, force: true }));
	const src = join(root, "src");
	const g = (...a) => {
		const r = sh("git", ["-C", src, "-c", "user.email=t@t", "-c", "user.name=t", ...a]);
		assert.equal(r.code, 0, r.stderr);
		return r.stdout.trim();
	};
	assert.equal(sh("git", ["init", "-q", "-b", "main", src]).code, 0);
	const shas = [];
	for (const n of ["one", "two", "three"]) {
		writeFileSync(join(src, `${n}.txt`), n);
		g("add", "-A");
		g("commit", "-q", "-m", n);
		shas.push(g("rev-parse", "HEAD"));
	}
	g("tag", "future");
	return { root, src, shas };
}

test("the eval repo ends at the base with no remote, no other ref and no later objects", (t) => {
	const { root, src, shas } = fixture(t);
	const dir = join(root, "eval");
	assert.equal(buildBaseRepo({ src, dir, base: shas[1] }), shas[1]);
	checkBaseRepo({ dir, base: shas[1] });
	assert.equal(sh("git", ["-C", dir, "remote"]).stdout.trim(), "");
	assert.notEqual(sh("git", ["-C", dir, "cat-file", "-e", shas[2]]).code, 0, "the later commit must be gone");
	assert.equal(sh("git", ["-C", dir, "cat-file", "-e", shas[0]]).code, 0, "history up to the base stays");
	assert.equal(sh("git", ["-C", dir, "rev-list", "--all", "--count"]).stdout.trim(), "2");
});

test("checkBaseRepo refuses a repo with a remote, another ref, or the wrong head", (t) => {
	const { root, src, shas } = fixture(t);
	const dir = join(root, "eval");
	buildBaseRepo({ src, dir, base: shas[1] });
	assert.throws(() => checkBaseRepo({ dir, base: shas[0] }), /not at the replay base/);
	sh("git", ["-C", dir, "branch", "other"]);
	assert.throws(() => checkBaseRepo({ dir, base: shas[1] }), /refs besides/);
	sh("git", ["-C", dir, "branch", "-D", "other"]);
	sh("git", ["-C", dir, "remote", "add", "origin", src]);
	assert.throws(() => checkBaseRepo({ dir, base: shas[1] }), /has a remote/);
});

test("a run clone has no remote and none of the later objects, and its branches stay its own", (t) => {
	const { root, src, shas } = fixture(t);
	const base = join(root, "base");
	buildBaseRepo({ src, dir: base, base: shas[1] });
	const a = join(root, "run-a");
	const b = join(root, "run-b");
	assert.equal(cloneRunRepo({ base, dir: a }), shas[1]);
	cloneRunRepo({ base, dir: b });
	checkBaseRepo({ dir: a, base: shas[1] });
	assert.equal(sh("git", ["-C", a, "remote"]).stdout.trim(), "");
	assert.notEqual(sh("git", ["-C", a, "cat-file", "-e", shas[2]]).code, 0, "the later commit is not in the run clone");
	sh("git", ["-C", a, "checkout", "-q", "-b", "tp/work"]);
	assert.equal(sh("git", ["-C", b, "branch", "--list", "tp/work"]).stdout.trim(), "", "another run's branch is invisible");
});
