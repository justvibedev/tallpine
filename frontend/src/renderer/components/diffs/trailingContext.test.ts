import { parsePatchFiles, type FileDiffMetadata } from "@pierre/diffs";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { endsAtLastHunk, hydratedCopy, patchIdentity, stableFileDiff } from "./trailingContext";

function parse(path: string, body: string): FileDiffMetadata {
	const patch = `diff --git a/${path} b/${path}\nindex 1111111..2222222 100644\n--- a/${path}\n+++ b/${path}\n${body}`;
	const [file] = parsePatchFiles(patch, `test:${path}:${body.length}`, true).flatMap((entry) => entry.files);
	if (!file) throw new Error("patch did not parse");
	return file;
}

const appendedAtEnd = "@@ -3,3 +3,5 @@\n line3\n line4\n line5\n+added6\n+added7\n";

describe("endsAtLastHunk", () => {
	it("is true when the last hunk ends on a change (no trailing context left)", () => {
		expect(endsAtLastHunk(parse("README.md", appendedAtEnd))).toBe(true);
	});

	it("is true when fewer than three unchanged lines follow the last change", () => {
		expect(endsAtLastHunk(parse("a.txt", "@@ -1,4 +1,4 @@\n line1\n-line2\n+LINE2\n line3\n line4\n"))).toBe(true);
	});

	it("is false when a full three lines of context follow, since the file may continue", () => {
		expect(endsAtLastHunk(parse("a.txt", "@@ -1,5 +1,5 @@\n line1\n-line2\n+LINE2\n line3\n line4\n line5\n"))).toBe(false);
	});

	it("is true when the patch marks the end of the file even after full context", () => {
		expect(endsAtLastHunk(parse("a.txt", "@@ -1,4 +1,4 @@\n-a\n+b\n c\n d\n e\n\\ No newline at end of file\n"))).toBe(true);
	});

	it("ignores new files, which never get the trailing row", () => {
		const patch = "diff --git a/NEW.md b/NEW.md\nnew file mode 100644\nindex 0000000..3333333\n--- /dev/null\n+++ b/NEW.md\n@@ -0,0 +1,2 @@\n+one\n+two\n";
		const [file] = parsePatchFiles(patch, "test:new", true).flatMap((entry) => entry.files);
		expect(file && endsAtLastHunk(file)).toBe(false);
	});
});

describe("patchIdentity", () => {
	it("matches for the same patch parsed twice and differs when a line changes", () => {
		const first = parse("README.md", appendedAtEnd);
		const again = parse("README.md", `${appendedAtEnd}`);
		const edited = parse("README.md", appendedAtEnd.replace("+added7", "+changed7"));
		expect(again).not.toBe(first);
		expect(patchIdentity(again)).toBe(patchIdentity(first));
		expect(patchIdentity(edited)).not.toBe(patchIdentity(first));
	});
});

describe("hydratedCopy", () => {
	it("returns a memoized full-content copy and leaves the parsed patch partial", () => {
		const partial = parse("README.md", appendedAtEnd);
		const oldContents = "line1\nline2\nline3\nline4\nline5\n";
		const files = {
			oldFile: { name: "README.md", contents: oldContents },
			newFile: { name: "README.md", contents: `${oldContents}added6\nadded7\n` },
		};
		const hydrated = hydratedCopy(partial, files);
		expect(hydrated).not.toBeNull();
		expect(hydrated?.isPartial).toBe(false);
		expect(hydrated?.additionLines).toHaveLength(7);
		expect(partial.isPartial).toBe(true);
		expect(hydratedCopy(partial, files)).toBe(hydrated);
	});

	it("reuses one object when the same patch is hydrated from a second parse", () => {
		const first = parse("NOTES.md", appendedAtEnd);
		const second = parse("NOTES.md", appendedAtEnd);
		const oldContents = "line1\nline2\nline3\nline4\nline5\n";
		const files = {
			oldFile: { name: "NOTES.md", contents: oldContents },
			newFile: { name: "NOTES.md", contents: `${oldContents}added6\nadded7\n` },
		};
		const again = {
			oldFile: { name: "NOTES.md", contents: oldContents },
			newFile: { name: "NOTES.md", contents: `${oldContents}added6\nadded7\n` },
		};
		expect(stableFileDiff(second)).toBe(stableFileDiff(first));
		expect(hydratedCopy(second, again)).toBe(hydratedCopy(first, files));
	});
});

// One workspace version of a review: `files` changed files, each parsed under
// a cacheKey that embeds the version, as the review pane parses them.
function workspaceVersion(version: number, files = 20, tag = "pane"): FileDiffMetadata[] {
	const patch = Array.from({ length: files }, (_, index) => {
		const body = Array.from({ length: 50 }, (_, line) => `+${tag} v${version} file ${index} line ${line}`).join("\n");
		return `diff --git a/f${index}.ts b/f${index}.ts\nindex 1111111..2222222 100644\n--- a/f${index}.ts\n+++ b/f${index}.ts\n@@ -0,0 +1,50 @@\n${body}\n`;
	}).join("");
	return parsePatchFiles(patch, `${tag}:v${version}`, true).flatMap((entry) => entry.files);
}

