import { hydratePartialDiff, type FileDiffLoadedFiles, type FileDiffMetadata } from "@pierre/diffs";

/** Unchanged lines requested around each change in review patches (git's `--unified`). */
export const REVIEW_CONTEXT_LINES = 3;

/**
 * A diff parsed from a patch can't tell whether the file continues after its
 * last hunk, so Pierre always ends it with a "More unchanged context may be
 * available" row that loads the full file. git writes up to
 * REVIEW_CONTEXT_LINES unchanged lines after the last change, so fewer than
 * that (or a "No newline at end of file" marker) proves the file ends there
 * and the row could only ever expand to nothing.
 */
export function endsAtLastHunk(fileDiff: FileDiffMetadata): boolean {
	if (!fileDiff.isPartial || (fileDiff.type !== "change" && fileDiff.type !== "rename-changed")) return false;
	const lastHunk = fileDiff.hunks[fileDiff.hunks.length - 1];
	if (!lastHunk) return false;
	if (lastHunk.noEOFCRAdditions || lastHunk.noEOFCRDeletions) return true;
	const tail = lastHunk.hunkContent[lastHunk.hunkContent.length - 1];
	return tail?.type === "change" || (tail?.type === "context" && tail.lines < REVIEW_CONTEXT_LINES);
}

// 32-bit FNV-1a over the given strings (each terminated so ["ab"] ≠ ["a", "b"]).
function hashLines(lines: readonly string[]): number {
	let hash = 0x811c9dc5;
	for (const line of lines) {
		for (let index = 0; index < line.length; index++) hash = Math.imul(hash ^ line.charCodeAt(index), 0x01000193);
		hash = Math.imul(hash ^ 0x0a, 0x01000193);
	}
	return hash >>> 0;
}

const identities = new WeakMap<FileDiffMetadata, string>();

/**
 * Identity of a file's patch content: hunk positions plus a hash of every
 * patch line. Parsing a new workspace version yields new metadata objects, so
 * this is what tells "same diff" apart from "changed diff".
 */
export function patchIdentity(fileDiff: FileDiffMetadata): string {
	const cached = identities.get(fileDiff);
	if (cached) return cached;
	const hunks = fileDiff.hunks.map((hunk) => `${hunk.deletionStart},${hunk.deletionCount},${hunk.additionStart},${hunk.additionCount}`).join(";");
	const lines = hashLines([...fileDiff.deletionLines, "\u0000", ...fileDiff.additionLines]);
	const identity = `${fileDiff.type}|${fileDiff.prevName ?? ""}|${hunks}|${lines.toString(36)}`;
	identities.set(fileDiff, identity);
	return identity;
}

/**
 * A number that changes whenever the diff's content, or its patch-only vs
 * full-contents state, changes. CodeView only re-reads an item when its
 * version changes, so this is what lets updated or hydrated diffs through
 * while unchanged ones (new objects, same content) are left alone.
 */
export function diffContentVersion(fileDiff: FileDiffMetadata): number {
	return hashLines([fileDiff.isPartial ? "patch" : "full", patchIdentity(fileDiff)]);
}

const hydratedCopies = new WeakMap<FileDiffMetadata, { files: FileDiffLoadedFiles; diff: FileDiffMetadata | null }>();

// Pierre 1.4.1's virtualizer asserts the rendered diff is the same object it
// measured. Two objects that share a cacheKey (a reparse, or a second hydration
// of the same patch) fail that check and the row is replaced with the error.
//
// The keys embed the workspace version, so every new version adds a full set
// of diffs. Entries a mounted view renders are pinned (retainFileDiffs) and
// dropped as soon as no view renders them any more: the view moved to a newer
// version or was closed. Everything unpinned, such as a diff only the
// single-file view touched, is a bounded LRU.
type CachedDiff = { diff: FileDiffMetadata; bytes: number; touchedAt: number };
const diffsByCacheKey = new Map<string, CachedDiff>();
const holdersByKey = new Map<string, number>();
const keysByOwner = new Map<object, Set<string>>();
let unpinnedCount = 0;
let unpinnedBytes = 0;

/** Unpinned diffs kept for reuse; pinned ones are bounded by what is mounted. */
export const MAX_UNPINNED_FILE_DIFFS = 200;
export const MAX_UNPINNED_FILE_DIFF_BYTES = 16 * 1024 * 1024;
// A view pins what it rendered after it commits, so a diff touched this
// recently may be on screen without its pin yet and is never trimmed.
export const UNPINNED_FILE_DIFF_GRACE_MS = 2_000;

// The diff's line text as UTF-16, which dominates its size.
function diffBytes(fileDiff: FileDiffMetadata): number {
	let chars = 0;
	for (const line of fileDiff.deletionLines) chars += line.length;
	for (const line of fileDiff.additionLines) chars += line.length;
	return chars * 2;
}

