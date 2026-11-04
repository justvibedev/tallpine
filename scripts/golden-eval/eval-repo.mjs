// Builds an eval repo that contains the project only up to a replay base: no
// remote (a push cannot happen), no later commit or ref, and the objects of
// everything after the base pruned, so an agent cannot read the real solution
// from history.
import { spawnSync } from "node:child_process";

/** Runs a command and returns {code, stdout, stderr}; never throws. */
export function sh(cmd, args, opts = {}) {
	const r = spawnSync(cmd, args, { encoding: "utf8", maxBuffer: 1 << 28, ...opts });
	return { code: r.status ?? 1, stdout: r.stdout ?? "", stderr: r.stderr ?? "" };
}

const must = (r, what) => {
	if (r.code !== 0) throw new Error(`${what} failed: ${r.stderr.trim() || r.stdout.trim()}`);
	return r.stdout.trim();
};

/**
 * Clones `src` (a path or URL of the source repo, read only) into `dir` at
 * `base` on branch `branch`, then removes the remote, every other ref and the
 * objects only they reach. Returns the head sha.
 */
export function buildBaseRepo({ src, dir, base, branch = "main", run = sh }) {
	const git = (...a) => run("git", ["-C", dir, ...a]);
	must(run("git", ["clone", "-q", "--no-local", "--no-checkout", "--no-tags", src, dir]), "clone");
	must(git("checkout", "-q", "-B", branch, base), "checkout base");
	must(git("remote", "remove", "origin"), "remove origin");
	for (const ref of must(git("for-each-ref", "--format=%(refname)"), "list refs").split("\n").filter(Boolean)) {
		if (ref !== `refs/heads/${branch}`) must(git("update-ref", "-d", ref), `delete ${ref}`);
	}
	must(git("reflog", "expire", "--expire=now", "--all"), "reflog expire");
	must(git("gc", "-q", "--prune=now"), "gc");
	return must(git("rev-parse", "HEAD"), "rev-parse");
}

/**
 * Gives one run its own repo: a plain local clone of the item's pruned base
 * (`git clone` hardlinks the objects, so the runs of an item share them on
 * disk), with the remote removed. Branches and commits the run makes stay in
 * its own repo, so no run sees another's. Returns the head sha.
 */
export function cloneRunRepo({ base, dir, branch = "main", run = sh }) {
	must(run("git", ["clone", "-q", "--no-tags", "--branch", branch, base, dir]), "clone base");
	must(run("git", ["-C", dir, "remote", "remove", "origin"]), "remove origin");
	return must(run("git", ["-C", dir, "rev-parse", "HEAD"]), "rev-parse");
}

/** Throws unless dir has no remote, only `branch`, and HEAD at base. */
export function checkBaseRepo({ dir, base, branch = "main", run = sh }) {
	const git = (...a) => run("git", ["-C", dir, ...a]);
	if (must(git("remote"), "remote")) throw new Error(`${dir} has a remote: eval repos must not be able to push`);
	const refs = must(git("for-each-ref", "--format=%(refname)"), "refs").split("\n").filter(Boolean);
	if (refs.length !== 1 || refs[0] !== `refs/heads/${branch}`) throw new Error(`${dir} has refs besides ${branch}: ${refs.join(", ")}`);
	if (must(git("rev-parse", "HEAD"), "head") !== base) throw new Error(`${dir} is not at the replay base ${base}`);
	if (must(git("status", "--porcelain"), "status")) throw new Error(`${dir} has uncommitted changes`);
}
