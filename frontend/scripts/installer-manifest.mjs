// Writes installer-manifest.json: sha256 of every file scripts/release-local
// runs or sources. It ships as an extraResource, so the app signature covers it,
// and the daemon (backend/internal/releaseinstall) refuses to run an installer
// tree that does not match it.
import { createHash } from "node:crypto";
import { readFileSync, readdirSync, writeFileSync, statSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

export const INSTALLER_DIRS = ["scripts/release-local.d", "scripts/lib"];
export const INSTALLER_FILES = ["scripts/release-local", "scripts/release-local-publish.sh", "scripts/bundled-helpers.txt", "backend/internal/brand/brand.go"];

export function buildInstallerManifest(repoRoot) {
	const rels = [...INSTALLER_FILES];
	for (const dir of INSTALLER_DIRS) {
		for (const name of readdirSync(path.join(repoRoot, dir)).sort()) {
			if (statSync(path.join(repoRoot, dir, name)).isFile()) rels.push(`${dir}/${name}`);
		}
	}
	const files = {};
	for (const rel of rels) files[rel] = createHash("sha256").update(readFileSync(path.join(repoRoot, rel))).digest("hex");
	return { dirs: INSTALLER_DIRS, files };
}

export function writeInstallerManifest(repoRoot, out) {
	writeFileSync(out, JSON.stringify(buildInstallerManifest(repoRoot), null, 2) + "\n");
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
	writeInstallerManifest(path.resolve(process.argv[2] ?? ".."), path.resolve(process.argv[3] ?? "installer-manifest.json"));
}