function pinned(key: string) {
	return (holdersByKey.get(key) ?? 0) > 0;
}

function storeDiff(key: string, diff: FileDiffMetadata) {
	const entry = { diff, bytes: diffBytes(diff), touchedAt: performance.now() };
	diffsByCacheKey.set(key, entry);
	if (!pinned(key)) {
		unpinnedCount++;
		unpinnedBytes += entry.bytes;
		trimUnpinned();
	}
}

function dropDiff(key: string) {
	const entry = diffsByCacheKey.get(key);
	if (!entry) return;
	diffsByCacheKey.delete(key);
	if (!pinned(key)) {
		unpinnedCount--;
		unpinnedBytes -= entry.bytes;
	}
}

// Map order is recency (a hit re-inserts), so the oldest unpinned go first,
// and the first one inside the grace period ends the pass.
function trimUnpinned() {
	const graceStart = performance.now() - UNPINNED_FILE_DIFF_GRACE_MS;
	for (const [key, entry] of diffsByCacheKey) {
		if (unpinnedCount <= MAX_UNPINNED_FILE_DIFFS && unpinnedBytes <= MAX_UNPINNED_FILE_DIFF_BYTES) return;
		if (entry.touchedAt > graceStart) return;
		if (!pinned(key)) dropDiff(key);
	}
}

export function stableFileDiff(fileDiff: FileDiffMetadata): FileDiffMetadata {
	const key = fileDiff.cacheKey;
	if (key == null) return fileDiff;
	const existing = diffsByCacheKey.get(key);
	if (existing == null) {
		storeDiff(key, fileDiff);
		return fileDiff;
	}
	if (existing.diff === fileDiff || (existing.diff.isPartial === fileDiff.isPartial && patchIdentity(existing.diff) === patchIdentity(fileDiff))) {
		existing.touchedAt = performance.now();
		diffsByCacheKey.delete(key);
		diffsByCacheKey.set(key, existing);
		return existing.diff;
	}
	fileDiff.cacheKey = `${key}\u0000${patchIdentity(fileDiff)}`;
	dropDiff(fileDiff.cacheKey);
	storeDiff(fileDiff.cacheKey, fileDiff);
	return fileDiff;
}

/**
 * Pin the diffs a mounted view renders (by their final cacheKey) in place of
 * the set it pinned before. Diffs it no longer renders, and no other view
 * does, are dropped at once: their workspace version is gone from screen.
 */
export function retainFileDiffs(owner: object, keys: Iterable<string>): void {
	const next = new Set(keys);
	const previous = keysByOwner.get(owner) ?? new Set<string>();
	for (const key of next) {
		if (previous.has(key)) continue;
		const entry = diffsByCacheKey.get(key);
		if (entry && !pinned(key)) {
			unpinnedCount--;
			unpinnedBytes -= entry.bytes;
		}
		holdersByKey.set(key, (holdersByKey.get(key) ?? 0) + 1);
	}
	for (const key of previous) {
		if (next.has(key)) continue;
		const holders = (holdersByKey.get(key) ?? 1) - 1;
		if (holders > 0) {
			holdersByKey.set(key, holders);
			continue;
		}
		holdersByKey.delete(key);
		// Not pinned any more, so dropDiff leaves the unpinned totals alone.
		diffsByCacheKey.delete(key);
	}
	if (next.size > 0) keysByOwner.set(owner, next);
	else keysByOwner.delete(owner);
	trimUnpinned();
}

/** Unpin everything a view pinned: the view closed (or its session did). */
export function releaseFileDiffs(owner: object): void {
	retainFileDiffs(owner, []);
}

/** How much the shared diff cache holds, for tests and diagnostics. */
export function fileDiffCacheStats(): { entries: number; pinned: number; unpinnedBytes: number } {
	return { entries: diffsByCacheKey.size, pinned: diffsByCacheKey.size - unpinnedCount, unpinnedBytes };
}

/**
 * A full-content copy of a partial diff (the parsed patch itself is left
 * untouched), or null when the fetched contents don't fit the patch. Memoized
 * per partial diff + contents so re-renders hand CodeView the same object.
 */
export function hydratedCopy(fileDiff: FileDiffMetadata, files: FileDiffLoadedFiles): FileDiffMetadata | null {
	const cached = hydratedCopies.get(fileDiff);
	if (cached?.files === files) return cached.diff;
	let diff: FileDiffMetadata | null = null;
	try {
		if (fileDiff.isPartial) diff = hydratePartialDiff("clone", fileDiff, files);
	} catch {
		diff = null;
	}
	if (diff) diff = stableFileDiff(diff);
	hydratedCopies.set(fileDiff, { files, diff });
	return diff;
}