describe("shared diff cache", () => {
	// A fresh module per test: the cache is module state.
	let cache: typeof import("./trailingContext");
	beforeEach(async () => {
		vi.useFakeTimers({ toFake: ["performance"] });
		vi.resetModules();
		cache = await import("./trailingContext");
	});
	afterEach(() => {
		vi.useRealTimers();
	});

	// What the review pane does each time it renders a version: stabilize every
	// diff, then pin the keys it rendered.
	function renderVersion(owner: object, files: FileDiffMetadata[]): FileDiffMetadata[] {
		const rendered = files.map((file) => cache.stableFileDiff(file));
		cache.retainFileDiffs(owner, rendered.map((file) => file.cacheKey as string));
		return rendered;
	}

	it("holds only the version on screen as the workspace keeps changing", () => {
		const pane = {};
		for (let version = 0; version < 50; version++) renderVersion(pane, workspaceVersion(version));
		expect(cache.fileDiffCacheStats()).toMatchObject({ entries: 20, pinned: 20 });
		// A superseded version is really gone: an equal re-parse is new, not the old object.
		const reparsed = workspaceVersion(3)[0] as FileDiffMetadata;
		expect(cache.stableFileDiff(reparsed)).toBe(reparsed);
	});

	it("releases a pane's diffs when it closes", () => {
		const pane = {};
		renderVersion(pane, workspaceVersion(0));
		expect(cache.fileDiffCacheStats().pinned).toBe(20);
		cache.releaseFileDiffs(pane);
		expect(cache.fileDiffCacheStats()).toEqual({ entries: 0, pinned: 0, unpinnedBytes: 0 });
	});

	it("keeps a diff while any mounted pane still renders it", () => {
		const files = workspaceVersion(0, 5);
		const first = {};
		const second = {};
		renderVersion(first, files);
		renderVersion(second, files);
		cache.releaseFileDiffs(first);
		const reparsed = workspaceVersion(0, 5)[0] as FileDiffMetadata;
		expect(cache.stableFileDiff(reparsed)).toBe(files[0]);
	});

	it("hands a mounted pane back the object it rendered after a re-parse, however much else churns", () => {
		const pane = {};
		const [rendered] = renderVersion(pane, workspaceVersion(0, 1, "mounted"));
		for (let version = 0; version < cache.MAX_UNPINNED_FILE_DIFFS * 2; version++) {
			vi.advanceTimersByTime(cache.UNPINNED_FILE_DIFF_GRACE_MS + 1);
			cache.stableFileDiff(workspaceVersion(version, 1, "churn")[0] as FileDiffMetadata);
		}
		expect(cache.fileDiffCacheStats().entries).toBeLessThanOrEqual(cache.MAX_UNPINNED_FILE_DIFFS + 1);
		const reparsed = workspaceVersion(0, 1, "mounted")[0] as FileDiffMetadata;
		expect(cache.stableFileDiff(reparsed)).toBe(rendered);
	});

	it("bounds diffs no pane pins, sparing ones touched within the grace period", () => {
		const burst = cache.MAX_UNPINNED_FILE_DIFFS + 50;
		for (let version = 0; version < burst; version++) cache.stableFileDiff(workspaceVersion(version, 1, "burst")[0] as FileDiffMetadata);
		// All inside the grace period: a view may be about to pin them.
		expect(cache.fileDiffCacheStats().entries).toBe(burst);
		vi.advanceTimersByTime(cache.UNPINNED_FILE_DIFF_GRACE_MS + 1);
		cache.stableFileDiff(workspaceVersion(0, 1, "late")[0] as FileDiffMetadata);
		expect(cache.fileDiffCacheStats().entries).toBe(cache.MAX_UNPINNED_FILE_DIFFS);
	});

	it("bounds unpinned diffs by size as well as count", () => {
		const big = (version: number) => {
			const body = Array.from({ length: 2_000 }, (_, line) => `+v${version} ${line} ${"x".repeat(500)}`).join("\n");
			const patch = `diff --git a/big.ts b/big.ts\nindex 1111111..2222222 100644\n--- a/big.ts\n+++ b/big.ts\n@@ -0,0 +1,2000 @@\n${body}\n`;
			return parsePatchFiles(patch, `big:v${version}`, true).flatMap((entry) => entry.files)[0] as FileDiffMetadata;
		};
		for (let version = 0; version < 20; version++) {
			vi.advanceTimersByTime(cache.UNPINNED_FILE_DIFF_GRACE_MS + 1);
			cache.stableFileDiff(big(version));
		}
		expect(cache.fileDiffCacheStats().unpinnedBytes).toBeLessThanOrEqual(cache.MAX_UNPINNED_FILE_DIFF_BYTES);
		expect(cache.fileDiffCacheStats().entries).toBeLessThan(20);
	});
});
