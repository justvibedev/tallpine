import { EventEmitter } from "node:events";
import { existsSync, mkdtempSync, mkdirSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createBrowserDownloadManager } from "./browser-download-manager";

const temporaryDirectories: string[] = [];

afterEach(() => {
	for (const directory of temporaryDirectories.splice(0)) rmSync(directory, { recursive: true, force: true });
});

function managerOptions(root: string, overrides: Partial<Parameters<typeof createBrowserDownloadManager>[0]> = {}) {
	return {
		downloadsDirectory: path.join(root, "Downloads"),
		stagingDirectory: path.join(root, "data", "browser-downloads-pending"),
		historyPath: path.join(root, "data", "browser-downloads.json"),
		shell: { openPath: vi.fn(async () => ""), showItemInFolder: vi.fn(), trashItem: vi.fn(async () => undefined) },
		notify: vi.fn(),
		...overrides,
	};
}

function setup() {
	const root = mkdtempSync(path.join(os.tmpdir(), "tallpine-browser-downloads-"));
	temporaryDirectories.push(root);
	const notify = vi.fn();
	const openPath = vi.fn(async () => "");
	const showItemInFolder = vi.fn();
	const trashItem = vi.fn(async (filePath: string) => {
		rmSync(filePath);
	});
	const options = managerOptions(root, {
		shell: { openPath, showItemInFolder, trashItem },
		notify,
		now: () => 42,
		createId: () => "download-1",
	});
	const manager = createBrowserDownloadManager(options);
	const session = new EventEmitter();
	const sessionOn = vi.spyOn(session, "on");
	const removeListener = vi.spyOn(session, "removeListener");
	manager.attach(session as never);
	return {
		...options,
		root,
		manager,
		notify,
		openPath,
		removeListener,
		session,
		sessionOn,
		showItemInFolder,
		trashItem,
		start: (item: FakeDownloadItem) => startApproved(manager, session, item),
	};
}

// Requests a download the way Chromium does, then allows it the way the user
// does from the Downloads list. Returns the request's will-download event.
function startApproved(
	manager: ReturnType<typeof createBrowserDownloadManager>,
	session: EventEmitter,
	item: FakeDownloadItem,
) {
	const event = { preventDefault: vi.fn() };
	session.emit("will-download", event, item, {});
	const blocked = manager.list().downloads.find((download) => download.status === "blocked");
	if (blocked) void manager.action({ id: blocked.id, action: "allow" });
	return { event };
}

// Behaves like Chromium's DownloadItem: bytes go to the path set in
// will-download, and "done" fires once the file is complete there.
class FakeDownloadItem extends EventEmitter {
	receivedBytes = 0;
	totalBytes = 100;
	paused = false;
	resumable = true;
	savedTo = "";
	setSavePath = vi.fn((target: string) => { this.savedTo = target; });
	pause = vi.fn(() => { this.paused = true; });
	resume = vi.fn(() => { this.paused = false; });
	cancel = vi.fn();
	url = "https://downloads.example.test/files/report.txt";
	getFilename = () => "report.txt";
	getURL = () => this.url;
	getURLChain = () => [this.url];
	getReceivedBytes = () => this.receivedBytes;
	getTotalBytes = () => this.totalBytes;
	isPaused = () => this.paused;
	canResume = () => this.resumable;
	// A server validator lets Chromium resume with a Range request.
	etag = '"v1"';
	lastModified = "";
	getETag = () => this.etag;
	getLastModifiedTime = () => this.lastModified;
	finish(content = "downloaded") {
		writeFileSync(this.savedTo, content);
		this.receivedBytes = content.length;
		this.emit("done", {}, "completed");
	}
}

