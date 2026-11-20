import type { DownloadItem, Session } from "electron";
import { randomUUID } from "node:crypto";
import { existsSync, lstatSync, mkdirSync, readFileSync, renameSync, rmSync, writeFileSync } from "node:fs";
import path from "node:path";
import { collisionSafePath, isInsideDirectory, moveFile, safeFilename } from "./browser-download-files";
import type {
	BrowserDownload,
	BrowserDownloadActionInput,
	BrowserDownloadsState,
} from "../shared/browser-downloads";

type DownloadItemLike = Pick<
	DownloadItem,
	| "cancel"
	| "getFilename"
	| "getURL"
	| "getURLChain"
	| "getReceivedBytes"
	| "getTotalBytes"
	| "isPaused"
	| "on"
	| "once"
	| "removeListener"
	| "pause"
	| "canResume"
	| "getETag"
	| "getLastModifiedTime"
	| "resume"
	| "setSavePath"
>;

type DownloadSessionLike = Pick<Session, "on" | "removeListener">;
type DownloadEventLike = { preventDefault: () => void };
type DoneState = "completed" | "cancelled" | "interrupted";

// Staging is swept once per app run, by the first manager that uses it.
const sweptStagingDirectories = new Set<string>();

// A blocked download holds Chromium's original request, paused, writing into
// Tallpine's private staging folder. Requesting the URL again cannot work for
// a revoked blob: link, a POST export, or a one-time link (B846), so allowing
// it resumes this same item and moves the file into Downloads when it is done.
type BlockedRequest = {
	url: string;
	session: DownloadSessionLike;
	// Only an identity: a repeat from the same tab is the same request.
	webContents?: unknown;
	item: DownloadItemLike;
	stagedPath: string;
	staged: "pending" | "completed";
	done: (event: unknown, state: "completed" | "cancelled" | "interrupted") => void;
};

type DownloadShell = {
	openPath: (filePath: string) => Promise<string>;
	showItemInFolder: (filePath: string) => void;
	trashItem: (filePath: string) => Promise<void>;
};

type StoredDownload = BrowserDownload & { savePath: string };

type BrowserDownloadManagerOptions = {
	downloadsDirectory: string;
	// Private folder under the app data dir. Unapproved bytes live only here.
	stagingDirectory: string;
	historyPath: string;
	shell: DownloadShell;
	notify: (state: BrowserDownloadsState) => void;
	now?: () => number;
	createId?: () => string;
};

const MAX_DOWNLOAD_HISTORY = 200;
// Blocked requests are capped on their own so a page or agent loop that keeps
// requesting files can never push real downloads out of the history.
const MAX_BLOCKED_DOWNLOADS = 20;
const DOWNLOAD_DESTINATION_ERROR = "Could not prepare the Downloads folder.";
const DOWNLOAD_DELETE_ERROR = "Could not delete the downloaded file.";
const DOWNLOAD_UNAVAILABLE_ERROR = "This download is no longer available. Open the link again.";
const DOWNLOAD_MOVE_ERROR = "Could not move the downloaded file into the Downloads folder.";

function requestedURL(item: DownloadItemLike): string {
	return item.getURLChain()[0] || item.getURL();
}

function downloadSource(url: string): string {
	try {
		const parsed = new URL(url);
		if (parsed.protocol === "blob:") return new URL(parsed.pathname).host;
		return parsed.host;
	} catch {
		return "";
	}
}

function publicDownload(download: StoredDownload): BrowserDownload {
	const { savePath: _savePath, ...safe } = download;
	return safe;
}

// Expects newest first, and keeps the newest of each kind.
function withinLimits(downloads: StoredDownload[]): StoredDownload[] {
	let blocked = 0;
	let history = 0;
	return downloads.filter((download) =>
		download.status === "blocked" ? ++blocked <= MAX_BLOCKED_DOWNLOADS : ++history <= MAX_DOWNLOAD_HISTORY);
}

function isStoredDownload(value: unknown): value is StoredDownload {
	if (!value || typeof value !== "object" || Array.isArray(value)) return false;
	const item = value as Partial<StoredDownload>;
	return (
		typeof item.id === "string" &&
		typeof item.fileName === "string" &&
		typeof item.savePath === "string" &&
		typeof item.receivedBytes === "number" &&
		typeof item.totalBytes === "number" &&
		typeof item.startedAt === "number" &&
		typeof item.updatedAt === "number" &&
		["progressing", "paused", "completed", "cancelled", "interrupted"].includes(item.status ?? "")
	);
}


