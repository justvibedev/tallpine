#!/usr/bin/env node
// Typecheck only the frontend TypeScript projects a change can affect: the
// projects that include a changed file, plus every project that references
// them (directly or transitively), since a changed declaration can break a
// dependant. `tsc -b` then builds those projects; their upstream references are
// built too, or reused from node_modules/.cache/tsc when up to date.
//
// Changed files = committed since the merge base with the base ref (default
// origin/main) + staged + unstaged + untracked. Use `--base <ref>` to compare
// against something else, or `--dry-run` to print the plan without building.
//
// A change to a tsconfig, package.json or the lockfile builds everything, and so
// does a deleted or renamed-away source file: it is in no project's file list
// any more, yet whatever imported it may now fail.
import { execFileSync, spawnSync } from "node:child_process";
import { existsSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import ts from "typescript";

const frontendDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const solutionPath = path.join(frontendDir, "tsconfig.json");
const e2eConfig = path.join(frontendDir, "tsconfig.e2e.json");

const args = process.argv.slice(2);
const flag = (name) => args.includes(name);
const option = (name, fallback) => {
	const i = args.indexOf(name);
	return i >= 0 && args[i + 1] ? args[i + 1] : fallback;
};
const base = option("--base", "origin/main");
const tscBin = path.join(frontendDir, "node_modules", ".bin", "tsc");
const dryRun = flag("--dry-run");

const git = (...gitArgs) => execFileSync("git", gitArgs, { cwd: frontendDir, encoding: "utf8" });
const repoRoot = git("rev-parse", "--show-toplevel").trim();
const lines = (text) => text.split("\n").filter(Boolean);

function changedFiles() {
	let mergeBase;
	try {
		mergeBase = git("merge-base", base, "HEAD").trim();
	} catch {
		fail(`no merge base with ${base}; fetch it (git fetch origin main) or pass --base <ref>`);
	}
	const files = new Set([
		// --no-renames lists a rename's old path too, as a deletion.
		...lines(git("diff", "--name-only", "--no-renames", mergeBase, "HEAD")),
		...lines(git("diff", "--name-only", "--no-renames", "HEAD")),
		...lines(git("ls-files", "--others", "--exclude-standard")),
	]);
	return [...files].map((file) => path.join(repoRoot, file));
}

function fail(message) {
	console.error(`typecheck:changed: ${message}`);
	process.exit(2);
}

function parseProject(configPath) {
	const parsed = ts.getParsedCommandLineOfConfigFile(configPath, {}, {
		...ts.sys,
		onUnRecoverableConfigFileDiagnostic: (d) => fail(ts.flattenDiagnosticMessageText(d.messageText, "\n")),
	});
	if (!parsed) fail(`cannot read ${configPath}`);
	return {
		path: configPath,
		files: new Set(parsed.fileNames.map((f) => path.resolve(f))),
		references: (parsed.projectReferences ?? []).map((ref) => ts.resolveProjectReferencePath(ref)),
	};
}

const solution = parseProject(solutionPath);
const projects = new Map(solution.references.map((ref) => [ref, parseProject(ref)]));
const rel = (p) => path.relative(frontendDir, p);

const changed = changedFiles();
const packagesDir = path.join(repoRoot, "packages");
const isGone = (file) =>
	/\.(tsx?|json)$/.test(file) &&
	(file.startsWith(frontendDir + path.sep) || file.startsWith(packagesDir + path.sep)) &&
	!existsSync(file);
const buildAll = changed.some((file) => {
	const name = path.basename(file);
	if (isGone(file)) return true;
	return path.dirname(file) === frontendDir && (/^tsconfig.*\.json$/.test(name) || name === "package.json" || name === "package-lock.json");
});

const owners = new Set();
let e2e = buildAll;
for (const file of changed) {
	for (const project of projects.values()) if (project.files.has(file)) owners.add(project.path);
	if (file.startsWith(path.join(frontendDir, "e2e") + path.sep)) e2e = true;
}

// Close over dependants: a project is affected if it references an affected one.
const affected = new Set(buildAll ? projects.keys() : owners);
for (let grew = true; grew; ) {
	grew = false;
	for (const project of projects.values()) {
		if (!affected.has(project.path) && project.references.some((ref) => affected.has(ref))) {
			affected.add(project.path);
			grew = true;
		}
	}
}
// The e2e specs import the preload bridge type (fake-bridge.ts `satisfies TallpineBridge`).
const preload = [...projects.keys()].find((p) => path.basename(p) === "tsconfig.preload.json");
if (preload && affected.has(preload)) e2e = true;

const targets = [...projects.keys()].filter((p) => affected.has(p));
console.log(`typecheck:changed: ${changed.length} changed file(s) vs ${base}`);
console.log(`  projects: ${targets.length ? targets.map(rel).join(", ") : "(none)"}${e2e ? " + tsconfig.e2e.json" : ""}`);
if (dryRun) process.exit(0);

let status = 0;
const run = (bin, runArgs) => {
	const result = spawnSync(bin, runArgs, { cwd: frontendDir, stdio: "inherit" });
	if (result.error) fail(`${bin}: ${result.error.message}`);
	status ||= result.status ?? 1;
};
if (targets.length) run(tscBin, ["-b", ...targets.map(rel)]);
if (e2e) run(tscBin, ["--noEmit", "-p", rel(e2eConfig)]);
process.exit(status);