describe("browser download manager", () => {
	it("contains destination setup failures and reports them without exposing the path", () => {
		const root = mkdtempSync(path.join(os.tmpdir(), "tallpine-browser-download-failure-"));
		temporaryDirectories.push(root);
		const blockingFile = path.join(root, "not-a-directory");
		writeFileSync(blockingFile, "blocker");
		const options = managerOptions(root, { downloadsDirectory: path.join(blockingFile, "Downloads") });
		const manager = createBrowserDownloadManager(options);
		const session = new EventEmitter();
		manager.attach(session as never);
		const item = new FakeDownloadItem();

		expect(() => startApproved(manager, session, item)).not.toThrow();
		expect(item.cancel).toHaveBeenCalledOnce();
		expect(manager.list()).toEqual({
			downloads: [],
			error: "Could not prepare the Downloads folder.",
		});
		expect(options.notify).toHaveBeenCalledWith(manager.list());
		expect(JSON.stringify(manager.list())).not.toContain(blockingFile);
		expect(readdirSync(options.stagingDirectory)).toEqual([]);
	});

	it("blocks a download nobody approved without saving it to Downloads", async () => {
		const test = setup();
		const item = new FakeDownloadItem();
		const event = { preventDefault: vi.fn() };
		const tab = {};

		test.session.emit("will-download", event, item, tab);

		// Chromium's request is kept, paused, writing only to private staging.
		expect(event.preventDefault).not.toHaveBeenCalled();
		expect(item.setSavePath).toHaveBeenCalledWith(path.join(test.stagingDirectory, "download-1", "report.txt"));
		expect(item.pause).toHaveBeenCalledOnce();
		expect(existsSync(test.downloadsDirectory)).toBe(false);
		expect(existsSync(test.historyPath)).toBe(false);
		expect(test.manager.list().downloads).toEqual([expect.objectContaining({
			id: "download-1",
			fileName: "report.txt",
			source: "downloads.example.test",
			status: "blocked",
			active: false,
		})]);
		expect(JSON.stringify(test.manager.list())).not.toContain(item.url);
		expect(JSON.stringify(test.manager.list())).not.toContain(test.stagingDirectory);

		// The same link opened again in that tab stays one blocked entry.
		const repeat = { preventDefault: vi.fn() };
		test.session.emit("will-download", repeat, new FakeDownloadItem(), tab);
		expect(repeat.preventDefault).toHaveBeenCalledOnce();
		expect(test.manager.list().downloads).toHaveLength(1);

		// Even a small file that finished into staging stays out of Downloads.
		item.finish();
		expect(test.manager.list().downloads[0]?.status).toBe("blocked");
		expect(existsSync(test.downloadsDirectory)).toBe(false);

		// Dismissing it deletes the staged file and leaves Downloads untouched.
		await test.manager.action({ id: "download-1", action: "remove" });
		expect(test.manager.list().downloads).toEqual([]);
		expect(test.trashItem).not.toHaveBeenCalled();
		expect(readdirSync(test.stagingDirectory)).toEqual([]);
		await expect(test.manager.action({ id: "download-1", action: "allow" })).rejects.toThrow("Download not found");
	});

	// B846: a site export is a blob: URL the page revokes right after the click.
	// Requesting that URL again ends "interrupted" with 0 bytes, so allowing a
	// download must keep Chromium's original request instead of replaying it.
	it("saves a revoked blob export the user allows, without requesting it again", async () => {
		const test = setup();
		const item = new FakeDownloadItem();
		item.url = "blob:https://app.example.test/4b6f2c1e-0d7a-4f8e-9c55-1f2a3b4c5d6e";
		item.getFilename = () => "stats.csv";
		const tab = { downloadURL: vi.fn() };
		test.session.emit("will-download", { preventDefault: vi.fn() }, item, tab);
		item.finish("a,b\n1,2\n");
		expect(test.manager.list().downloads[0]).toMatchObject({ fileName: "stats.csv", source: "app.example.test", status: "blocked" });

		await test.manager.action({ id: "download-1", action: "allow" });

		expect(tab.downloadURL).not.toHaveBeenCalled();
		const savePath = path.join(test.downloadsDirectory, "stats.csv");
		expect(readFileSync(savePath, "utf8")).toBe("a,b\n1,2\n");
		expect(test.manager.list().downloads).toEqual([expect.objectContaining({
			id: "download-1",
			fileName: "stats.csv",
			status: "completed",
			active: false,
		})]);
		expect(readdirSync(test.stagingDirectory)).toEqual([]);
		expect(JSON.parse(readFileSync(test.historyPath, "utf8"))[0]).toMatchObject({ status: "completed", savePath });
	});

	it("resumes a still-running blocked download when allowed and moves it into Downloads when done", async () => {
		const test = setup();
		const item = new FakeDownloadItem();
		test.session.emit("will-download", { preventDefault: vi.fn() }, item, {});

		await test.manager.action({ id: "download-1", action: "allow" });

		expect(item.resume).toHaveBeenCalledOnce();
		expect(test.manager.list().downloads[0]).toMatchObject({ id: "download-1", status: "progressing", active: true });
		expect(existsSync(path.join(test.downloadsDirectory, "report.txt"))).toBe(false);

		item.finish();
		expect(readFileSync(path.join(test.downloadsDirectory, "report.txt"), "utf8")).toBe("downloaded");
		expect(test.manager.list().downloads[0]).toMatchObject({ status: "completed", active: false });
		expect(readdirSync(test.stagingDirectory)).toEqual([]);
		await expect(test.manager.action({ id: "download-1", action: "allow" })).rejects.toThrow("no longer available");
	});

	it("lets a transfer Chromium cannot resume finish into staging instead of pausing it", async () => {
		const test = setup();
		const item = new FakeDownloadItem();
		item.etag = "";
		test.session.emit("will-download", { preventDefault: vi.fn() }, item, {});

		// A POST export has no validator: a paused one could be closed by the server.
		expect(item.pause).not.toHaveBeenCalled();
		item.finish();
		expect(test.manager.list().downloads[0]?.status).toBe("blocked");
		expect(existsSync(test.downloadsDirectory)).toBe(false);

		await test.manager.action({ id: "download-1", action: "allow" });
		expect(readFileSync(path.join(test.downloadsDirectory, "report.txt"), "utf8")).toBe("downloaded");

		const dated = new FakeDownloadItem();
		dated.etag = "";
		dated.lastModified = "Wed, 18 Nov 2026 10:00:00 GMT";
		test.session.emit("will-download", { preventDefault: vi.fn() }, dated, {});
		expect(dated.pause).toHaveBeenCalledOnce();
	});

	it("still saves an allowed download that finishes after its manager was disposed", async () => {
		const test = setup();
		const item = new FakeDownloadItem();
		test.session.emit("will-download", { preventDefault: vi.fn() }, item, {});
		await test.manager.action({ id: "download-1", action: "allow" });

		// The window closes mid-download and a new one creates a manager.
		test.manager.dispose();
		createBrowserDownloadManager(managerOptions(test.root));
		expect(readdirSync(test.stagingDirectory)).toEqual(["download-1"]);

		item.finish();
		expect(readFileSync(path.join(test.downloadsDirectory, "report.txt"), "utf8")).toBe("downloaded");
		expect(readdirSync(test.stagingDirectory)).toEqual([]);
	});

	it("drops a blocked request that fails before anyone allows it", () => {
		const test = setup();
		const item = new FakeDownloadItem();
		test.session.emit("will-download", { preventDefault: vi.fn() }, item, {});

		item.emit("done", {}, "interrupted");

		expect(test.manager.list().downloads).toEqual([]);
		expect(readdirSync(test.stagingDirectory)).toEqual([]);
	});

	it("reports a staged file that cannot be moved into Downloads as interrupted", async () => {
		const test = setup();
		const item = new FakeDownloadItem();
		test.session.emit("will-download", { preventDefault: vi.fn() }, item, {});
		await test.manager.action({ id: "download-1", action: "allow" });

		// Chromium reports completion, but the staged file is gone.
		item.emit("done", {}, "completed");

		expect(test.manager.list()).toEqual({
			downloads: [expect.objectContaining({ status: "interrupted", active: false })],
			error: "Could not move the downloaded file into the Downloads folder.",
		});
	});

	it("removes staged files a previous run left behind", () => {
		const root = mkdtempSync(path.join(os.tmpdir(), "tallpine-browser-downloads-"));
		temporaryDirectories.push(root);
		const options = managerOptions(root);
		mkdirSync(path.join(options.stagingDirectory, "old"), { recursive: true });
		writeFileSync(path.join(options.stagingDirectory, "old", "orphan.csv"), "x");

		createBrowserDownloadManager(options);

		expect(existsSync(options.stagingDirectory)).toBe(false);
	});

	it("keeps each blocked row tied to the profile that requested its file", async () => {
		const root = mkdtempSync(path.join(os.tmpdir(), "tallpine-browser-downloads-"));
		temporaryDirectories.push(root);
		let nextId = 0;
		const manager = createBrowserDownloadManager(managerOptions(root, { createId: () => `download-${nextId++}` }));
		const sessionA = new EventEmitter();
		const sessionB = new EventEmitter();
		manager.attach(sessionA as never);
		manager.attach(sessionB as never);
		const itemA = new FakeDownloadItem();
		itemA.getFilename = () => "account-a.csv";
		const itemB = new FakeDownloadItem();
		itemB.getFilename = () => "account-b.csv";

		// Both profiles ask for the same URL; each row saves its own file.
		sessionA.emit("will-download", { preventDefault: vi.fn() }, itemA, {});
		sessionB.emit("will-download", { preventDefault: vi.fn() }, itemB, {});

		expect(manager.list().downloads.map((download) => [download.id, download.fileName, download.status])).toEqual([
			["download-1", "account-b.csv", "blocked"],
			["download-0", "account-a.csv", "blocked"],
		]);

		await manager.action({ id: "download-0", action: "allow" });
		expect(itemA.resume).toHaveBeenCalledOnce();
		expect(itemB.resume).not.toHaveBeenCalled();
		expect(manager.list().downloads.map((download) => [download.fileName, download.status])).toEqual([
			["account-a.csv", "progressing"],
			["account-b.csv", "blocked"],
		]);
	});

	it("caps blocked requests without evicting real download history", async () => {
		const root = mkdtempSync(path.join(os.tmpdir(), "tallpine-browser-downloads-"));
		temporaryDirectories.push(root);
		const downloadsDirectory = path.join(root, "Downloads");
		const historyPath = path.join(root, "data", "browser-downloads.json");
		mkdirSync(path.dirname(historyPath), { recursive: true });
		const history = Array.from({ length: 200 }, (_, index) => ({
			id: `done-${index}`,
			fileName: `done-${index}.txt`,
			savePath: path.join(downloadsDirectory, `done-${index}.txt`),
			receivedBytes: 1,
			totalBytes: 1,
			status: "completed",
			startedAt: 1,
			updatedAt: 1,
		}));
		writeFileSync(historyPath, JSON.stringify(history));
		let nextId = 0;
		const manager = createBrowserDownloadManager(managerOptions(root, { createId: () => `new-${nextId++}` }));
		const session = new EventEmitter();
		manager.attach(session as never);

		const first = new FakeDownloadItem();
		for (let index = 0; index < 250; index += 1) {
			const item = index === 0 ? first : new FakeDownloadItem();
			item.url = `https://downloads.example.test/files/${index}.zip`;
			session.emit("will-download", { preventDefault: vi.fn() }, item, {});
		}

		const listed = manager.list().downloads;
		expect(listed.filter((download) => download.status === "blocked").map((download) => download.id))
			.toEqual(Array.from({ length: 20 }, (_, index) => `new-${249 - index}`));
		expect(listed.filter((download) => download.status === "completed")).toHaveLength(200);
		// The oldest blocked request was dropped: its transfer is stopped and it
		// can no longer be allowed.
		expect(first.cancel).toHaveBeenCalledOnce();
		await expect(manager.action({ id: "new-0", action: "allow" })).rejects.toThrow("Download not found");

		// Allowing one persists history: every real entry is still there.
		startApproved(manager, session, new FakeDownloadItem());
		const persisted = JSON.parse(readFileSync(historyPath, "utf8")) as Array<{ id: string; status: string }>;
		expect(persisted.filter((download) => download.status === "completed")).toHaveLength(199);
		expect(persisted.some((download) => download.status === "blocked")).toBe(false);
	});

	it("tracks progress, supports controls, and reveals a completed system download", async () => {
		const test = setup();
		mkdirSync(test.downloadsDirectory, { recursive: true });
		writeFileSync(path.join(test.downloadsDirectory, "report.txt"), "existing");
		const item = new FakeDownloadItem();

		test.start(item);
		const savePath = path.join(test.downloadsDirectory, "report (1).txt");
		expect(test.manager.list().downloads[0]).toMatchObject({ id: "download-1", fileName: "report (1).txt", status: "progressing" });

		item.receivedBytes = 25;
		item.emit("updated", {}, "progressing");
		expect(test.manager.list().downloads[0]?.receivedBytes).toBe(25);
		await test.manager.action({ id: "download-1", action: "pause" });
		// Once while it waited for the user, once for the explicit pause.
		expect(item.pause).toHaveBeenCalledTimes(2);
		await test.manager.action({ id: "download-1", action: "resume" });
		// Once when the user allowed it, once for the explicit resume.
		expect(item.resume).toHaveBeenCalledTimes(2);

		item.finish();
		await test.manager.action({ id: "download-1", action: "show" });
		expect(test.showItemInFolder).toHaveBeenCalledWith(savePath);
		await test.manager.action({ id: "download-1", action: "open" });
		expect(test.openPath).toHaveBeenCalledWith(savePath);
		expect(JSON.parse(readFileSync(test.historyPath, "utf8"))[0]).toMatchObject({ status: "completed", savePath });

		test.manager.clear();
		expect(test.manager.list().downloads).toEqual([]);
	});

	it("moves a downloaded file to the recycle bin before removing its history", async () => {
		const test = setup();
		const item = new FakeDownloadItem();
		test.start(item);
		item.finish();
		const savePath = path.join(test.downloadsDirectory, "report.txt");

		await test.manager.action({ id: "download-1", action: "remove" });

		expect(test.trashItem).toHaveBeenCalledWith(savePath);
		expect(existsSync(savePath)).toBe(false);
		expect(test.manager.list().downloads).toEqual([]);
		expect(JSON.parse(readFileSync(test.historyPath, "utf8"))).toEqual([]);
	});

	it("keeps download history when moving the local file to the recycle bin fails", async () => {
		const test = setup();
		const item = new FakeDownloadItem();
		test.start(item);
		item.finish();
		const savePath = path.join(test.downloadsDirectory, "report.txt");
		test.trashItem.mockRejectedValueOnce(new Error("Recycle bin unavailable"));

		await expect(test.manager.action({ id: "download-1", action: "remove" })).rejects.toThrow("Could not delete the downloaded file.");

		expect(existsSync(savePath)).toBe(true);
		expect(test.manager.list().downloads).toHaveLength(1);
	});

	it("attaches once per Electron session and restores unfinished history as interrupted", () => {
		const test = setup();
		test.manager.attach(test.session as never);
		expect(test.sessionOn).toHaveBeenCalledTimes(1);
		const item = new FakeDownloadItem();
		test.start(item);

		const restored = createBrowserDownloadManager(managerOptions(test.root));
		expect(restored.list().downloads[0]?.status).toBe("interrupted");
		expect(restored.list().downloads[0]?.resumable).toBe(false);
	});

	it("detaches session listeners before a replacement manager attaches", () => {
		const first = setup();
		first.manager.dispose();
		expect(first.removeListener).toHaveBeenCalledOnce();
		first.notify.mockImplementation(() => {
			throw new Error("destroyed shell WebContents");
		});

		const replacementNotify = vi.fn();
		const replacement = createBrowserDownloadManager(managerOptions(first.root, {
			notify: replacementNotify,
			createId: () => "download-2",
		}));
		replacement.attach(first.session as never);
		startApproved(replacement, first.session, new FakeDownloadItem());

		expect(first.manager.list().downloads).toEqual([]);
		expect(replacement.list().downloads[0]).toMatchObject({ id: "download-2", status: "progressing" });
		expect(replacementNotify).toHaveBeenCalled();
	});

	it("publishes an empty state on dispose so observers drop stale downloads", () => {
		const test = setup();
		test.start(new FakeDownloadItem());
		test.notify.mockClear();

		test.manager.dispose();

		expect(test.notify).toHaveBeenCalledOnce();
		expect(test.notify).toHaveBeenCalledWith({ downloads: [] });
	});

	it("detaches active item listeners when disposed", () => {
		const test = setup();
		const item = new FakeDownloadItem();
		test.start(item);
		expect(item.listenerCount("updated")).toBe(1);
		expect(item.listenerCount("done")).toBe(1);

		test.manager.dispose();

		expect(item.listenerCount("updated")).toBe(0);
		// Only the handler that moves the finished file into Downloads remains.
		expect(item.listenerCount("done")).toBe(1);
	});

	it("keeps an interrupted item resumable until Electron reports it done", async () => {
		const test = setup();
		const item = new FakeDownloadItem();
		test.start(item);

		item.emit("updated", {}, "interrupted");
		expect(test.manager.list().downloads[0]).toMatchObject({ status: "interrupted", active: true, resumable: true });
		await test.manager.action({ id: "download-1", action: "resume" });
		expect(item.resume).toHaveBeenCalledTimes(2);

		item.emit("done", {}, "interrupted");
		expect(test.manager.list().downloads[0]).toMatchObject({ status: "interrupted", active: false, resumable: false });
	});
});