export type BrowserDownloadManager = ReturnType<typeof createBrowserDownloadManager>;

export function createBrowserDownloadManager(options: BrowserDownloadManagerOptions) {
	const attachedSessions = new Map<
		DownloadSessionLike,
		(event: DownloadEventLike, item: DownloadItem, webContents?: unknown) => void
	>();
	const blockedRequests = new Map<string, BlockedRequest>();
	const activeItems = new Map<string, DownloadItemLike>();
	const activeItemListeners = new Map<string, {
		item: DownloadItemLike;
		updated: (event: unknown, state: "progressing" | "interrupted") => void;
		done: (event: unknown, state: DoneState) => void;
		settle: (state: DoneState) => DoneState;
	}>();
	const reservedPaths = new Set<string>();
	let disposed = false;
	let downloads: StoredDownload[] = [];
	let error = "";

	try {
		const parsed = JSON.parse(readFileSync(options.historyPath, "utf8")) as unknown;
		if (Array.isArray(parsed)) {
			downloads = parsed
				.filter((download): download is StoredDownload =>
					isStoredDownload(download) && isInsideDirectory(options.downloadsDirectory, download.savePath))
				.map((download) => ({
					...download,
					fileName: path.basename(download.savePath),
					status: download.status === "progressing" || download.status === "paused" ? "interrupted" : download.status,
					active: false,
					resumable: false,
				}));
		}
	} catch {
		// A missing or malformed optional history file starts with an empty list.
	}
	const removeStaged = (target: string): void => {
		try {
			rmSync(target, { recursive: true, force: true });
		} catch {
			// A leftover private staging file never blocks a download.
		}
	};
	// Blocked requests do not survive a restart, so anything staged at app start
	// is an orphan. A manager created later in the same run (window reopened)
	// must not sweep: downloads allowed under the previous one still write there.
	if (!sweptStagingDirectories.has(options.stagingDirectory)) {
		sweptStagingDirectories.add(options.stagingDirectory);
		removeStaged(options.stagingDirectory);
	}

	const state = (): BrowserDownloadsState => ({
		downloads: downloads.map(publicDownload),
		...(error ? { error } : {}),
	});
	const persist = (): void => {
		mkdirSync(path.dirname(options.historyPath), { recursive: true });
		const temporaryPath = `${options.historyPath}.tmp`;
		// Blocked requests do not survive a restart, so they are not history.
		const history = downloads.filter((download) => download.status !== "blocked");
		writeFileSync(temporaryPath, JSON.stringify(history), { encoding: "utf8", mode: 0o600 });
		renameSync(temporaryPath, options.historyPath);
	};
	const publish = (persistState = false): BrowserDownloadsState => {
		if (persistState) {
			try {
				persist();
			} catch {
				// A history write failure must not abort Chromium's file transfer.
			}
		}
		const next = state();
		if (!disposed) {
			try {
				options.notify(next);
			} catch {
				// A window can disappear while an Electron download event is being
				// dispatched. Notification failure must not block other listeners.
			}
		}
		return next;
	};
	const updateItem = (id: string, item: DownloadItemLike, status?: StoredDownload["status"]): void => {
		downloads = downloads.map<StoredDownload>((download) =>
			download.id === id
				? {
						...download,
						receivedBytes: Math.max(0, item.getReceivedBytes()),
						totalBytes: Math.max(0, item.getTotalBytes()),
						status: status ?? (item.isPaused() ? "paused" : "progressing"),
						active: activeItems.has(id),
						resumable: status === "interrupted" && activeItems.has(id) && item.canResume(),
						updatedAt: (options.now ?? Date.now)(),
					}
				: download,
		);
	};

	// Drops a blocked request: stops its transfer and deletes what was staged.
	const discardBlocked = (id: string): void => {
		const request = blockedRequests.get(id);
		if (!request) return;
		blockedRequests.delete(id);
		request.item.removeListener("done", request.done);
		if (request.staged === "pending") {
			try {
				request.item.cancel();
			} catch {
				// The item may already be finished or destroyed.
			}
		}
		removeStaged(path.dirname(request.stagedPath));
	};

	// Pages, redirects, and agent-driven navigation can all start a download
	// without the user asking for one. Nothing reaches Downloads until the user
	// allows it; until then the bytes stay in the private staging folder.
	const block = (
		event: DownloadEventLike,
		item: DownloadItemLike,
		session: DownloadSessionLike,
		webContents?: unknown,
	): void => {
		const url = requestedURL(item);
		const now = (options.now ?? Date.now)();
		// Only a repeat from the same tab is the same request. Another profile or
		// tab asking for this URL gets its own row, so the file a row shows is
		// always the one that row's Download saves.
		const existing = [...blockedRequests].find(([, request]) =>
			request.url === url && request.session === session && request.webContents === webContents)?.[0];
		if (existing) {
			event.preventDefault();
			downloads = downloads.map((download) => download.id === existing ? { ...download, updatedAt: now } : download);
			publish();
			return;
		}
		const id = (options.createId ?? randomUUID)();
		const fileName = safeFilename(item.getFilename());
		const stagedPath = path.join(options.stagingDirectory, id, fileName);
		try {
			mkdirSync(path.dirname(stagedPath), { recursive: true });
			item.setSavePath(stagedPath);
		} catch {
			event.preventDefault();
			error = DOWNLOAD_DESTINATION_ERROR;
			publish();
			return;
		}
		// Pausing saves bandwidth while the user decides, but a paused transfer
		// can be closed by the server. Only one Chromium can resume (it has a
		// validator for a Range request) is paused; a POST export or a blob
		// finishes into staging instead. MAX_BLOCKED_DOWNLOADS bounds that.
		if (item.getETag() || item.getLastModifiedTime()) {
			try {
				item.pause();
			} catch {
				// The staging folder still keeps the bytes out of Downloads.
			}
		}
		const done = (_event: unknown, doneState: "completed" | "cancelled" | "interrupted") => {
			const request = blockedRequests.get(id);
			if (!request) return;
			if (doneState === "completed") {
				request.staged = "completed";
				return;
			}
			// The request failed before anyone allowed it; nothing is left to save.
			discardBlocked(id);
			downloads = downloads.filter((download) => download.id !== id);
			publish();
		};
		blockedRequests.set(id, { url, session, webContents, item, stagedPath, staged: "pending", done });
		item.once("done", done);
		const download: StoredDownload = {
			id,
			fileName,
			savePath: "",
			source: downloadSource(url),
			receivedBytes: 0,
			totalBytes: Math.max(0, item.getTotalBytes()),
			status: "blocked",
			active: false,
			resumable: false,
			startedAt: now,
			updatedAt: now,
		};
		downloads = withinLimits([download, ...downloads]);
		for (const blockedId of [...blockedRequests.keys()]) {
			if (!downloads.some((candidate) => candidate.id === blockedId)) discardBlocked(blockedId);
		}
		publish();
	};

	// Saves the staged file under the approved row's name in Downloads.
	const finish = (stagedPath: string, savePath: string): "completed" | "interrupted" => {
		try {
			moveFile(stagedPath, savePath);
			return "completed";
		} catch {
			error = DOWNLOAD_MOVE_ERROR;
			return "interrupted";
		} finally {
			removeStaged(path.dirname(stagedPath));
		}
	};

	const allow = (id: string, request: BlockedRequest, approved: StoredDownload): void => {
		let savePath: string;
		try {
			mkdirSync(options.downloadsDirectory, { recursive: true });
			savePath = collisionSafePath(options.downloadsDirectory, approved.fileName, reservedPaths);
		} catch {
			discardBlocked(id);
			downloads = downloads.filter((download) => download.id !== id);
			error = DOWNLOAD_DESTINATION_ERROR;
			publish();
			return;
		}
		const { item, stagedPath } = request;
		item.removeListener("done", request.done);
		blockedRequests.delete(id);
		error = "";
		const now = (options.now ?? Date.now)();
		const row: StoredDownload = {
			...approved,
			fileName: path.basename(savePath),
			savePath,
			status: "progressing",
			startedAt: now,
			updatedAt: now,
		};
		downloads = withinLimits([row, ...downloads.filter((download) => download.id !== id)]);
		if (request.staged === "completed") {
			updateItem(id, item, finish(stagedPath, savePath));
			publish(true);
			return;
		}
		reservedPaths.add(savePath.toLowerCase());
		activeItems.set(id, item);
		const updated = (_event: unknown, updateState: "progressing" | "interrupted") => {
			updateItem(id, item, updateState === "interrupted" ? "interrupted" : undefined);
			publish();
		};
		// Puts the finished file in place; also runs for an item a disposed
		// manager still owns, so a window closed mid-download loses nothing.
		const settle = (doneState: DoneState): DoneState => {
			if (doneState === "completed") return finish(stagedPath, savePath);
			removeStaged(path.dirname(stagedPath));
			return doneState;
		};
		const done = (_event: unknown, doneState: DoneState) => {
			activeItems.delete(id);
			activeItemListeners.delete(id);
			reservedPaths.delete(savePath.toLowerCase());
			updateItem(id, item, settle(doneState));
			publish(true);
		};
		activeItemListeners.set(id, { item, updated, done, settle });
		item.on("updated", updated);
		item.once("done", done);
		item.resume();
		updateItem(id, item);
		publish(true);
	};

	return {
		attach(session: DownloadSessionLike | undefined): void {
			if (!session || disposed || attachedSessions.has(session)) return;
			const listener = (event: DownloadEventLike, item: DownloadItem, webContents?: unknown) => {
				// Electron reports a session-level request with a null WebContents.
				block(event, item, session, webContents ?? undefined);
			};
			attachedSessions.set(session, listener);
			session.on("will-download", listener);
		},
		dispose(): void {
			if (disposed) return;
			disposed = true;
			for (const [session, listener] of attachedSessions) {
				session.removeListener("will-download", listener);
			}
			attachedSessions.clear();
			for (const { item, updated, done, settle } of activeItemListeners.values()) {
				item.removeListener("updated", updated);
				item.removeListener("done", done);
				item.once("done", (_event: unknown, doneState: DoneState) => settle(doneState));
			}
			activeItemListeners.clear();
			activeItems.clear();
			reservedPaths.clear();
			for (const id of [...blockedRequests.keys()]) discardBlocked(id);
			try {
				// Observers (wake policy) must learn that nothing is tracked any more;
				// otherwise they keep treating the last progressing download as live.
				options.notify({ downloads: [] });
			} catch {
				// The window may already be gone.
			}
		},
		list: state,
		async action(input: BrowserDownloadActionInput): Promise<BrowserDownloadsState> {
			if (!input || typeof input.id !== "string") throw new Error("Invalid download action");
			const download = downloads.find((candidate) => candidate.id === input.id);
			if (!download) throw new Error("Download not found");
			const item = activeItems.get(input.id);
			switch (input.action) {
				case "allow": {
					const request = blockedRequests.get(input.id);
					if (download.status !== "blocked" || !request) throw new Error(DOWNLOAD_UNAVAILABLE_ERROR);
					allow(input.id, request, download);
					return state();
				}
				case "pause":
					if (!item) throw new Error("Download is no longer active");
					item.pause();
					updateItem(input.id, item, "paused");
					return publish(true);
				case "resume":
					if (!item) throw new Error("Download is no longer active");
					item.resume();
					updateItem(input.id, item, "progressing");
					return publish(true);
				case "cancel":
					if (!item) throw new Error("Download is no longer active");
					item.cancel();
					return state();
				case "open": {
					if (download.status !== "completed" || !existsSync(download.savePath)) throw new Error("Downloaded file is unavailable");
					const error = await options.shell.openPath(download.savePath);
					if (error) throw new Error(error);
					return state();
				}
				case "show":
					if (download.status !== "completed" || !existsSync(download.savePath)) throw new Error("Downloaded file is unavailable");
					options.shell.showItemInFolder(download.savePath);
					return state();
				case "remove": {
					if (item) throw new Error("Active downloads cannot be removed");
					try {
						if (existsSync(download.savePath)) {
							if (!isInsideDirectory(options.downloadsDirectory, download.savePath)) {
								throw new Error(DOWNLOAD_DELETE_ERROR);
							}
							const file = lstatSync(download.savePath);
							if (!file.isFile() && !file.isSymbolicLink()) throw new Error(DOWNLOAD_DELETE_ERROR);
							await options.shell.trashItem(download.savePath);
						}
					} catch {
						throw new Error(DOWNLOAD_DELETE_ERROR);
					}
					discardBlocked(input.id);
					downloads = downloads.filter((candidate) => candidate.id !== input.id);
					return publish(true);
				}
				default:
					throw new Error("Unsupported download action");
			}
		},
		clear(): BrowserDownloadsState {
			downloads = downloads.filter((download) => activeItems.has(download.id));
			for (const id of [...blockedRequests.keys()]) discardBlocked(id);
			return publish(true);
		},
	};
}
