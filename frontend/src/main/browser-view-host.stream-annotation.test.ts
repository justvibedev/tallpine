import {
	describe,
	expect,
	it,
	vi,
} from "vitest";
import { type BrowserNavState } from "./browser-view-host";
import type { BrowserAnnotationSession, BrowserAnnotationSubmitPayload } from "../shared/browser-annotations";
import {
	annotationSession,
	submittedAnnotationSession,
	setupHost,
	enableAnnotation,
	setupTabHost,
} from "./browser-view-host-test-helpers";

vi.mock("./render-check-proxy", () => ({ startRenderCheckProxy: vi.fn(async () => 4321) }));

vi.mock("electron", async (importOriginal) => {
	const actual = await importOriginal<typeof import("electron")>();
	return { ...actual, net: { fetch: vi.fn() } };
});


describe("browser live stream", () => {
	it("stops and detaches live capture when the private stream link drops", async () => {
		const { debuggerSendCommand, host } = setupHost();
		await host.startLiveStream("sess-1", 7, {
			frame: vi.fn(),
			state: vi.fn(),
			error: vi.fn(),
		});
		expect(debuggerSendCommand).toHaveBeenCalledWith("Page.startScreencast", expect.any(Object));

		await host.stopAllLiveStreams();

		expect(debuggerSendCommand).toHaveBeenCalledWith("Page.stopScreencast");
		await expect(host.handleRemoteInput("sess-1", {
			kind: "pointer",
			phase: "down",
			x: 0.5,
			y: 0.5,
			button: "left",
		})).rejects.toMatchObject({ code: "BROWSER_REMOTE_NOT_VIEWED" });
	});

	const liveSink = () => ({ frame: vi.fn(), state: vi.fn(), error: vi.fn() });
	const panelRect = { x: 300, y: 40, width: 480, height: 520 };
	const hiddenRect = { x: 0, y: 0, width: 0, height: 0 };

	// A streamed view must paint offscreen without covering the board or other
	// sessions. Move it after showing so Chromium applies the offscreen bounds.
	it("parks a streamed browser outside the desktop after its panel is left", async () => {
		const { emit, host, invoke, setCurrentURL, view } = setupHost();
		const { viewId } = await invoke("browser:ensure", "sess-1");
		setCurrentURL("https://example.com/");
		emit("browser:setBounds", 1, { viewId, revision: 1, rect: panelRect, visible: true });
		await host.startLiveStream("sess-1", 7, liveSink());
		expect(view.setVisible).toHaveBeenLastCalledWith(true);

		view.setVisible.mockClear();
		emit("browser:setBounds", 1, { viewId, revision: 2, rect: hiddenRect, visible: false });
		// Restarting the stream re-applies the parked bounds, as a session switch does.
		await host.startLiveStream("sess-1", 8, liveSink());

		expect(view.setBounds).toHaveBeenLastCalledWith({ x: -10000, y: -10000, width: 1280, height: 720 });
		expect(view.setVisible.mock.invocationCallOrder.at(-1)).toBeLessThan(view.setBounds.mock.invocationCallOrder.at(-1)!);
	});

	it("hides a streamed view if the native view does not accept offscreen bounds", async () => {
		const { host, invoke, view } = setupHost();
		await invoke("browser:ensure", "sess-1");
		view.getBounds.mockReturnValue({ x: 20, y: 30, width: 700, height: 500 });
		const sink = liveSink();
		await host.startLiveStream("sess-1", 7, sink);
		expect(view.setVisible).toHaveBeenLastCalledWith(false);
		expect(sink.error).toHaveBeenCalledWith("BROWSER_CAPTURE_UNAVAILABLE", expect.stringContaining("parked outside"));
		await host.stopLiveStream("sess-1", 7);
	});

	it("captures an app-hidden window without capturing an idle visible desktop", async () => {
		vi.useFakeTimers();
		try {
			const { debuggerSendCommand, emit, host, invoke, isWindowVisible, setCurrentURL } = setupHost();
			const { viewId } = await invoke("browser:ensure", "sess-1");
			setCurrentURL("https://example.com/");
			emit("browser:setBounds", 1, { viewId, revision: 1, rect: panelRect, visible: true });
			await host.startLiveStream("sess-1", 7, liveSink());
			await vi.advanceTimersByTimeAsync(1000);
			expect(debuggerSendCommand).not.toHaveBeenCalledWith("Page.captureScreenshot", expect.anything());
			isWindowVisible.mockReturnValue(false);
			await vi.advanceTimersByTimeAsync(500);
			expect(debuggerSendCommand).toHaveBeenCalledWith("Page.captureScreenshot", expect.objectContaining({ captureBeyondViewport: true }));
			debuggerSendCommand.mockClear();
			isWindowVisible.mockReturnValue(true);
			await vi.advanceTimersByTimeAsync(1000);
			expect(debuggerSendCommand).not.toHaveBeenCalledWith("Page.captureScreenshot", expect.anything());
			await host.stopLiveStream("sess-1", 7);
		} finally { vi.useRealTimers(); }
	});

	it("gives a parked streamed page the viewport the desktop last showed", async () => {
		const { debuggerSendCommand, emit, host, invoke, setCurrentURL, webContents } = setupHost();
		const { viewId } = await invoke("browser:ensure", "sess-1");
		setCurrentURL("https://example.com/");
		emit("browser:setBounds", 1, { viewId, revision: 1, rect: panelRect, visible: true });
		const sink = liveSink();
		await host.startLiveStream("sess-1", 7, sink);
		expect(debuggerSendCommand).not.toHaveBeenCalledWith("Emulation.setDeviceMetricsOverride", expect.anything());

		emit("browser:setBounds", 1, { viewId, revision: 2, rect: hiddenRect, visible: false });
		expect(debuggerSendCommand).toHaveBeenCalledWith("Emulation.setDeviceMetricsOverride", {
			width: 480,
			height: 520,
			deviceScaleFactor: 0,
			mobile: false,
		});

		await host.handleRemoteInput("sess-1", { kind: "pointer", phase: "down", x: 1, y: 1, button: "left" });
		expect(webContents.sendInputEvent).toHaveBeenLastCalledWith(expect.objectContaining({ type: "mouseDown", x: 479, y: 519 }));

		emit("browser:setBounds", 1, { viewId, revision: 3, rect: panelRect, visible: true });
		expect(debuggerSendCommand).toHaveBeenLastCalledWith("Emulation.clearDeviceMetricsOverride");
	});

	it("emulates and resets an agent viewport through device metrics", async () => {
		const { debuggerSendCommand, host } = setupHost();
		await host.execute("sess-1", "open", { url: "https://example.com/" });
		const set = await host.execute("sess-1", "viewport", { width: 390, height: 844, deviceScaleFactor: 2, mobile: true });
		expect(debuggerSendCommand).toHaveBeenCalledWith("Emulation.setDeviceMetricsOverride", {
			width: 390,
			height: 844,
			deviceScaleFactor: 2,
			mobile: true,
		});
		expect(set).toMatchObject({ width: 390, height: 844, mobile: true });
		await host.execute("sess-1", "viewport", { reset: true });
		expect(debuggerSendCommand).toHaveBeenLastCalledWith("Emulation.clearDeviceMetricsOverride");
	});

	it("rejects invalid agent viewport sizes", async () => {
		const { debuggerSendCommand, host } = setupHost();
		await host.execute("sess-1", "open", { url: "https://example.com/" });
		await expect(host.execute("sess-1", "viewport", {})).rejects.toMatchObject({ code: "INVALID_ARGUMENT" });
		await expect(host.execute("sess-1", "viewport", { width: "x", height: 10 })).rejects.toMatchObject({ code: "INVALID_ARGUMENT" });
		await expect(host.execute("sess-1", "viewport", { width: 10, height: 10, deviceScaleFactor: -1 })).rejects.toMatchObject({ code: "INVALID_ARGUMENT" });
		expect(debuggerSendCommand).not.toHaveBeenCalledWith("Emulation.setDeviceMetricsOverride", expect.anything());
	});

	it("keeps the agent viewport against the live pin and restores the pin on reset", async () => {
		const { debuggerSendCommand, emit, host, invoke, setCurrentURL } = setupHost();
		const { viewId } = await invoke("browser:ensure", "sess-1");
		setCurrentURL("https://example.com/");
		emit("browser:setBounds", 1, { viewId, revision: 1, rect: panelRect, visible: true });
		await host.startLiveStream("sess-1", 7, liveSink());
		await host.execute("sess-1", "viewport", { width: 390, height: 844 });
		emit("browser:setBounds", 1, { viewId, revision: 2, rect: hiddenRect, visible: false });
		expect(debuggerSendCommand).not.toHaveBeenCalledWith("Emulation.setDeviceMetricsOverride", expect.objectContaining({ width: 480 }));
		await host.execute("sess-1", "viewport", { reset: true });
		expect(debuggerSendCommand).toHaveBeenLastCalledWith("Emulation.setDeviceMetricsOverride", expect.objectContaining({ width: 480, height: 520 }));
	});

	// A session whose desktop panel was never opened has no layout size at all,
	// so without a viewport the stream stays empty and input lands nowhere.
	it("gives a never-shown streamed page a default viewport", async () => {
		const { debuggerSendCommand, host } = setupHost();
		const state = await host.startLiveStream("sess-1", 7, liveSink());

		expect(debuggerSendCommand).toHaveBeenCalledWith("Emulation.setDeviceMetricsOverride", {
			width: 1280,
			height: 720,
			deviceScaleFactor: 0,
			mobile: false,
		});
		expect(state).toMatchObject({ width: 1280, height: 720 });

		await host.stopLiveStream("sess-1", 7);
		expect(debuggerSendCommand).toHaveBeenCalledWith("Emulation.clearDeviceMetricsOverride");
	});

	it("lets a phone scroll while the desktop panel is focused but not click", async () => {
		const { debuggerSendCommand, emit, host, invoke, setCurrentURL, webContents } = setupHost();
		const { viewId } = await invoke("browser:ensure", "sess-1");
		setCurrentURL("https://example.com/");
		emit("browser:setBounds", 1, { viewId, revision: 1, rect: panelRect, visible: true });
		await host.startLiveStream("sess-1", 7, liveSink());

		await host.handleRemoteInput("sess-1", { kind: "wheel", deltaX: 0, deltaY: -40 });
		expect(debuggerSendCommand).toHaveBeenLastCalledWith("Input.dispatchMouseEvent", { type: "mouseWheel", x: 240, y: 260, deltaX: -0, deltaY: 40 });
		expect(webContents.sendInputEvent).not.toHaveBeenCalled();

		await expect(
			host.handleRemoteInput("sess-1", { kind: "pointer", phase: "down", x: 0.5, y: 0.5, button: "left" }),
		).rejects.toMatchObject({ code: "BROWSER_REMOTE_READ_ONLY" });
	});

	it("dispatches scrolling to the parked page without native focus", async () => {
		const { debuggerSendCommand, host, webContents } = setupHost();
		await host.startLiveStream("sess-1", 7, liveSink());
		await host.handleRemoteInput("sess-1", { kind: "wheel", deltaX: 30, deltaY: -400 });
		expect(debuggerSendCommand).toHaveBeenLastCalledWith("Input.dispatchMouseEvent", {
			type: "mouseWheel", x: 640, y: 360, deltaX: -30, deltaY: 400,
		});
		expect(webContents.sendInputEvent).not.toHaveBeenCalled();
		expect(webContents.focus).not.toHaveBeenCalled();
		await expect(host.handleRemoteInput("sess-1", { kind: "wheel", deltaX: 0, deltaY: NaN })).rejects.toMatchObject({ code: "INVALID_ARGUMENT" });
	});

	// Regression, reported live: a page the agent built showed in the phone's
	// App preview tab but not in its Browser tab until someone opened the
	// desktop Browser panel, because only that panel opens a session's preview.
	it("opens the session preview for a phone watching an empty browser", async () => {
		const { host, webContents } = setupHost();
		const sink = liveSink();
		await host.startLiveStream("sess-1", 7, sink);
		webContents.loadURL.mockClear();

		await host.openLivePreview("sess-1", "http://localhost:4317/index.html");

		expect(webContents.loadURL).toHaveBeenCalledWith("http://localhost:4317/index.html");
		expect(sink.state).toHaveBeenLastCalledWith(expect.objectContaining({ url: "http://localhost:4317/index.html" }));
	});

	it("lets the person's phone remote open a private-network URL that an agent open refuses", async () => {
		const { host, webContents } = setupHost();
		await host.startLiveStream("sess-1", 7, liveSink());
		webContents.loadURL.mockClear();

		await host.handleRemoteNavigation("sess-1", { action: "open", url: "http://192.168.1.20:3000/" });
		expect(webContents.loadURL).toHaveBeenCalledWith("http://192.168.1.20:3000/");

		await expect(host.execute("sess-1", "open", { url: "http://192.168.1.20:3000/" })).rejects.toMatchObject({
			code: "BROWSER_URL_FORBIDDEN",
		});
	});

	it("keeps repeated desktop New Tab actions blank while mobile preview offers continue", async () => {
		const { host, invoke, webContents, setCurrentURL } = setupHost();
		const { viewId } = await invoke("browser:ensure", "sess-1");
		await host.startLiveStream("sess-1", 7, liveSink());
		await host.openLivePreview("sess-1", "http://localhost:4317/index.html");
		for (let i = 0; i < 3; i++) {
			await invoke("browser:openTab", { viewId });
			setCurrentURL("about:blank");
			webContents.loadURL.mockClear();
			await host.openLivePreview("sess-1", "http://localhost:4317/index.html");
			expect(webContents.loadURL).not.toHaveBeenCalled();
		}
		await host.stopLiveStream("sess-1", 7);
		await host.startLiveStream("sess-1", 8, liveSink());
		webContents.loadURL.mockClear();
		await host.openLivePreview("sess-1", "http://localhost:4317/index.html");
		expect(webContents.loadURL).not.toHaveBeenCalled();
	});

	it("does not seed a blank tab the user opened before the first preview offer", async () => {
		const { host, invoke, webContents, setCurrentURL } = setupHost();
		const { viewId } = await invoke("browser:ensure", "sess-1");
		await invoke("browser:openTab", { viewId });
		setCurrentURL("about:blank");
		await host.startLiveStream("sess-1", 7, liveSink());
		webContents.loadURL.mockClear();
		await host.openLivePreview("sess-1", "http://localhost:4317/index.html");
		expect(webContents.loadURL).not.toHaveBeenCalled();
	});

	it("keeps an explicitly cleared browser empty across mobile reconnects", async () => {
		const { host, invoke, webContents } = setupHost();
		const { viewId } = await invoke("browser:ensure", "sess-1");
		await invoke("browser:clear", viewId);
		await host.startLiveStream("sess-1", 7, liveSink());
		webContents.loadURL.mockClear();
		await host.openLivePreview("sess-1", "http://localhost:4317/index.html");
		expect(webContents.loadURL).not.toHaveBeenCalled();
	});

	it("publishes the initial empty state before hidden capture preparation finishes", async () => {
		const { host, debuggerSendCommand } = setupHost();
		let finish: ((value: unknown) => void) | undefined;
		debuggerSendCommand.mockImplementation(async (method) => method === "Page.startScreencast"
			? new Promise((resolve) => { finish = resolve; }) : {});
		const sink = liveSink();
		const starting = host.startLiveStream("sess-1", 7, sink);
		await vi.waitFor(() => expect(sink.state).toHaveBeenCalledWith(expect.objectContaining({ url: "", width: 1280, height: 720 })));
		await vi.waitFor(() => expect(finish).toBeTypeOf("function"));
		finish?.({});
		await starting;
		await host.stopLiveStream("sess-1", 7);
	});

	it("never replaces a page that is already open with the session preview", async () => {
		const { host, setCurrentURL, webContents } = setupHost();
		await host.startLiveStream("sess-1", 7, liveSink());
		setCurrentURL("https://example.com/docs");
		webContents.loadURL.mockClear();

		await host.openLivePreview("sess-1", "http://localhost:4317/index.html");
		await host.stopLiveStream("sess-1", 7);
		setCurrentURL("");
		await host.openLivePreview("sess-1", "http://localhost:4317/index.html");

		expect(webContents.loadURL).not.toHaveBeenCalled();
	});
});


