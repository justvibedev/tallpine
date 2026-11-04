import { lstatSync, readdirSync, readlinkSync } from "node:fs";
import path from "node:path";

/** Symlinks under `root` whose target resolves outside `root` (absolute or via `..`). */
export function findEscapingSymlinks(root: string): string[] {
	const base = path.resolve(root);
	const bad: string[] = [];
	const walk = (dir: string): void => {
		for (const entry of readdirSync(dir)) {
			const full = path.join(dir, entry);
			const stat = lstatSync(full);
			if (stat.isSymbolicLink()) {
				const target = path.resolve(path.dirname(full), readlinkSync(full));
				const rel = path.relative(base, target);
				if (rel === ".." || rel.startsWith(`..${path.sep}`) || path.isAbsolute(rel)) {
					bad.push(`${path.relative(base, full)} -> ${readlinkSync(full)}`);
				}
			} else if (stat.isDirectory()) {
				walk(full);
			}
		}
	};
	walk(base);
	return bad;
}

export function assertNoEscapingSymlinks(root: string): void {
	const bad = findEscapingSymlinks(root);
	if (bad.length > 0) {
		throw new Error(`packaged bundle contains symlinks pointing outside ${root} (codesign --strict would fail):\n${bad.join("\n")}`);
	}
}
