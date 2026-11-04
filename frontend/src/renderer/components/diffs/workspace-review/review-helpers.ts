/** Patch parsing, prefetch and viewed-file helpers for the workspace review pane. */
import { useCallback, useEffect, useMemo, useState } from "react";
import { parsePatchFiles, type FileDiffMetadata } from "@pierre/diffs";
import { type WorkspaceDiffScope, type WorkspaceFileSummary } from "../../../hooks/workspace-files/workspace-files-types";

export const parsedPatchCache = new Map<string, { patch: string; files: FileDiffMetadata[] }>();
export const MAX_PARSED_GROUPS = 24;
// Height of the custom per-file header row below (h-9).
export const FILE_HEADER_HEIGHT_PX = 36;
// Keeps an item's version (a 32-bit content hash, two flag bits, then this) a safe integer.
export const ANNOTATION_REVISION_SPAN = 65_536;
export const REVIEW_CODE_VIEW_LAYOUT = { gap: 4, paddingBottom: 8, paddingTop: 0 };
export const REVIEW_CODE_VIEW_METRICS = { diffHeaderHeight: FILE_HEADER_HEIGHT_PX };
export const REVIEW_CODE_VIEW_THEME = { dark: "github-dark", light: "github-light" } as const;
// Files whose patch proves they end at the last hunk get their full contents up
// front, so their diff has no dead "More unchanged context may be available"
// row. Bounded so a big review doesn't fetch every file; beyond these limits the
// row stays and still loads on click.
export const MAX_END_OF_FILE_PREFETCHES = 40;
export const END_OF_FILE_PREFETCH_MAX_BYTES = 128 * 1024;
// Keep speculative reads serial within a host/session, without allowing one
// stalled host to block other reviews. Every active pair has its own deadline.
export const FULL_CONTEXT_PREFETCH_TIMEOUT_MS = 30_000;
export const fullContextPrefetchTails = new Map<string, Promise<void>>();
export function prefetchFullContext<T>(key: string, load: (signal: AbortSignal) => Promise<T>, signal: AbortSignal): Promise<T> {
	const previous = fullContextPrefetchTails.get(key) ?? Promise.resolve();
	let release = () => {};
	const tail = new Promise<void>((resolve) => { release = resolve; });
	fullContextPrefetchTails.set(key, tail);
	return previous.then(async () => {
		signal.throwIfAborted();
		const controller = new AbortController();
		const onAbort = () => controller.abort(signal.reason);
		signal.addEventListener("abort", onAbort, { once: true });
		const timer = setTimeout(() => controller.abort(new DOMException("Diff context loading timed out", "TimeoutError")), FULL_CONTEXT_PREFETCH_TIMEOUT_MS);
		let onCancelled = () => {};
		const cancelled = new Promise<never>((_resolve, reject) => {
			onCancelled = () => reject(controller.signal.reason);
			controller.signal.addEventListener("abort", onCancelled, { once: true });
		});
		try {
			return await Promise.race([load(controller.signal), cancelled]);
		} finally {
			clearTimeout(timer);
			// Stop a sibling request if the other side failed first.
			controller.abort();
			signal.removeEventListener("abort", onAbort);
			controller.signal.removeEventListener("abort", onCancelled);
		}
	}).finally(() => {
		release();
		if (fullContextPrefetchTails.get(key) === tail) fullContextPrefetchTails.delete(key);
	});
}

export function chunked<T>(items: readonly T[], size: number): T[][] {
	const chunks: T[][] = [];
	for (let index = 0; index < items.length; index += size) chunks.push(items.slice(index, index + size));
	return chunks;
}

export function patchCacheKey(workspaceVersion: string | undefined, scope: WorkspaceDiffScope, selectionKey: string, repository: string | undefined, patch: string) {
	return `${workspaceVersion ?? "legacy"}:${scope}:${selectionKey || "working"}:${repository ?? "root"}:${patch.length}:${patch.slice(0, 80)}:${patch.slice(-80)}`;
}

export function parseGroupPatch(workspaceVersion: string | undefined, scope: WorkspaceDiffScope, selectionKey: string, repository: string | undefined, patch: string) {
	const key = patchCacheKey(workspaceVersion, scope, selectionKey, repository, patch);
	const cached = parsedPatchCache.get(key);
	// The bounded key is only a lookup hint: same-sized edits can preserve
	// both outer slices and the metadata-based workspace version.
	if (cached?.patch === patch) return cached.files;
	const prefix = repository ? `${repository}/` : "";
	const files = parsePatchFiles(patch, key, true).flatMap((entry) => entry.files);
	for (const file of files) {
		if (prefix && !file.name.startsWith(prefix)) file.name = prefix + file.name;
		if (prefix && file.prevName && !file.prevName.startsWith(prefix)) file.prevName = prefix + file.prevName;
	}
	parsedPatchCache.set(key, { patch, files });
	if (parsedPatchCache.size > MAX_PARSED_GROUPS) {
		const oldest = parsedPatchCache.keys().next().value;
		if (oldest) parsedPatchCache.delete(oldest);
	}
	return files;
}

export function isDeferredByDefault(file: WorkspaceFileSummary) {
	if (file.binary) return false;
	const name = file.path.split("/").pop()?.toLowerCase() ?? "";
	return file.size > 512 * 1024 || /^(package-lock\.json|pnpm-lock\.yaml|yarn\.lock|bun\.lockb?|go\.sum|cargo\.lock)$/.test(name);
}



export type ViewedRecord = Record<string, string>;

export function viewedStorageKey(sessionId: string, selectionKey: string) {
	return `ao.files.viewed.${sessionId}.${selectionKey}`;
}

export function readViewedRecords(key: string): ViewedRecord {
	try {
		return JSON.parse(window.localStorage.getItem(key) ?? "{}") as ViewedRecord;
	} catch {
		return {};
	}
}

export function isViewedRecord(file: WorkspaceFileSummary, records: ViewedRecord) {
	return records[file.path] === (file.fileFingerprint ?? "legacy");
}

export function useViewedFiles(sessionId: string, selectionKey: string, files: readonly WorkspaceFileSummary[]) {
	const key = viewedStorageKey(sessionId, selectionKey);
	const [records, setRecords] = useState<ViewedRecord>(() => readViewedRecords(key));
	useEffect(() => {
		setRecords(readViewedRecords(key));
	}, [key]);
	const viewed = useMemo(
		() => new Set(files.filter((file) => isViewedRecord(file, records)).map((file) => file.path)),
		[files, records],
	);
	const toggle = useCallback(
		(file: WorkspaceFileSummary) => {
			setRecords((current) => {
				const next = { ...current };
				if (next[file.path] === (file.fileFingerprint ?? "legacy")) delete next[file.path];
				else next[file.path] = file.fileFingerprint ?? "legacy";
				window.localStorage.setItem(key, JSON.stringify(next));
				return next;
			});
		},
		[key],
	);
	return { viewed, toggle };
}