describe("browser screenshots", () => {
	it("copies the active page capture to the system clipboard", async () => {
		const { invoke, webContents, writeImage } = setupHost();
		const state = await invoke("browser:ensure", "sess-1");

		await expect(invoke("browser:captureScreenshot", state.viewId)).rejects.toThrow(
			"Open a page before taking a screenshot",
		);
		await invoke("browser:navigate", { viewId: state.viewId, url: "https://example.test/" });
		await invoke("browser:captureScreenshot", state.viewId);

		expect(webContents.capturePage).toHaveBeenCalledOnce();
		expect(writeImage).toHaveBeenCalledWith(expect.objectContaining({ isEmpty: expect.any(Function) }));
	});

	it("rejects screenshot requests from a renderer that does not own the browser panel", async () => {
		const { invoke, invokeFromTab, writeImage } = setupHost();
		const state = await invoke("browser:ensure", "sess-1");

		await expect(invokeFromTab("browser:captureScreenshot", 99, state.viewId)).rejects.toThrow(
			"Browser tab is unavailable",
		);
		expect(writeImage).not.toHaveBeenCalled();
	});
});


describe("annotation screenshots", () => {
	it("copies an annotation capture to the system clipboard", async () => {
		const { invoke, invokeFromTab, webContents, writeImage } = setupHost();
		await enableAnnotation(invoke, (await invoke("browser:ensure", "sess-1")) as BrowserNavState);

		const copied = await invokeFromTab("browser:annotation:capture", 99);

		expect(copied).toBe(true);
		expect(webContents.capturePage).toHaveBeenCalledOnce();
		expect(writeImage).toHaveBeenCalledWith(expect.objectContaining({ isEmpty: expect.any(Function) }));
	});

	it("refuses a capture from a page whose annotation mode is off", async () => {
		const { invoke, invokeFromTab, writeImage } = setupHost();
		await invoke("browser:ensure", "sess-1");

		expect(await invokeFromTab("browser:annotation:capture", 99)).toBe(false);
		expect(writeImage).not.toHaveBeenCalled();
	});

	it("resolves false without touching the clipboard when the page cannot be captured", async () => {
		const { invoke, invokeFromTab, webContents, writeImage } = setupHost();
		await enableAnnotation(invoke, (await invoke("browser:ensure", "sess-1")) as BrowserNavState);
		webContents.capturePage.mockResolvedValueOnce({
			isEmpty: () => true,
			toJPEG: () => Buffer.from("snapshot"),
			toPNG: () => Buffer.from("png-snapshot"),
			getSize: () => ({ width: 640, height: 480 }),
			resize: vi.fn(() => ({ toPNG: () => Buffer.from("resized-png") })),
		});

		const copied = await invokeFromTab("browser:annotation:capture", 99);

		expect(copied).toBe(false);
		expect(writeImage).not.toHaveBeenCalled();
	});
});


