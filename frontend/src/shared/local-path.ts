/**
 * Local filesystem paths in chat text: detection (renderer) and the IPC
 * contract (renderer <-> main). Detection is deliberately conservative: a
 * missed path costs nothing, a false positive turns prose into a dead link.
 * Whether a detected path actually exists is decided later, in main.
 */

export type LocalPathAction = "reveal" | "open" | "copy";

export type LocalPathInspection =
	| { ok: true; path: string; kind: "file" | "directory"; canOpen: boolean }
	| { ok: false; reason: "invalid" | "not_found" };

export type LocalPathResult =
	| { ok: true; performed: LocalPathAction }
	| { ok: false; reason: "invalid" | "not_found" | "failed" };

export type LocalPathMatch = { start: number; end: number; path: string };

const MAX_PATH_LENGTH = 4096;
const SEGMENT = "[\\p{L}\\p{N}_.@+%~=,#-]+";
// Starts at a boundary so `and/or`, `https://host/x` and `a/b/c` never match.
const PATH_IN_TEXT = new RegExp(`(?<=^|[\\s("'\`\\[{<=])(?:~|(?:/${SEGMENT})+)(?:/${SEGMENT})*/?`, "gu");
// A final segment with spaces ("Sample App-0.1.0.dmg") counts only when it ends in a file extension,
// so prose after a path is not swallowed. The plain token must have no extension, every extra word must start
// uppercase or with a digit (lowercase prose like "and update" stops it), and the extension needs a letter. At most 5 words.
const SPACED_FILE = new RegExp(`^(?:/${SEGMENT})+(?: [\\p{Lu}\\p{N}]${SEGMENT}?){1,4}\\.(?=[A-Za-z0-9]*[A-Za-z])[A-Za-z0-9]{1,8}(?![\\p{L}\\p{N}_])`, "u");
const TRAILING_PUNCTUATION = /[.,;:!?]+$/;
const LINE_SUFFIX = /:\d+(?::\d+)?$/;
const URL_SCHEME = /^[A-Za-z][A-Za-z0-9+.-]+:/;

/** True when `value` is a whole-string absolute (`/…`, two or more segments) or home (`~/…`) path. */
export function isLocalPathCandidate(value: string): boolean {
	if (value.length === 0 || value.length > MAX_PATH_LENGTH) return false;
	if (value !== value.trim() || /[\0\r\n\t]/.test(value) || URL_SCHEME.test(value)) return false;
	if (value.startsWith("~/")) return value.length > 2;
	if (!value.startsWith("/") || value.startsWith("//")) return false;
	const segments = value.split("/").filter(Boolean);
	return segments.length >= 2 && !segments.every(looksLikeDatePart);
}

/** Find path-shaped tokens in prose. A space is allowed only inside the final segment of a file path with an extension. */
export function findLocalPaths(text: string): LocalPathMatch[] {
	const matches: LocalPathMatch[] = [];
	let skipUntil = 0;
	for (const match of text.matchAll(PATH_IN_TEXT)) {
		let token = match[0];
		const start = match.index;
		if (start < skipUntil) continue;
		const spaced = SPACED_FILE.exec(text.slice(start))?.[0];
		if (spaced && !/\.[^/]*$/.test(token) && spaced.startsWith(token) && isLocalPathCandidate(spaced)) {
			matches.push({ start, end: start + spaced.length, path: spaced });
			skipUntil = start + spaced.length;
			continue;
		}
		if (token === "~" || token.startsWith("~") && !token.startsWith("~/")) continue;
		token = trimToken(token);
		if (!isLocalPathCandidate(token)) continue;
		matches.push({ start, end: start + token.length, path: token });
	}
	return matches;
}

function trimToken(token: string): string {
	let value = token;
	for (;;) {
		const next = value.replace(TRAILING_PUNCTUATION, "").replace(LINE_SUFFIX, "");
		if (next === value) return value;
		value = next;
	}
}

// 2024, 10, 25, 1 — purely numeric segments read as dates or ratios, not paths.
function looksLikeDatePart(segment: string): boolean {
	return /^\d{1,4}$/.test(segment);
}
