// Path and file helpers for Browser panel downloads. They never decide
// whether a download is allowed; browser-download-manager.ts does.
import { copyFileSync, existsSync, renameSync, unlinkSync } from "node:fs";
import path from "node:path";

// Moves a finished staged file to its final place. Downloads can be on another
// volume than the app data dir, where rename fails with EXDEV.
export function moveFile(from: string, to: string): void {
	try {
		renameSync(from, to);
	} catch (error) {
		if ((error as NodeJS.ErrnoException).code !== "EXDEV") throw error;
		copyFileSync(from, to);
		unlinkSync(from);
	}
}

export function safeFilename(value: string): string {
	const fileName = path.basename(value.trim()).replace(/[. ]+$/u, "");
	return fileName && fileName !== "." && fileName !== ".." ? fileName : "download";
}

export function collisionSafePath(directory: string, fileName: string, unavailable: Set<string>): string {
	const parsed = path.parse(fileName);
	let candidate = path.join(directory, fileName);
	let suffix = 1;
	while (existsSync(candidate) || unavailable.has(candidate.toLowerCase())) {
		candidate = path.join(directory, `${parsed.name} (${suffix})${parsed.ext}`);
		suffix += 1;
	}
	return candidate;
}

export function isInsideDirectory(directory: string, candidate: string): boolean {
	const relative = path.relative(path.resolve(directory), path.resolve(candidate));
	return relative !== "" && relative !== ".." && !relative.startsWith(`..${path.sep}`) && !path.isAbsolute(relative);
}