describe("browser annotation IPC", () => {
	it("routes renderer mode changes to the matching preview webContents", async () => {
		const { invoke, webContents } = setupHost();
		await invoke("browser:ensure", "sess-1");

		await invoke("browser:annotation:setMode", { viewId: "1:sess-1", enabled: true });

		expect(webContents.send).toHaveBeenCalledWith("browser:annotation:setMode", { enabled: true });
	});

	it("ignores annotation mode changes for views owned by a different renderer", async () => {
		const { invoke, webContents } = setupHost();
		await invoke("browser:ensure", "sess-1");

		await invoke("browser:annotation:setMode", { viewId: "2:sess-1", enabled: true });

		expect(webContents.send).not.toHaveBeenCalledWith("browser:annotation:setMode", { enabled: true });
	});

	it("focuses the preview webContents when annotation mode is enabled, so a keypress reaches it without a prior click", async () => {
		const { invoke, webContents } = setupHost();
		await invoke("browser:ensure", "sess-1");

		await invoke("browser:annotation:setMode", { viewId: "1:sess-1", enabled: true });

		expect(webContents.focus).toHaveBeenCalledTimes(1);
	});

	it("does not steal focus when annotation mode is turned off", async () => {
		const { invoke, webContents } = setupHost();
		await invoke("browser:ensure", "sess-1");
		await invoke("browser:annotation:setMode", { viewId: "1:sess-1", enabled: true });
		webContents.focus.mockClear();

		await invoke("browser:annotation:setMode", { viewId: "1:sess-1", enabled: false });

		expect(webContents.focus).not.toHaveBeenCalled();
	});

	it("drops an open draft when annotation mode is turned off, but keeps saved batch annotations", async () => {
		const { invoke, send, sent, webContents } = setupHost();
		await invoke("browser:ensure", "sess-1");
		await invoke("browser:navigate", { viewId: "1:sess-1", url: "http://localhost:4173/" });
		await invoke("browser:annotation:setMode", { viewId: "1:sess-1", enabled: true });
		const session = {
			...annotationSession(),
			annotations: submittedAnnotationSession().annotations,
		};
		send("browser:annotation:state", 99, session);
		webContents.send.mockClear();

		await invoke("browser:annotation:setMode", { viewId: "1:sess-1", enabled: false });

		expect(sent).toContainEqual({
			channel: "browser:annotation:state",
			payload: { viewId: "1:sess-1", count: 1, screenshotCount: 0, hasDraft: false },
		});
		const disablePayload = webContents.send.mock.calls.findLast(
			([channel, payload]) => channel === "browser:annotation:setMode" && payload?.enabled === false,
		)?.[1] as { enabled: boolean; session?: BrowserAnnotationSession } | undefined;
		expect(disablePayload?.session?.draft).toBeUndefined();
		expect(disablePayload?.session?.annotations).toEqual(session.annotations);

		webContents.send.mockClear();
		await invoke("browser:annotation:setMode", { viewId: "1:sess-1", enabled: true });

		const enablePayload = webContents.send.mock.calls.find(
			([channel, payload]) => channel === "browser:annotation:setMode" && payload?.enabled === true,
		)?.[1] as { enabled: boolean; session?: BrowserAnnotationSession } | undefined;
		expect(enablePayload?.session?.draft).toBeUndefined();
		expect(enablePayload?.session?.annotations).toHaveLength(1);
	});

	it("preserves and restores an unfinished annotation when the toolbar reloads the page", async () => {
		const { invoke, send, sent, webContents, webContentsListeners } = setupHost();
		await invoke("browser:ensure", "sess-1");
		await invoke("browser:navigate", { viewId: "1:sess-1", url: "http://localhost:4173/" });
		await invoke("browser:annotation:setMode", { viewId: "1:sess-1", enabled: true });
		const session = annotationSession();
		send("browser:annotation:state", 99, session);
		webContents.send.mockClear();
		webContents.focus.mockClear();

		await invoke("browser:reload", "1:sess-1");
		webContentsListeners.get("did-start-loading")?.();
		webContentsListeners.get("did-stop-loading")?.();

		expect(webContents.reload).toHaveBeenCalledOnce();
		expect(sent).not.toContainEqual({
			channel: "browser:annotation:canceled",
			payload: expect.anything(),
		});
		expect(webContents.send).toHaveBeenCalledWith("browser:annotation:setMode", { enabled: true, session });
		expect(webContents.focus).toHaveBeenCalledOnce();
	});

	it("re-enables annotation picking after a reload before any element is selected", async () => {
		const { invoke, sent, webContents, webContentsListeners } = setupHost();
		await invoke("browser:ensure", "sess-1");
		await invoke("browser:navigate", { viewId: "1:sess-1", url: "http://localhost:4173/" });
		await invoke("browser:annotation:setMode", { viewId: "1:sess-1", enabled: true });
		webContents.send.mockClear();
		webContents.focus.mockClear();

		await invoke("browser:reload", "1:sess-1");
		webContentsListeners.get("did-stop-loading")?.();

		expect(sent).not.toContainEqual({ channel: "browser:annotation:canceled", payload: expect.anything() });
		expect(webContents.send).toHaveBeenCalledWith("browser:annotation:setMode", { enabled: true });
		expect(webContents.focus).toHaveBeenCalledOnce();
	});

	it("preserves a draft when the same URL is submitted again by preview revision or the address bar", async () => {
		const { invoke, send, sent, webContents, webContentsListeners } = setupHost();
		await invoke("browser:ensure", "sess-1");
		await invoke("browser:navigate", { viewId: "1:sess-1", url: "http://localhost:4173/" });
		await invoke("browser:annotation:setMode", { viewId: "1:sess-1", enabled: true });
		const session = annotationSession();
		send("browser:annotation:state", 99, session);
		webContents.send.mockClear();

		await invoke("browser:navigate", { viewId: "1:sess-1", url: "http://localhost:4173/" });
		webContentsListeners.get("did-stop-loading")?.();

		expect(sent).not.toContainEqual({ channel: "browser:annotation:canceled", payload: expect.anything() });
		expect(webContents.send).toHaveBeenCalledWith("browser:annotation:setMode", { enabled: true, session });
	});

	it("cancels a draft when a page-driven main-frame navigation targets another document", async () => {
		const { invoke, send, sent, webContents, webContentsListeners } = setupHost();
		await invoke("browser:ensure", "sess-1");
		await invoke("browser:navigate", { viewId: "1:sess-1", url: "http://localhost:4173/" });
		await invoke("browser:annotation:setMode", { viewId: "1:sess-1", enabled: true });
		send("browser:annotation:state", 99, annotationSession());
		webContents.send.mockClear();

		webContentsListeners.get("will-navigate")?.(
			{ preventDefault: vi.fn() } as never,
			"http://localhost:4173/another-page" as never,
		);
		webContentsListeners.get("did-stop-loading")?.();

		expect(sent).toContainEqual({
			channel: "browser:annotation:canceled",
			payload: { viewId: "1:sess-1", reason: "navigation" },
		});
		expect(webContents.send).not.toHaveBeenCalledWith(
			"browser:annotation:setMode",
			expect.objectContaining({ enabled: true }),
		);
	});

	it("clears a draft when a reload is stopped or fails", async () => {
		const { invoke, send, sent, webContentsListeners } = setupHost();
		await invoke("browser:ensure", "sess-1");
		await invoke("browser:navigate", { viewId: "1:sess-1", url: "http://localhost:4173/" });
		await invoke("browser:annotation:setMode", { viewId: "1:sess-1", enabled: true });
		send("browser:annotation:state", 99, annotationSession());

		await invoke("browser:stop", "1:sess-1");

		expect(sent).toContainEqual({
			channel: "browser:annotation:canceled",
			payload: { viewId: "1:sess-1", reason: "navigation" },
		});

		await invoke("browser:annotation:setMode", { viewId: "1:sess-1", enabled: true });
		send("browser:annotation:state", 99, annotationSession());
		webContentsListeners.get("did-fail-load")?.(
			{} as never,
			-105 as never,
			"Name not resolved" as never,
			"http://localhost:4173/" as never,
			true as never,
		);

		expect(sent.filter(({ channel }) => channel === "browser:annotation:canceled")).toHaveLength(2);
	});

	it("keeps a top-level draft when a subframe load fails", async () => {
		const { invoke, send, sent, webContents, webContentsListeners } = setupHost();
		await invoke("browser:ensure", "sess-1");
		await invoke("browser:navigate", { viewId: "1:sess-1", url: "http://localhost:4173/" });
		await invoke("browser:annotation:setMode", { viewId: "1:sess-1", enabled: true });
		const session = annotationSession();
		send("browser:annotation:state", 99, session);
		webContents.send.mockClear();

		webContentsListeners.get("did-fail-load")?.(
			{} as never,
			-105 as never,
			"Iframe name not resolved" as never,
			"http://invalid.test/frame" as never,
			false as never,
		);
		webContentsListeners.get("did-stop-loading")?.();

		expect(sent).not.toContainEqual({ channel: "browser:annotation:canceled", payload: expect.anything() });
		expect(webContents.send).toHaveBeenCalledWith("browser:annotation:setMode", { enabled: true, session });
	});

	it("forwards a single-element preview annotation submission to the renderer-owned view", async () => {
		const { invoke, invokeFromTab, sent } = setupHost();
		await enableAnnotation(invoke, (await invoke("browser:ensure", "sess-1")) as BrowserNavState);
		const session = submittedAnnotationSession();

		await invokeFromTab("browser:annotation:submit", 99, { session });

		expect(sent).toContainEqual({
			channel: "browser:annotation:submitted",
			payload: expect.objectContaining({
				viewId: "1:sess-1",
				session,
				snapshot: {
					mimeType: "image/png",
					data: Buffer.from("png-snapshot").toString("base64"),
				},
			}),
		});
	});

	it("completes only the submitted tab and page session when another annotated tab becomes active", async () => {
		const { invoke, invokeFromTab, sendFromTab, sent, views } = setupTabHost();
		const ensured = (await invoke("browser:ensure", "sess-1")) as BrowserNavState;
		await invoke("browser:navigate", { viewId: ensured.viewId, url: "https://a.example.test/" });
		await enableAnnotation(invoke, ensured);
		const sessionA = submittedAnnotationSession("https://a.example.test/");
		sendFromTab("browser:annotation:state", views[0]!.webContents.id, sessionA);

		await invokeFromTab("browser:annotation:submit", views[0]!.webContents.id, { session: sessionA });
		const submitted = sent.find(({ channel }) => channel === "browser:annotation:submitted")
			?.payload as BrowserAnnotationSubmitPayload;
		expect(submitted).toMatchObject({
			viewId: ensured.viewId,
			tabId: "t1",
			pageKey: "https://a.example.test/",
			session: sessionA,
		});
		expect(submitted.sessionToken).toEqual(expect.any(String));

		await invoke("browser:openTab", { viewId: ensured.viewId, url: "https://b.example.test/" });
		const sessionB = submittedAnnotationSession("https://b.example.test/");
		sendFromTab("browser:annotation:state", views[1]!.webContents.id, sessionB);
		await invoke("browser:annotation:setMode", { viewId: ensured.viewId, enabled: true });
		views[1]!.webContents.send.mockClear();
		sent.length = 0;

		await invoke("browser:annotation:complete", {
			viewId: submitted.viewId,
			tabId: submitted.tabId,
			pageKey: submitted.pageKey,
			sessionToken: submitted.sessionToken,
			success: true,
		});

		expect(views[1]!.webContents.send).not.toHaveBeenCalled();
		views[1]!.listeners.get("did-stop-loading")?.();
		expect(sent).toContainEqual({
			channel: "browser:annotation:state",
			payload: { viewId: ensured.viewId, count: 1, screenshotCount: 0, hasDraft: false },
		});

		sent.length = 0;
		await invoke("browser:selectTab", { viewId: ensured.viewId, tabId: "t1" });
		views[0]!.listeners.get("did-stop-loading")?.();
		expect(sent).toContainEqual({
			channel: "browser:annotation:state",
			payload: { viewId: ensured.viewId, count: 0, screenshotCount: 0, hasDraft: false },
		});
	});

	it("resizes a captured snapshot that exceeds the longest-edge cap", async () => {
		const { invoke, invokeFromTab, sent, webContents } = setupHost();
		await enableAnnotation(invoke, (await invoke("browser:ensure", "sess-1")) as BrowserNavState);
		const resize = vi.fn(() => ({ toPNG: () => Buffer.from("resized-png") }));
		webContents.capturePage.mockResolvedValueOnce({
			isEmpty: () => false,
			toJPEG: () => Buffer.from("snapshot"),
			toPNG: () => Buffer.from("full-size-png"),
			getSize: () => ({ width: 2000, height: 1000 }),
			resize,
		});

		await invokeFromTab("browser:annotation:submit", 99, { session: submittedAnnotationSession() });

		expect(resize).toHaveBeenCalledWith({ width: 1568 });
		expect(sent).toContainEqual({
			channel: "browser:annotation:submitted",
			payload: expect.objectContaining({
				snapshot: { mimeType: "image/png", data: Buffer.from("resized-png").toString("base64") },
			}),
		});
	});

	it("forwards without a snapshot when capture exceeds the timeout budget", async () => {
		const { invoke, invokeFromTab, sent, webContents } = setupHost();
		await enableAnnotation(invoke, (await invoke("browser:ensure", "sess-1")) as BrowserNavState);
		// Never resolves: forces the capture-vs-timeout race to resolve via the
		// timeout branch, proving a hung capturePage() cannot block the send.
		webContents.capturePage.mockReturnValueOnce(new Promise(() => undefined));

		await invokeFromTab("browser:annotation:submit", 99, { session: submittedAnnotationSession() });

		const forwarded = sent.find((entry) => entry.channel === "browser:annotation:submitted");
		expect(forwarded).toBeDefined();
		expect((forwarded?.payload as { snapshot?: unknown }).snapshot).toBeUndefined();
	});

	it("drops an in-flight annotation submission after its browser view is destroyed", async () => {
		const { host, invoke, invokeFromTab, sent, webContents } = setupHost();
		await enableAnnotation(invoke, (await invoke("browser:ensure", "sess-1")) as BrowserNavState);
		let releaseCapture!: (image: Awaited<ReturnType<typeof webContents.capturePage>>) => void;
		const capture = new Promise<Awaited<ReturnType<typeof webContents.capturePage>>>((resolve) => {
			releaseCapture = resolve;
		});
		webContents.capturePage.mockReturnValueOnce(capture);

		const submit = invokeFromTab("browser:annotation:submit", 99, {
			session: submittedAnnotationSession(),
		}) as Promise<void>;
		await new Promise<void>((resolve) => setImmediate(resolve));
		host.destroy("1:sess-1");
		releaseCapture({
			isEmpty: () => false,
			toJPEG: () => Buffer.from("snapshot"),
			toPNG: () => Buffer.from("png-snapshot"),
			getSize: () => ({ width: 640, height: 480 }),
			resize: vi.fn(),
		});
		await submit;

		expect(sent.some((entry) => entry.channel === "browser:annotation:submitted")).toBe(false);
	});

	it("forwards a multi-comment annotation session to the renderer-owned view", async () => {
		const { invoke, invokeFromTab, sent } = setupHost();
		await enableAnnotation(invoke, (await invoke("browser:ensure", "sess-1")) as BrowserNavState);
		const session = submittedAnnotationSession("http://localhost:5173/", [
			{ body: "Align this with the next button.", selector: "button#a" },
			{ body: "Use this button as the alignment reference.", selector: "button#b" },
		]);

		await invokeFromTab("browser:annotation:submit", 99, { session });

		expect(sent).toContainEqual({
			channel: "browser:annotation:submitted",
			payload: expect.objectContaining({
				viewId: "1:sess-1",
				session,
			}),
		});
	});

	it("drops a submission from a page the shell never put in annotation mode", async () => {
		const { invoke, invokeFromTab, sent } = setupHost();
		await invoke("browser:ensure", "sess-1");

		await invokeFromTab("browser:annotation:submit", 99, { session: submittedAnnotationSession() });

		expect(sent.find(({ channel }) => channel === "browser:annotation:submitted")).toBeUndefined();
	});

	it("ignores a malformed annotation session instead of forwarding it", async () => {
		const { invoke, invokeFromTab, sent } = setupHost();
		await enableAnnotation(invoke, (await invoke("browser:ensure", "sess-1")) as BrowserNavState);

		await invokeFromTab("browser:annotation:submit", 99, {
			session: { version: 1, page: { url: "http://localhost:5173/" }, annotations: "invalid", screenshots: [] },
		});

		expect(sent.some((entry) => entry.channel === "browser:annotation:submitted")).toBe(false);
	});

	it("ignores an annotation whose context is missing required fields", async () => {
		const { invoke, invokeFromTab, sent } = setupHost();
		await enableAnnotation(invoke, (await invoke("browser:ensure", "sess-1")) as BrowserNavState);
		const session = submittedAnnotationSession();
		(session.annotations[0]!.target.context as { url?: string }).url = undefined;

		await invokeFromTab("browser:annotation:submit", 99, { session });

		expect(sent.some((entry) => entry.channel === "browser:annotation:submitted")).toBe(false);
	});

	it("ignores a multi-comment session containing a malformed annotation", async () => {
		const { invoke, invokeFromTab, sent } = setupHost();
		await enableAnnotation(invoke, (await invoke("browser:ensure", "sess-1")) as BrowserNavState);
		const session = submittedAnnotationSession("http://localhost:5173/", [
			{ body: "First", selector: "button#a" },
			{ body: "Second", selector: "button#b" },
		]);
		(session.annotations as unknown[])[1] = null;

		await invokeFromTab("browser:annotation:submit", 99, { session });

		expect(sent.some((entry) => entry.channel === "browser:annotation:submitted")).toBe(false);
	});

	it("ignores preview annotation events after the view is destroyed", async () => {
		const { host, invoke, send, sent } = setupHost();
		await enableAnnotation(invoke, (await invoke("browser:ensure", "sess-1")) as BrowserNavState);

		host.destroy("1:sess-1");
		send("browser:annotation:cancel", 99, { reason: "escape" });

		expect(sent.some((entry) => entry.channel === "browser:annotation:canceled")).toBe(false);
	});
});


describe("browser snapshot deltas", () => {
	type NativeSnapshot = Record<string, unknown>;
	function setupDeltaHost(respond: (args: Record<string, unknown>) => NativeSnapshot) {
		const runtime = {
			runAction: vi.fn(async (_sessionId: string, action: string, args: Record<string, unknown>) =>
				action === "snapshot"
					? {
							snapshot: respond(args),
							refs: { e1: { role: "button", name: "Save" } },
							_boundary: { nonce: "n1", origin: "http://localhost:3000/" },
						}
					: {},
			),
			closeSession: vi.fn(async () => undefined),
			dispose: vi.fn(async () => undefined),
		} as unknown as import("./agent-browser-runtime/runtime").AgentBrowserRuntime;
		const snapshotCalls = () =>
			(runtime.runAction as unknown as ReturnType<typeof vi.fn>).mock.calls
				.filter((call: unknown[]) => call[1] === "snapshot")
				.map((call: unknown[]) => call[2]);
		return { ...setupHost(runtime), snapshotCalls };
	}
	const full = (revision: number) => ({
		kind: "full",
		revision,
		tree: '- button "Save" [ref=e1]',
		refs: { e1: { role: "button", name: "Save" } },
	});

	it("requests a full baseline first and returns it as snapshot text", async () => {
		const { host, snapshotCalls } = setupDeltaHost(() => full(1));

		const result = await host.execute("sess-1", "snapshot", { delta: true });

		expect(snapshotCalls()).toEqual([{ interactive: false, delta: true, full: true }]);
		expect(result).toEqual({
			kind: "full",
			revision: 1,
			text: '- button "Save" [ref=e1]',
			refs: { e1: { role: "button", name: "Save" } },
			_boundary: { nonce: "n1", origin: "http://localhost:3000/" },
			untrustedExternalContent: true,
		});
	});

	it("returns unchanged and structural deltas measured from the revision the agent last received", async () => {
		const responses: NativeSnapshot[] = [
			full(1),
			{ kind: "unchanged", baseRevision: 1, revision: 2 },
			{
				kind: "delta",
				baseRevision: 2,
				revision: 3,
				changes: [{ op: "remove", ref: "@e1" }],
				treeChange: { startLine: 0, deleteCount: 1, lines: [] },
			},
		];
		const { host, snapshotCalls } = setupDeltaHost(() => responses.shift()!);

		await host.execute("sess-1", "snapshot", { delta: true });
		const unchanged = await host.execute("sess-1", "snapshot", { delta: true });
		const delta = await host.execute("sess-1", "snapshot", { delta: true });

		expect(snapshotCalls().slice(1)).toEqual([
			{ interactive: false, delta: true },
			{ interactive: false, delta: true },
		]);
		expect(unchanged).toMatchObject({ kind: "unchanged", baseRevision: 1, revision: 2 });
		expect(delta).toMatchObject({
			kind: "delta",
			baseRevision: 2,
			revision: 3,
			changes: [{ op: "remove", ref: "@e1" }],
			treeChange: { startLine: 0, deleteCount: 1, lines: [] },
		});
	});

	it("refetches full state when the native baseline moved without the agent seeing it", async () => {
		const responses: NativeSnapshot[] = [full(1), { kind: "unchanged", baseRevision: 2, revision: 3 }, full(4)];
		const { host, snapshotCalls } = setupDeltaHost(() => responses.shift()!);

		await host.execute("sess-1", "snapshot", { delta: true });
		const result = await host.execute("sess-1", "snapshot", { delta: true });

		expect(snapshotCalls().slice(1)).toEqual([
			{ interactive: false, delta: true },
			{ interactive: false, delta: true, full: true },
		]);
		expect(result).toMatchObject({ kind: "full", revision: 4, text: '- button "Save" [ref=e1]' });
	});

	it("forces full state when the agent asks for it or switches the interactive option set", async () => {
		const { host, snapshotCalls } = setupDeltaHost(() => full(1));

		await host.execute("sess-1", "snapshot", { delta: true });
		await host.execute("sess-1", "snapshot", { delta: true, full: true });
		await host.execute("sess-1", "snapshot", { delta: true, interactive: true });

		expect(snapshotCalls()).toEqual([
			{ interactive: false, delta: true, full: true },
			{ interactive: false, delta: true, full: true },
			{ interactive: true, delta: true, full: true },
		]);
	});

	it("forces full state after the browser switches frames", async () => {
		const { host, snapshotCalls } = setupDeltaHost(() => full(1));

		await host.execute("sess-1", "snapshot", { delta: true });
		await host.execute("sess-1", "frame", { target: "main" });
		await host.execute("sess-1", "snapshot", { delta: true });

		expect(snapshotCalls()[1]).toEqual({ interactive: false, delta: true, full: true });
	});

	it("forces full state after the browser switches tabs", async () => {
		const { host, snapshotCalls } = setupDeltaHost(() => full(1));

		await host.execute("sess-1", "snapshot", { delta: true });
		await host.execute("sess-1", "tab-select", { tabId: "t1" });
		await host.execute("sess-1", "snapshot", { delta: true });

		expect(snapshotCalls()[1]).toEqual({ interactive: false, delta: true, full: true });
	});

	it("rejects a forced refetch that still is not full state", async () => {
		const responses: NativeSnapshot[] = [
			full(1),
			{ kind: "unchanged", baseRevision: 2, revision: 3 },
			{ kind: "unchanged", baseRevision: 3, revision: 4 },
		];
		const { host } = setupDeltaHost(() => responses.shift()!);

		await host.execute("sess-1", "snapshot", { delta: true });

		await expect(host.execute("sess-1", "snapshot", { delta: true })).rejects.toMatchObject({
			code: "BROWSER_AUTOMATION_INVALID_OUTPUT",
		});
	});

	it("rejects a partial answer to an explicit --full request", async () => {
		const responses: NativeSnapshot[] = [full(1), { kind: "unchanged", baseRevision: 1, revision: 2 }];
		const { host } = setupDeltaHost(() => responses.shift()!);

		await host.execute("sess-1", "snapshot", { delta: true });

		await expect(host.execute("sess-1", "snapshot", { delta: true, full: true })).rejects.toMatchObject({
			code: "BROWSER_AUTOMATION_INVALID_OUTPUT",
		});
	});

	it("forces full state after the agent takes a plain snapshot, but not after act", async () => {
		const { host, snapshotCalls } = setupDeltaHost((args) =>
			args.delta ? full(1) : ('- button "Save" [ref=e1]' as unknown as NativeSnapshot),
		);

		await host.execute("sess-1", "snapshot", { delta: true });
		await host.execute("sess-1", "act", { instruction: "the Save button" });
		await host.execute("sess-1", "snapshot", { delta: true });
		await host.execute("sess-1", "snapshot", {});
		await host.execute("sess-1", "snapshot", { delta: true });

		const deltaCalls = snapshotCalls().filter((call) => (call as { delta?: boolean }).delta);
		expect(deltaCalls).toEqual([
			{ interactive: false, delta: true, full: true },
			{ interactive: false, delta: true },
			{ interactive: false, delta: true, full: true },
		]);
	});

	it("drops the delta baseline when an unresolved act hands the agent a snapshot", async () => {
		const { host, snapshotCalls } = setupDeltaHost((args) =>
			args.delta ? full(1) : ('- button "Save" [ref=e1]' as unknown as NativeSnapshot),
		);

		await host.execute("sess-1", "snapshot", { delta: true });
		await host.execute("sess-1", "act", { instruction: "nothing matches this instruction" });
		await host.execute("sess-1", "snapshot", { delta: true });

		const deltaCalls = snapshotCalls().filter((call) => (call as { delta?: boolean }).delta);
		expect(deltaCalls.at(-1)).toEqual({ interactive: false, delta: true, full: true });
	});

	it("rejects delta output it cannot interpret", async () => {
		const { host } = setupDeltaHost(() => ({ kind: "delta", revision: "two" }));

		await expect(host.execute("sess-1", "snapshot", { delta: true })).rejects.toMatchObject({
			code: "BROWSER_AUTOMATION_INVALID_OUTPUT",
		});
	});

	it("keeps plain snapshots on the existing full-text contract", async () => {
		const runtime = {
			runAction: vi.fn(async () => ({ snapshot: '- button "Save" [ref=e1]', refs: {} })),
			closeSession: vi.fn(async () => undefined),
			dispose: vi.fn(async () => undefined),
		} as unknown as import("./agent-browser-runtime/runtime").AgentBrowserRuntime;
		const { host } = setupHost(runtime);

		const result = await host.execute("sess-1", "snapshot", { interactive: true });

		expect(runtime.runAction).toHaveBeenCalledWith(
			"sess-1",
			"snapshot",
			{ interactive: true },
			expect.anything(),
			undefined,
		);
		expect(result).toEqual({
			text: '- button "Save" [ref=e1]',
			refs: {},
			target: { tabId: "t1", url: "about:blank", origin: "" },
			untrustedExternalContent: true,
		});
	});
});


describe("browser human pointer and annotated screenshots", () => {
	function setupPointerHost() {
		const runtime = {
			runAction: vi.fn(async () => ({})),
			screenshot: vi.fn(async () => ({
				data: "",
				width: 1,
				height: 1,
				annotations: [{ number: 1, ref: "e1", role: "button", name: "Save" }],
				untrustedExternalContent: true as const,
			})),
			closeSession: vi.fn(async () => undefined),
			dispose: vi.fn(async () => undefined),
		} as unknown as import("./agent-browser-runtime/runtime").AgentBrowserRuntime;
		return { ...setupHost(runtime), runtime };
	}

	it("forwards the human pointer option for clicks and drags only when asked", async () => {
		const { host, runtime, invoke, emit } = setupPointerHost();
		const ensure = (await invoke("browser:ensure", "sess-1")) as { viewId: string };
		emit("browser:setBounds", 1, {
			viewId: ensure.viewId,
			revision: 1,
			rect: { x: 0, y: 0, width: 10, height: 10 },
			visible: true,
		});
		const nativeArgs = () =>
			(runtime.runAction as unknown as ReturnType<typeof vi.fn>).mock.calls.map((call: unknown[]) => call[2]);

		// The first command moves the agent onto its own tab; settle that before counting.
		await host.execute("sess-1", "click", { ref: "e1" });
		const settled = nativeArgs().length;
		await host.execute("sess-1", "click", { ref: "e1" });
		await host.execute("sess-1", "click", { ref: "e1", human: true });
		await host.execute("sess-1", "drag", { ref: "e1", targetRef: "e2", human: true });
		await host.execute("sess-1", "dblclick", { ref: "e1", human: true });

		expect(nativeArgs().slice(settled)).toEqual([
			{ ref: "e1" },
			{ ref: "e1", human: true },
			{ ref: "e1", targetRef: "e2", human: true },
			{ ref: "e1" },
		]);
	});

	it("fails fast for a human pointer action while the Browser panel is hidden", async () => {
		const { host, invoke, emit } = setupPointerHost();
		const ensure = (await invoke("browser:ensure", "sess-1")) as { viewId: string };
		emit("browser:setBounds", 1, {
			viewId: ensure.viewId,
			revision: 1,
			rect: { x: 0, y: 0, width: 10, height: 10 },
			visible: false,
		});

		await expect(host.execute("sess-1", "click", { ref: "e1", human: true })).rejects.toMatchObject({
			code: "BROWSER_PANEL_HIDDEN",
		});
		await expect(host.execute("sess-1", "click", { ref: "e1" })).resolves.toBeDefined();
	});

	it("passes the annotate option to the screenshot runtime and returns its annotations", async () => {
		const { host, runtime } = setupPointerHost();

		const plain = await host.execute("sess-1", "screenshot", {});
		const annotated = await host.execute("sess-1", "screenshot", { annotate: true });

		expect(runtime.screenshot).toHaveBeenNthCalledWith(1, "sess-1", expect.anything(), undefined, { annotate: false });
		expect(runtime.screenshot).toHaveBeenNthCalledWith(2, "sess-1", expect.anything(), undefined, { annotate: true });
		expect(plain).toMatchObject({ width: 1, height: 1 });
		expect(annotated).toMatchObject({ annotations: [{ number: 1, ref: "e1", role: "button", name: "Save" }] });
	});
});

