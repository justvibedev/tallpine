import {
	afterEach,
	describe,
	expect,
	it,
	vi,
} from "vitest";
import { type BrowserNavState, type BrowserTabsState, createBrowserViewHost } from "./browser-view-host";
import {
	InvokeHandler,
	setupHost,
	LifecycleContents,
	setupTabHost,
} from "./browser-view-host-test-helpers";

vi.mock("./render-check-proxy", () => ({ startRenderCheckProxy: vi.fn(async () => 4321) }));

vi.mock("electron", async (importOriginal) => {
	const actual = await importOriginal<typeof import("electron")>();
	return { ...actual, net: { fetch: vi.fn() } };
});


describe("browser:closeTab automation-runtime fallback", () => {
	it.each([1, 2])("accepts a repeated close with %i tabs initially", async (count) => {
		const { invoke, onTabCloseFailure } = setupTabHost();
		const { viewId } = await invoke("browser:ensure", "sess-1") as BrowserNavState;
		const tabId = count === 1 ? "t1" : (await invoke("browser:openTab", { viewId }) as BrowserTabsState).activeTabId;
		await invoke("browser:closeTab", { viewId, tabId });
		expect(await invoke("browser:closeTab", { viewId, tabId })).toMatchObject({ tabs: count === 1 ? [] : [{ id: "t1" }] });
		expect(onTabCloseFailure).not.toHaveBeenCalled();
	});

	it("accepts a runtime error after the requested tab actually closed", async () => {
		const { invoke, runtime, onTabCloseFailure } = setupTabHost();
		const { viewId } = await invoke("browser:ensure", "sess-1") as BrowserNavState;
		await invoke("browser:openTab", { viewId });
		vi.mocked(runtime.runAction).mockImplementation(async (_sessionId, action, _args, provider) => {
			if (action === "tab-close") {
				await provider.closeTarget("t2");
				throw Object.assign(new Error("connection lost"), { code: "AGENT_BROWSER_START_FAILED" });
			}
			return {};
		});
		expect(await invoke("browser:closeTab", { viewId, tabId: "t2" })).toMatchObject({ tabs: [{ id: "t1" }] });
		expect(onTabCloseFailure).toHaveBeenCalledWith(expect.objectContaining({ tabPresent: false, tabCount: 1, error: expect.objectContaining({ code: "AGENT_BROWSER_START_FAILED" }) }));
	});

	it("records a genuine close failure and preserves the error even if diagnostics fail", async () => {
		const { invoke, runtime, onTabCloseFailure } = setupTabHost();
		const { viewId } = await invoke("browser:ensure", "sess-1") as BrowserNavState;
		await invoke("browser:openTab", { viewId });
		const error = Object.assign(new Error("private runtime output"), { code: "AGENT_BROWSER_START_FAILED" });
		vi.mocked(runtime.runAction).mockRejectedValue(error);
		onTabCloseFailure.mockImplementation(() => { throw new Error("disk full"); });
		await expect(invoke("browser:closeTab", { viewId, tabId: "t2" })).rejects.toBe(error);
		expect(onTabCloseFailure).toHaveBeenCalledWith({ error, sessionId: "sess-1", tabId: "t2", tabCount: 2, tabPresent: true, wasActive: true, agentTab: false });
		expect(await invoke("browser:getTabs", viewId)).toMatchObject({ tabs: [{ id: "t1" }, { id: "t2" }] });
	});

	it("starts with zero tabs when the renderer requests an empty browser", async () => {
		const { invoke, views } = setupTabHost();
		const initial = await invoke("browser:ensure", "sess-1", false) as BrowserNavState;
		expect(views).toHaveLength(0);
		expect(await invoke("browser:getTabs", initial.viewId)).toMatchObject({ tabs: [], activeTabId: "" });
		expect(await invoke("browser:ensure", "sess-1", false)).toMatchObject({ url: "" });
		const opened = await invoke("browser:openTab", { viewId: initial.viewId }) as BrowserTabsState;
		expect(opened.tabs).toHaveLength(1);
		expect(views[0].webContents.session.webRequest.onCompleted).toHaveBeenCalledOnce();
	});

	it("creates only one first tab when automation bootstraps an initial page", async () => {
		const { invoke, runtime } = setupTabHost();
		const runAction = vi.mocked(runtime.runAction);
		const original = runAction.getMockImplementation()!;
		runAction.mockImplementation(async (sessionId, action, args, provider, signal) => {
			// The real runtime connects before running the requested command.
			// Connecting to an empty browser creates an initial page of its own.
			if (!provider.listTargets().length) await provider.createTarget("about:blank");
			return original(sessionId, action, args, provider, signal);
		});
		const { viewId } = await invoke("browser:ensure", "sess-1", false) as BrowserNavState;
		const first = await invoke("browser:openTab", { viewId }) as BrowserTabsState;
		expect(first.tabs).toHaveLength(1);
		const second = await invoke("browser:openTab", { viewId }) as BrowserTabsState;
		expect(second.tabs).toHaveLength(2);
		await invoke("browser:closeTab", { viewId, tabId: second.activeTabId });
		await invoke("browser:closeTab", { viewId, tabId: first.activeTabId });
		const reopened = await invoke("browser:openTab", { viewId }) as BrowserTabsState;
		expect(reopened.tabs).toHaveLength(1);
	});

	it("lets the window composition restack after each new page view", async () => {
		const { addChildView, invoke, onNativeViewAdded } = setupTabHost();
		const { viewId } = await invoke("browser:ensure", "sess-1", false) as BrowserNavState;
		await invoke("browser:openTab", { viewId });
		await invoke("browser:openTab", { viewId });
		// A new view is appended above the shell, so the hook must fire after
		// every append; otherwise an open dialog/notice ends up under the page.
		expect(addChildView).toHaveBeenCalledTimes(2);
		expect(onNativeViewAdded).toHaveBeenCalledTimes(2);
		expect(onNativeViewAdded.mock.invocationCallOrder[1]).toBeGreaterThan(addChildView.mock.invocationCallOrder[1]);
	});

	it("opens just one tab for an agent tab-new after empty renderer initialization", async () => {
		const { host, invoke } = setupTabHost();
		const initial = await invoke("browser:ensure", "sess-1", false) as BrowserNavState;
		await host.execute("sess-1", "tab-new", { url: "https://example.test/" });
		expect((await invoke("browser:getTabs", initial.viewId) as BrowserTabsState).tabs).toHaveLength(1);
	});

	it("closes the final page and can create and close a new blank tab", async () => {
		const { invoke, views } = setupTabHost();
		const initial = await invoke("browser:ensure", "sess-1") as BrowserNavState;
		await invoke("browser:navigate", { viewId: initial.viewId, url: "https://example.test/" });
		const before = await invoke("browser:getTabs", initial.viewId) as BrowserTabsState;
		const closed = await invoke("browser:closeTab", { viewId: initial.viewId, tabId: before.activeTabId }) as BrowserTabsState;
		expect(closed).toMatchObject({ tabs: [], activeTabId: "", change: { kind: "closed" } });
		expect(views[0].webContents.close).toHaveBeenCalledOnce();
		expect(views[0].webContents.session.webRequest.onCompleted).toHaveBeenLastCalledWith(null);
		expect(views).toHaveLength(1);
		expect(await invoke("browser:ensure", "sess-1", false)).toMatchObject({ url: "" });
		const opened = await invoke("browser:openTab", { viewId: initial.viewId }) as BrowserTabsState;
		expect(opened.tabs).toHaveLength(1);
		const blankClosed = await invoke("browser:closeTab", { viewId: initial.viewId, tabId: opened.activeTabId }) as BrowserTabsState;
		expect(blankClosed.tabs).toHaveLength(0);
		await invoke("browser:navigate", { viewId: initial.viewId, url: "https://example.test/again" });
		expect((await invoke("browser:getTabs", initial.viewId) as BrowserTabsState).tabs).toHaveLength(1);
	});

	// Regression: observed in a real long-running session as
	// "Tab t5 not found; run `agent-browser tab` to list open tabs" — the
	// external automation runtime's own internal tab registry had drifted from
	// session.tabs, and the close request just threw instead of falling back,
	// leaving the tab permanently stuck open with no way to close it.
	it("closes the tab locally when the automation runtime reports its registry does not recognize it", async () => {
		const { invoke, runtime } = setupTabHost();
		const ensure = (await invoke("browser:ensure", "sess-1")) as { viewId: string };
		const viewId = ensure.viewId;
		await invoke("browser:openTab", { viewId });

		const before = (await invoke("browser:getTabs", viewId)) as { tabs: { id: string }[] };
		expect(before.tabs.map((tab) => tab.id)).toEqual(["t1", "t2"]);

		const runAction = runtime.runAction as unknown as ReturnType<typeof vi.fn>;
		const originalRunAction = runAction.getMockImplementation()! as (...args: unknown[]) => Promise<unknown>;
		runAction.mockImplementation(async (sessionId: string, action: string, args: Record<string, unknown>, provider: unknown) => {
			if (action === "tab-close" && String(args.tabId) === "t2") {
				throw Object.assign(new Error("Tab t2 not found; run `agent-browser tab` to list open tabs"), {
					code: "AGENT_BROWSER_COMMAND_FAILED",
				});
			}
			return originalRunAction(sessionId, action, args, provider);
		});

		const result = (await invoke("browser:closeTab", { viewId, tabId: "t2" })) as { tabs: { id: string }[] };
		expect(result.tabs.map((tab) => tab.id)).toEqual(["t1"]);
	});

	// Regression, reported live: the runtime's Target.closeTarget handling
	// (invoked from inside runAction) can call Tallpine's own internal closeTab
	// before the runtime still reports the overall tab-close command as
	// failed. The fallback used to call closeTab a second time regardless,
	// which threw TAB_NOT_FOUND for a tab that had already, genuinely closed —
	// the exact outcome the user wanted, reported as an error.
	it("treats a tab-close as successful when the runtime already removed the tab before reporting failure", async () => {
		const { invoke, runtime } = setupTabHost();
		const ensure = (await invoke("browser:ensure", "sess-1")) as { viewId: string };
		const viewId = ensure.viewId;
		await invoke("browser:openTab", { viewId });

		const runAction = runtime.runAction as unknown as ReturnType<typeof vi.fn>;
		runAction.mockImplementation(async (_sessionId: string, action: string, args: Record<string, unknown>, provider: import("./agent-browser-cdp-bridge").AgentBrowserTargetProvider) => {
			if (action === "tab-close" && String(args.tabId) === "t2") {
				// The bridge's own Target.closeTarget calls this before reporting
				// the command as failed — mirrors the real, observed sequence.
				await provider.closeTarget("t2");
				throw Object.assign(new Error("agent-browser lost the connection mid-command"), {
					code: "AGENT_BROWSER_COMMAND_FAILED",
				});
			}
			return {};
		});

		const result = (await invoke("browser:closeTab", { viewId, tabId: "t2" })) as { tabs: { id: string }[] };
		expect(result.tabs.map((tab) => tab.id)).toEqual(["t1"]);
	});

	it("still surfaces an unrelated automation-runtime failure instead of silently closing the tab", async () => {
		const { invoke, runtime } = setupTabHost();
		const ensure = (await invoke("browser:ensure", "sess-1")) as { viewId: string };
		const viewId = ensure.viewId;
		await invoke("browser:openTab", { viewId });

		const runAction = runtime.runAction as unknown as ReturnType<typeof vi.fn>;
		const originalRunAction = runAction.getMockImplementation()! as (...args: unknown[]) => Promise<unknown>;
		runAction.mockImplementation(async (sessionId: string, action: string, args: Record<string, unknown>, provider: unknown) => {
			if (action === "tab-close" && String(args.tabId) === "t2") {
				throw Object.assign(new Error("agent-browser exited with code 1"), {
					code: "AGENT_BROWSER_START_FAILED",
				});
			}
			return originalRunAction(sessionId, action, args, provider);
		});

		await expect(invoke("browser:closeTab", { viewId, tabId: "t2" })).rejects.toThrow("agent-browser exited with code 1");

		const after = (await invoke("browser:getTabs", viewId)) as { tabs: { id: string }[] };
		expect(after.tabs.map((tab) => tab.id)).toEqual(["t1", "t2"]);
	});
});


describe("browser:openTab navigation failure", () => {
	// Regression, reported with a real repro against reopening a closed tab
	// pointed at a stopped dev server: openTab used to throw NAVIGATION_FAILED
	// after already creating the tab and telling the renderer about it
	// (pushTabsState), so a dead URL rejected an IPC call for a tab that
	// demonstrably existed. navigateEntry — the same navigation codepath used
	// for an *existing* tab — never throws on a failed load; it just sets
	// `.error` on the nav state and resolves. openTab now matches that.
	it("does not reject when the new tab's initial navigation fails — the tab still exists", async () => {
		const { failNavigationTo, invoke } = setupTabHost();
		const ensure = (await invoke("browser:ensure", "sess-1")) as { viewId: string };
		const viewId = ensure.viewId;

		failNavigationTo.add("http://localhost:5175/");
		const result = (await invoke("browser:openTab", { viewId, url: "http://localhost:5175/" })) as {
			tabs: { id: string }[];
		};

		expect(result.tabs.map((tab) => tab.id)).toEqual(["t1", "t2"]);
	});
});


describe("ensureNativeActiveTab automation-runtime resync", () => {
	it("resynchronizes after a failed explicit selection changes the native target", async () => {
		const { activeTargets, host, invoke, runtime } = setupTabHost();
		// Agent-owned tabs: in a temporary session the agent never acts on the person's own tabs.
		await host.execute("sess-1", "tabs");
		await host.execute("sess-1", "tab-new");
		await invoke("browser:ensure", "sess-1");
		const runAction = runtime.runAction as unknown as ReturnType<typeof vi.fn>;
		const originalRunAction = runAction.getMockImplementation()! as (...args: unknown[]) => Promise<unknown>;
		const pageTargets: string[] = [];
		runAction.mockImplementation(async (sessionId: string, action: string, args: Record<string, unknown>, provider: unknown) => {
			if (action === "tab-select" && args.tabId === "t1") {
				activeTargets.set(sessionId, "t1");
				throw Object.assign(new Error("Returned target identity did not match"), { code: "AGENT_BROWSER_COMMAND_FAILED" });
			}
			if (action === "get") pageTargets.push(activeTargets.get(sessionId) ?? "");
			return originalRunAction(sessionId, action, args, provider);
		});

		await expect(host.execute("sess-1", "tab-select", { tabId: "t1" })).rejects.toMatchObject({
			code: "BROWSER_TARGET_MISMATCH",
		});
		expect(activeTargets.get("sess-1")).toBe("t1");
		await host.execute("sess-1", "get", { property: "url" });
		expect(pageTargets).toEqual(["t2"]);
	});

	// Regression, reported against the reopen-closed-tabs PR with a real repro
	// log: the runtime's own tab registry drifted from session.tabs, and
	// ensureNativeActiveTab's convergence loop just retried the identical
	// failing tab-select forever, since re-selecting the same tabId can never
	// succeed once the runtime has forgotten it. Every native browser
	// operation for the session routes through this loop (select, close,
	// click, snapshot, ...), so that permanently wedged the whole session, not
	// just whichever call happened to trigger it first — browser:closeTab's
	// own try/catch never even ran, because the throw came from its unguarded
	// ensureNativeActiveTab call sitting *before* that try block.
	it("recovers browser:selectTab when the runtime rejects tab-select for the newly active tab", async () => {
		const { invoke, runtime } = setupTabHost();
		const ensure = (await invoke("browser:ensure", "sess-1")) as { viewId: string };
		const viewId = ensure.viewId;
		await invoke("browser:openTab", { viewId }); // t1, t2 — t2 active, natively synced

		const runAction = runtime.runAction as unknown as ReturnType<typeof vi.fn>;
		const originalRunAction = runAction.getMockImplementation()! as (...args: unknown[]) => Promise<unknown>;
		const seenActions: string[] = [];
		let failNextSelect = true;
		runAction.mockImplementation(async (sessionId: string, action: string, args: Record<string, unknown>, provider: unknown) => {
			seenActions.push(action);
			if (action === "tab-select" && String(args.tabId) === "t1" && failNextSelect) {
				failNextSelect = false;
				throw Object.assign(new Error("Tab t1 not found; run `agent-browser tab` to list open tabs"), {
					code: "AGENT_BROWSER_COMMAND_FAILED",
				});
			}
			return originalRunAction(sessionId, action, args, provider);
		});

		const result = (await invoke("browser:selectTab", { viewId, tabId: "t1" })) as { activeTabId: string };
		expect(result.activeTabId).toBe("t1");
		// Asked the runtime to refresh its own view (exactly what its error
		// message suggests) before retrying, rather than giving up immediately.
		expect(seenActions).toContain("tabs");
	});

	it("does not wedge the session after a transient tab-select failure — later operations still work", async () => {
		const { invoke, runtime } = setupTabHost();
		const ensure = (await invoke("browser:ensure", "sess-1")) as { viewId: string };
		const viewId = ensure.viewId;
		await invoke("browser:openTab", { viewId }); // t1, t2 — t2 active, natively synced

		const runAction = runtime.runAction as unknown as ReturnType<typeof vi.fn>;
		const originalRunAction = runAction.getMockImplementation()! as (...args: unknown[]) => Promise<unknown>;
		let failNextSelect = true;
		runAction.mockImplementation(async (sessionId: string, action: string, args: Record<string, unknown>, provider: unknown) => {
			if (action === "tab-select" && String(args.tabId) === "t1" && failNextSelect) {
				failNextSelect = false;
				throw Object.assign(new Error("Tab t1 not found; run `agent-browser tab` to list open tabs"), {
					code: "AGENT_BROWSER_COMMAND_FAILED",
				});
			}
			return originalRunAction(sessionId, action, args, provider);
		});

		await invoke("browser:selectTab", { viewId, tabId: "t1" });
		// If the loop were still wedged, this would hang/reject instead of
		// closing — the bug's whole symptom was "works for a while, then every
		// later close/select fails identically forever."
		const result = (await invoke("browser:closeTab", { viewId, tabId: "t2" })) as { tabs: { id: string }[] };
		expect(result.tabs.map((tab) => tab.id)).toEqual(["t1"]);
	});

	// Regression for #4705: after the refresh-and-retry also failed, Tallpine used to
	// mark t1 synchronized anyway. The next click then ran on the runtime's stale
	// t2 target even though Tallpine reported t1 active.
	it("fails closed after a native target resync fails without mutating the stale tab", async () => {
		const { activeTargets, host, invoke, runtime, views } = setupTabHost();
		// Agent-owned tabs: in a temporary session the agent never acts on the person's own tabs.
		await host.execute("sess-1", "tabs");
		await host.execute("sess-1", "tab-new"); // t1, t2 — t2 active, natively synced
		const { viewId } = (await invoke("browser:ensure", "sess-1")) as { viewId: string };

		const runAction = runtime.runAction as unknown as ReturnType<typeof vi.fn>;
		const dispatchedPageActions: string[] = [];
		runAction.mockImplementation(async (_sessionId: string, action: string, args: Record<string, unknown>) => {
			if (action === "tab-select" && String(args.tabId) === "t1") {
				throw Object.assign(new Error("Tab t1 not found; run `agent-browser tab` to list open tabs"), {
					code: "AGENT_BROWSER_COMMAND_FAILED",
				});
			}
			if (action === "click" || action === "get") dispatchedPageActions.push(action);
			return {};
		});

		await expect(invoke("browser:selectTab", { viewId, tabId: "t1" })).rejects.toMatchObject({
			code: "BROWSER_TARGET_MISMATCH",
			message: "Browser automation could not target Tallpine tab t1",
		});
		await expect(host.execute("sess-1", "click", { ref: "e1" })).rejects.toMatchObject({
			code: "BROWSER_TARGET_MISMATCH",
		});
		await expect(host.execute("sess-1", "get", { property: "url" })).rejects.toMatchObject({
			code: "BROWSER_TARGET_MISMATCH",
		});

		expect(dispatchedPageActions).toEqual([]);
		expect(activeTargets.get("sess-1")).toBe("t2");
		const repeated = (await invoke("browser:selectTab", { viewId, tabId: "t1" }).catch((error) => error)) as Error;
		expect(repeated.message).not.toContain("agent-browser");

		const closed = (await invoke("browser:closeTab", { viewId, tabId: "t1" })) as {
			activeTabId: string;
			tabs: Array<{ id: string }>;
		};
		expect(closed.activeTabId).toBe("t2");
		expect(closed.tabs.map((tab) => tab.id)).toEqual(["t2"]);
		expect(views[0].webContents.close).toHaveBeenCalledOnce();
		expect(views[1].webContents.close).not.toHaveBeenCalled();
		expect(runAction.mock.calls.some(([, action]) => action === "tab-close")).toBe(false);
	});

	it("synchronizes screenshots and fails before capture when the popup target cannot be selected", async () => {
		const { host, runtime, views } = setupTabHost();
		const runAction = runtime.runAction as unknown as ReturnType<typeof vi.fn>;
		const originalRunAction = runAction.getMockImplementation()! as (...args: unknown[]) => Promise<unknown>;
		let failedPopupSelects = 0;
		runAction.mockImplementation(
			async (sessionId: string, action: string, args: Record<string, unknown>, provider: unknown, signal?: AbortSignal) => {
				if (action === "tab-select" && args.tabId === "t2") {
					failedPopupSelects += 1;
					throw Object.assign(new Error("Tab t2 not found; run `agent-browser tab` to list open tabs"), {
						code: "AGENT_BROWSER_COMMAND_FAILED",
					});
				}
				return originalRunAction(sessionId, action, args, provider, signal);
			},
		);

		await host.execute("sess-1", "tabs");
		views[0].webContents.openWindow("https://popup.example.test/");
		await vi.waitFor(() => expect(failedPopupSelects).toBeGreaterThanOrEqual(2));

		await expect(host.execute("sess-1", "screenshot")).rejects.toMatchObject({
			code: "BROWSER_TARGET_MISMATCH",
		});
		expect(runtime.screenshot).not.toHaveBeenCalled();
	});
});


describe("native Chromium DevTools host", () => {
	it("uses Chromium's native DevTools surface without embedding a second preview", async () => {
		const { emit, invoke, mainContentView, openDevTools, closeDevTools, view } = setupHost();
		const nav = await invoke("browser:ensure", "sess-1");
		const viewId = (nav as BrowserNavState).viewId;
		await invoke("browser:navigate", { viewId, url: "http://localhost:3000/" });
		emit("browser:setBounds", 1, { viewId, revision: 1, rect: { x: 20, y: 30, width: 700, height: 500 }, visible: true });

		const opened = await invoke("browser:devtools", { viewId, operation: "open" });
		expect(opened).toMatchObject({ open: true, placement: "right" });
		expect(openDevTools).toHaveBeenCalledWith({ mode: "right", activate: true });
		expect(mainContentView.addChildView).toHaveBeenCalledTimes(1);
		expect(mainContentView.addChildView).toHaveBeenCalledWith(view);

		const bottom = await invoke("browser:devtools", {
			viewId,
			operation: "setPlacement",
			placement: "bottom",
		});
		expect(bottom).toMatchObject({ placement: "bottom" });
		expect(closeDevTools).toHaveBeenCalledOnce();
		expect(openDevTools).toHaveBeenLastCalledWith({ mode: "bottom", activate: false });

		await invoke("browser:devtools", { viewId, operation: "setPlacement", placement: "undocked" });
		expect(openDevTools).toHaveBeenLastCalledWith({ mode: "undocked", activate: true });
	});

	it("does not open DevTools for a blank browser target", async () => {
		const { invoke, openDevTools } = setupHost();
		const nav = await invoke("browser:ensure", "sess-1");
		const viewId = (nav as BrowserNavState).viewId;

		const state = await invoke("browser:devtools", { viewId, operation: "open" });

		expect(state).toMatchObject({ open: false, placement: "right" });
		expect(openDevTools).not.toHaveBeenCalled();
	});

	it("reflects a manual native DevTools close in the browser toolbar state", async () => {
		const { invoke, sent, webContentsListeners } = setupHost();
		const nav = await invoke("browser:ensure", "sess-1");
		const viewId = (nav as BrowserNavState).viewId;
		await invoke("browser:navigate", { viewId, url: "http://localhost:3000/" });

		await invoke("browser:devtools", { viewId, operation: "open" });
		expect(sent.filter((entry) => entry.channel === "browser:devtoolsState").at(-1)?.payload).toMatchObject({
			viewId,
			open: true,
		});

		// The first close event after a placement change belongs to our own
		// close/reopen cycle, not to a user clicking the DevTools window's X.
		await invoke("browser:devtools", { viewId, operation: "setPlacement", placement: "bottom" });
		webContentsListeners.get("devtools-closed")?.();
		expect(sent.filter((entry) => entry.channel === "browser:devtoolsState").at(-1)?.payload).toMatchObject({
			viewId,
			open: true,
		});

		// A subsequent close is the user's actual close and must immediately
		// switch the renderer button back to "Open DevTools".
		webContentsListeners.get("devtools-closed")?.();
		expect(sent.filter((entry) => entry.channel === "browser:devtoolsState").at(-1)?.payload).toMatchObject({
			viewId,
			open: false,
		});
	});

	it("closes DevTools when the active page is cleared", async () => {
		const { closeDevTools, invoke } = setupHost();
		const nav = await invoke("browser:ensure", "sess-1");
		const viewId = (nav as BrowserNavState).viewId;
		await invoke("browser:navigate", { viewId, url: "http://localhost:3000/" });
		await invoke("browser:devtools", { viewId, operation: "open" });

		await invoke("browser:clear", viewId);

		expect(closeDevTools).toHaveBeenCalledOnce();
		expect(await invoke("browser:devtools", { viewId, operation: "open" })).toMatchObject({ open: false });
	});

	it("closes DevTools when switching to a blank tab", async () => {
		const { invoke, views } = setupTabHost();
		const nav = (await invoke("browser:ensure", "sess-1")) as BrowserNavState;
		await invoke("browser:navigate", { viewId: nav.viewId, url: "http://localhost:3000/" });
		await invoke("browser:devtools", { viewId: nav.viewId, operation: "open" });

		await invoke("browser:openTab", { viewId: nav.viewId });

		expect(views[0].webContents.closeDevTools).toHaveBeenCalledOnce();
		expect(views[1].webContents.openDevTools).not.toHaveBeenCalled();
		expect(await invoke("browser:devtools", { viewId: nav.viewId, operation: "open" })).toMatchObject({ open: false });
	});

});


describe("browser:clear", () => {
	it("loads about:blank and reports it as an empty url (cleared state)", async () => {
		const { invoke, webContents } = setupHost();
		await invoke("browser:ensure", "sess-1");
		await invoke("browser:navigate", { viewId: "1:sess-1", url: "http://localhost:3000/" });

		const state = await invoke("browser:clear", "1:sess-1");

		expect(webContents.loadURL).toHaveBeenLastCalledWith("about:blank");
		expect(state.url).toBe("");
	});
});


describe("native browser visibility", () => {
	it("shows Tallpine's empty state while keeping an initialized blank target alive", async () => {
		const { emit, invoke, view } = setupHost();
		await invoke("browser:ensure", "sess-1");

		emit("browser:setBounds", 1, {
			viewId: "1:sess-1",
			revision: 1,
			rect: { x: 10, y: 20, width: 320, height: 240 },
			visible: true,
		});
		expect(view.setBounds).toHaveBeenLastCalledWith({ x: 10, y: 20, width: 320, height: 240 });
		expect(view.setVisible).toHaveBeenLastCalledWith(false);

		await invoke("browser:navigate", { viewId: "1:sess-1", url: "http://localhost:3000" });
		expect(view.setVisible).toHaveBeenLastCalledWith(true);
	});

	// Regression, reported live (macOS, maximized/popped-out panel): opening a
	// toolbar dropdown over the browser blanked the live page to black.
	// window-composition.ts documents the same class of bug for its own shell
	// view — re-adding a view to reorder it can leave its *previous*
	// compositor surface on screen until a real geometry change rebuilds it.
	// Visibility must restore on the next tick with the bounds nudge: a
	// same-turn hide/show can coalesce into a no-op and leave the page blank
	// for the whole overlay lifetime.
	it("hides immediately, then restores visibility with bounds on the next tick", async () => {
		const { emit, host, invoke, view } = setupHost();
		await invoke("browser:ensure", "sess-1");
		emit("browser:setBounds", 1, {
			viewId: "1:sess-1",
			revision: 1,
			rect: { x: 10, y: 20, width: 320, height: 240 },
			visible: true,
		});
		await invoke("browser:navigate", { viewId: "1:sess-1", url: "http://localhost:3000" });
		view.setBounds.mockClear();
		view.setVisible.mockClear();

		host.refreshLastFocusedPanelSurface();
		expect(view.setBounds).toHaveBeenLastCalledWith({ x: 10, y: 20, width: 320, height: 239 });
		expect(view.setVisible.mock.calls).toEqual([[false]]);

		await new Promise((resolve) => setTimeout(resolve, 0));
		expect(view.setBounds).toHaveBeenLastCalledWith({ x: 10, y: 20, width: 320, height: 240 });
		expect(view.setVisible.mock.calls).toEqual([[false], [true]]);
	});

	it("does nothing when nothing has been focused yet, or the panel is hidden", async () => {
		const { host, view } = setupHost();
		host.refreshLastFocusedPanelSurface();
		expect(view.setBounds).not.toHaveBeenCalled();
	});

	it("keeps rounded native geometry across page zoom", async () => {
		const { emit, invoke, view } = setupHost();
		await invoke("browser:ensure", "sess-1");

		emit("browser:setBounds", 1.25, {
			viewId: "1:sess-1",
			revision: 1,
			rect: { x: 100.25, y: 20.25, width: 319.5, height: 239.5 },
			visible: true,
		});

		expect(view.setBounds).toHaveBeenLastCalledWith({ x: 125, y: 25, width: 399, height: 299 });
	});
});


describe("browser tab lifecycle stress (tabs stop closing regression guard)", () => {
	const stressTabCount = 16;
	// Re-verifies the historically reported "tabs stop closing" bug against the
	// REAL closeTab/destroyTabView code paths (not a mock of them), by driving
	// the IPC handlers exactly as the renderer rail does: open many tabs,
	// close back down to one, repeatedly, interleaving tab selection plus a
	// DevTools open/close and an annotation-mode toggle each cycle -- the two
	// failure modes ("wrong tab content after switching", "stale overlay
	// captures") called out by the stabilization commits already on main.
	it("opens many tabs and closes back to one across churn cycles without a stuck or leaked tab", async () => {
		const { invoke, views } = setupTabHost();
		const ensured = (await invoke("browser:ensure", "sess-1")) as BrowserNavState;
		const { viewId } = ensured;

		// Counts closes that were made to hit closeTab's "wasActive"
		// reactivation branch (browser-view-host.ts:696 -- the path that calls
		// activateTab/applySessionBounds/pushNavState/pushDevToolsState/
		// retargetDevTools) versus its plain non-active-tab deletion branch, so
		// the final assertion has real evidence both paths actually ran.
		//
		// These counters are ground truth BY CONSTRUCTION, not by inference:
		// each iteration below explicitly selects a tab X and then either closes
		// that same X (wasActive true by direct equality, tabId ===
		// activeTabId) or closes a different Y (wasActive false by direct
		// inequality). Which id we pass to closeTab is exactly what determines
		// the branch -- there is nothing to derive from closeTab's internal
		// reactivation-promotes-the-tail behavior, unlike a prior version of
		// this test that tried to infer wasActive from close order/parity and,
		// by induction on that promotion behavior, ended up hitting the
		// reactivation branch on every single close.
		let reactivatingCloses = 0;
		let nonReactivatingCloses = 0;

		for (let cycle = 0; cycle < 20; cycle += 1) {
			for (let i = 0; i < stressTabCount - 1; i += 1) {
				await invoke("browser:openTab", { viewId });
			}
			let tabs = (await invoke("browser:getTabs", viewId)) as BrowserTabsState;
			expect(tabs.tabs).toHaveLength(stressTabCount);

			// Interleave the two related failure modes named in the brief: a
			// DevTools open/close cycle and an annotation-mode toggle.
			await invoke("browser:devtools", { viewId, operation: "open" });
			await invoke("browser:devtools", { viewId, operation: "close" });
			await invoke("browser:annotation:setMode", { viewId, enabled: true });
			await invoke("browser:annotation:setMode", { viewId, enabled: false });

			// Re-fetch (rather than reusing the pre-interleave `tabs` snapshot)
			// so any corruption introduced by the DevTools/annotation-mode calls
			// above surfaces here directly, instead of only indirectly on the
			// next close below.
			tabs = (await invoke("browser:getTabs", viewId)) as BrowserTabsState;
			expect(tabs.tabs).toHaveLength(stressTabCount);

			let closeCount = 0;
			while (tabs.tabs.length > 1) {
				const before = tabs.tabs.length;
				// Always select a tab first (X), fetched fresh from the current
				// `tabs.tabs` list rather than a remembered index or position,
				// since positions shift as tabs close. Then, by construction:
				//   - reactivating close: close that SAME id X.
				//   - non-reactivating close: close a DIFFERENT id Y != X that
				//     was not the one just selected.
				// Each branch is verifiable by inspection right here -- no
				// induction over closeTab's internal promotion behavior required.
				const wantsReactivation = closeCount % 2 === 0;
				const selectedId = tabs.tabs[0].id;
				await invoke("browser:selectTab", { viewId, tabId: selectedId });
				const closingId = wantsReactivation
					? selectedId
					: tabs.tabs.find((tab) => tab.id !== selectedId)!.id;

				const result = (await invoke("browser:closeTab", { viewId, tabId: closingId })) as BrowserTabsState;

				expect(result.tabs.some((tab) => tab.id === closingId)).toBe(false);
				expect(result.tabs).toHaveLength(before - 1);
				// The reported active tab must always be one that's still present
				// -- a broken reactivation would otherwise pass silently, as it
				// did before this assertion existed.
				expect(result.tabs.some((tab) => tab.id === result.activeTabId)).toBe(true);
				if (wantsReactivation) {
					reactivatingCloses += 1;
				} else {
					nonReactivatingCloses += 1;
				}

				tabs = result;
				closeCount += 1;
			}

			expect(tabs.tabs).toHaveLength(1);
			expect((await invoke("browser:getTabs", viewId)) as BrowserTabsState).toMatchObject({ tabs: [{ id: tabs.tabs[0].id }] });
		}

		// Both branches must have real coverage -- these counters are ground
		// truth by construction (see the comment above the loop), not derived
		// from closeTab's behavior, so this assertion can't pass by accident
		// the way the prior induction-based version did. The thresholds are
		// deliberately loose (the actual split is a deterministic 160
		// reactivating / 140 non-reactivating for 16 tabs across 20
		// cycles) since what matters is that neither branch has zero coverage.
		expect(reactivatingCloses).toBeGreaterThanOrEqual(5);
		expect(nonReactivatingCloses).toBeGreaterThanOrEqual(5);

		const final = (await invoke("browser:getTabs", viewId)) as BrowserTabsState;
		expect(final.tabs).toHaveLength(1);
		// Every WebContentsView ever created (16 tabs x 20 cycles, plus the
		// original) must have had its underlying webContents closed except the
		// one tab still alive at the end -- otherwise a leaked view is exactly
		// what "tabs stop closing" would look like under the hood.
		const stillOpen = views.filter((view) => view.webContents.close.mock.calls.length === 0);
		expect(stillOpen).toHaveLength(1);
	});
});


describe("browser:setBounds", () => {
	it("keeps the newest geometry across 200 rapid updates and acknowledges only applied revisions", async () => {
		const { emit, invoke, shellSend, view } = setupHost();
		await invoke("browser:ensure", "sess-1");
		view.setBounds.mockClear();
		shellSend.mockClear();

		for (let revision = 1; revision <= 200; revision += 1) {
			emit("browser:setBounds", 1, {
				viewId: "1:sess-1",
				revision,
				rect: { x: revision, y: 20, width: 320, height: 240 },
				visible: true,
			});
		}
		emit("browser:setBounds", 1, {
			viewId: "1:sess-1",
			revision: 100,
			rect: { x: 999, y: 20, width: 320, height: 240 },
			visible: true,
		});

		expect(view.setBounds).toHaveBeenLastCalledWith({ x: 200, y: 20, width: 320, height: 240 });
		const acknowledgements = shellSend.mock.calls.filter(([channel]) => channel === "browser:boundsApplied");
		expect(acknowledgements).toHaveLength(200);
		expect(acknowledgements.at(-1)?.[1]).toEqual({
			viewId: "1:sess-1",
			revision: 200,
			rect: { x: 200, y: 20, width: 320, height: 240 },
			visible: true,
		});
	});

	it("converts page-zoomed renderer slot bounds before positioning the native view", async () => {
		const { emit, invoke, view } = setupHost();
		await invoke("browser:ensure", "sess-1");
		view.setBorderRadius.mockClear();

		emit("browser:setBounds", 1.25, {
			viewId: "1:sess-1",
			revision: 1,
			rect: { x: 100, y: 20, width: 320, height: 240 },
			visible: true,
		});

		expect(view.setBounds).toHaveBeenLastCalledWith({ x: 125, y: 25, width: 400, height: 300 });
		expect(view.setBorderRadius).toHaveBeenLastCalledWith(10);
		expect(view.setBounds.mock.invocationCallOrder.at(-1)).toBeLessThan(
			view.setBorderRadius.mock.invocationCallOrder.at(-1)!,
		);
		expect(view.setVisible).toHaveBeenLastCalledWith(false);
	});

	it("does not let a hidden page navigation grant native visibility", async () => {
		const { emit, invoke, view, webContentsListeners } = setupHost();
		await invoke("browser:ensure", "sess-1");

		emit("browser:setBounds", 1, {
			viewId: "1:sess-1",
			revision: 1,
			rect: { x: 100, y: 20, width: 320, height: 240 },
			visible: true,
		});
		expect(view.setVisible).toHaveBeenLastCalledWith(false);
		emit("browser:setBounds", 1, {
			viewId: "1:sess-1",
			revision: 2,
			rect: { x: 0, y: 0, width: 0, height: 0 },
			visible: false,
		});

		view.setBounds.mockClear();
		view.setVisible.mockClear();
		webContentsListeners.get("did-navigate")?.();

		expect(view.setVisible).not.toHaveBeenCalledWith(true);
		expect(view.setVisible).toHaveBeenLastCalledWith(false);
	});

	it("restores renderer-owned bounds when reloading after a failed load", async () => {
		const { emit, invoke, view, webContents, webContentsListeners } = setupHost();
		await invoke("browser:ensure", "sess-1");
		emit("browser:setBounds", 1, {
			viewId: "1:sess-1",
			revision: 1,
			rect: { x: 100, y: 20, width: 320, height: 240 },
			visible: true,
		});

		webContentsListeners.get("did-fail-load")?.(
			{} as never,
			-105 as never,
			"Name not resolved" as never,
			"http://localhost:4173/" as never,
			true as never,
		);
		expect(view.setVisible).toHaveBeenLastCalledWith(false);

		view.setBounds.mockClear();
		view.setVisible.mockClear();
		await invoke("browser:reload", "1:sess-1");

		expect(webContents.reload).toHaveBeenCalled();
		expect(view.setBounds).toHaveBeenLastCalledWith({ x: 100, y: 20, width: 320, height: 240 });
		expect(view.setVisible).toHaveBeenLastCalledWith(false);
	});

	it("does not hide the page when a subresource fails to load", async () => {
		const { invoke, sent, view, webContentsListeners } = setupHost();
		await invoke("browser:ensure", "sess-1");
		view.setVisible.mockClear();
		sent.length = 0;

		webContentsListeners.get("did-fail-load")?.(
			{} as never,
			-118 as never,
			"Connection timed out" as never,
			"https://third-party.example/script.js" as never,
			false as never,
		);

		expect(view.setVisible).not.toHaveBeenCalled();
		expect(sent).not.toContainEqual(expect.objectContaining({ channel: "browser:navState" }));
	});
});


describe("dispose after the window is destroyed", () => {
	it.each(["live", "already destroyed"])("closes %s tab contents without touching the destroyed window", async (contentsState) => {
		const handlers = new Map<string, InvokeHandler>();
		const view = {
			webContents: {
				canGoBack: () => false,
				canGoForward: () => false,
				clearHistory: () => undefined,
				getTitle: () => "",
				getURL: () => "",
				goBack: () => undefined,
				goForward: () => undefined,
				isLoading: () => false,
				loadURL: async () => undefined,
				on: () => undefined,
				reload: () => undefined,
				send: () => undefined,
				setWindowOpenHandler: () => undefined,
				stop: () => undefined,
				close: vi.fn(() => {
					if (contentsState === "already destroyed") throw new Error("Object has been destroyed");
				}),
			},
			setBounds: () => undefined,
			setVisible: () => undefined,
		};
		let destroyed = false;
		const removeChildView = vi.fn(() => {
			throw new Error("Object has been destroyed");
		});
		const host = createBrowserViewHost({
			mainWindow: {
				get contentView() {
					if (destroyed) throw new Error("Object has been destroyed");
					return { addChildView: () => undefined, removeChildView };
				},
				getContentBounds: () => ({ x: 0, y: 0, width: 800, height: 600 }),
				webContents: { id: 1, send: () => undefined },
				isDestroyed: () => destroyed,
			} as never,
			ipcMain: {
				handle: (channel: string, fn: InvokeHandler) => handlers.set(channel, fn),
				on: () => undefined,
				removeHandler: () => undefined,
				off: () => undefined,
			} as never,
			shell: { openExternal: async () => undefined },
			WebContentsView: function () {
				return view;
			} as never,
			annotatePreloadPath: "/preload.js",
			rendererOrigin: "http://localhost:5173",
		});
		await (handlers.get("browser:ensure")!({ sender: { id: 1 } }, "sess-1") as Promise<unknown>);

		destroyed = true; // window "closed" fired

		await expect(host.dispose()).resolves.toBeUndefined();
		expect(removeChildView).not.toHaveBeenCalled();
		expect(view.webContents.close).toHaveBeenCalledOnce();
	});

	it("deduplicates host disposal while runtime cleanup is in flight", async () => {
		let release!: () => void;
		const runtimeDispose = new Promise<void>((resolve) => {
			release = resolve;
		});
		const runtime = {
			runAction: vi.fn(async () => ({})),
			screenshot: vi.fn(async () => ({
				data: "",
				width: 1,
				height: 1,
				untrustedExternalContent: true as const,
			})),
			closeSession: vi.fn(async () => undefined),
			dispose: vi.fn(() => runtimeDispose),
		} as unknown as import("./agent-browser-runtime/runtime").AgentBrowserRuntime;
		const { host } = setupHost(runtime);

		const first = host.dispose();
		const second = host.dispose();
		expect(second).toBe(first);
		expect(runtime.dispose).toHaveBeenCalledTimes(1);
		release();
		await first;
	});
});


describe("browser tab lifecycle", () => {
	const panelRect = { x: 300, y: 40, width: 480, height: 520 };
	const hiddenRect = { x: 0, y: 0, width: 0, height: 0 };
	const limits = { discardAfterMs: 10 * 60_000, maxLoadedHiddenTabs: 3, minHiddenBeforeCapDiscardMs: 60_000 };
	const contentsOf = (view: { webContents: unknown }) => view.webContents as unknown as LifecycleContents;
	const lastThrottle = (view: { webContents: unknown }) => contentsOf(view).setBackgroundThrottling.mock.calls.at(-1)?.[0];

	afterEach(() => {
		vi.useRealTimers();
	});

	it("throttles tabs nobody can see and wakes them before they are used", async () => {
		const { emit, host, invoke, views } = setupTabHost(undefined, false, undefined, undefined, undefined, { lifecycle: { limits } });
		const { viewId } = (await invoke("browser:ensure", "sess-1")) as { viewId: string };
		await invoke("browser:openTab", { viewId }); // t2 active
		emit("browser:setBounds", { viewId, revision: 1, rect: panelRect, visible: true });
		expect(lastThrottle(views[0])).toBe(true); // t1 is a background tab
		expect(lastThrottle(views[1])).toBe(false);

		await invoke("browser:selectTab", { viewId, tabId: "t1" });
		expect(lastThrottle(views[0])).toBe(false);
		expect(lastThrottle(views[1])).toBe(true);

		// Leaving the session hides its active tab too.
		emit("browser:setBounds", { viewId, revision: 2, rect: hiddenRect, visible: false });
		expect(lastThrottle(views[0])).toBe(true);

		// An agent command wakes the active tab before it runs.
		await host.execute("sess-1", "snapshot", {});
		const calls = contentsOf(views[0]).setBackgroundThrottling.mock.calls.map(([allowed]) => allowed);
		expect(calls.slice(-2)).toEqual([false, true]);
		await host.dispose();
	});

	it("discards a tab hidden past the limit and reloads it when it is shown again", async () => {
		vi.useFakeTimers();
		const { emit, host, invoke, views } = setupTabHost(undefined, false, undefined, undefined, undefined, { lifecycle: { limits, sweepMs: 30_000 } });
		const { viewId } = (await invoke("browser:ensure", "sess-1")) as { viewId: string };
		await invoke("browser:navigate", { viewId, url: "https://first.example/" });
		await invoke("browser:openTab", { viewId });
		await invoke("browser:navigate", { viewId, url: "https://second.example/" });
		emit("browser:setBounds", { viewId, revision: 1, rect: panelRect, visible: true });

		await vi.advanceTimersByTimeAsync(limits.discardAfterMs - 60_000);
		expect(contentsOf(views[0]).forcefullyCrashRenderer).not.toHaveBeenCalled();
		await vi.advanceTimersByTimeAsync(2 * 60_000);
		expect(contentsOf(views[0]).forcefullyCrashRenderer).toHaveBeenCalledTimes(1);
		// The tab keeps its place, URL and title in the strip.
		const tabs = (await invoke("browser:getTabs", viewId)) as { tabs: Array<{ id: string; url: string }> };
		expect(tabs.tabs.map((tab) => [tab.id, tab.url])).toEqual([
			["t1", "https://first.example/"],
			["t2", "https://second.example/"],
		]);
		// The visible tab is never discarded.
		expect(contentsOf(views[1]).forcefullyCrashRenderer).not.toHaveBeenCalled();

		// Native tab work first brings the session's discarded tabs back.
		const selected = invoke("browser:selectTab", { viewId, tabId: "t1" });
		await vi.advanceTimersByTimeAsync(200);
		await selected;
		expect(contentsOf(views[0]).reload).toHaveBeenCalledTimes(1);
		expect(contentsOf(views[0]).isCrashed()).toBe(false);
		await host.dispose();
	});

	it("reloads only a discarded active tab before agent-browser work", async () => {
		vi.useFakeTimers();
		const { emit, host, invoke, views } = setupTabHost(undefined, false, undefined, undefined, undefined, { lifecycle: { limits, sweepMs: 30_000 } });
		const { viewId } = (await invoke("browser:ensure", "sess-1")) as { viewId: string };
		await invoke("browser:navigate", { viewId, url: "https://first.example/" });
		await invoke("browser:openTab", { viewId });
		await invoke("browser:navigate", { viewId, url: "https://second.example/" });
		emit("browser:setBounds", { viewId, revision: 1, rect: hiddenRect, visible: false });
		await vi.advanceTimersByTimeAsync(limits.discardAfterMs + 60_000);
		expect(contentsOf(views[0]).forcefullyCrashRenderer).toHaveBeenCalledTimes(1);
		expect(contentsOf(views[1]).forcefullyCrashRenderer).toHaveBeenCalledTimes(1);

		const opened = invoke("browser:openTab", { viewId });
		await vi.advanceTimersByTimeAsync(200);
		await opened;
		expect(contentsOf(views[1]).reload).toHaveBeenCalledTimes(1); // t2 was active
		expect(contentsOf(views[0]).reload).not.toHaveBeenCalled();
		await host.dispose();
	});

	it("never discards a tab whose renderer process another tab shares", async () => {
		vi.useFakeTimers();
		const { emit, host, invoke, views } = setupTabHost(undefined, false, undefined, undefined, undefined, {
			lifecycle: { limits, sweepMs: 30_000, pids: [7_000, 7_000] },
		});
		const { viewId } = (await invoke("browser:ensure", "sess-1")) as { viewId: string };
		await invoke("browser:navigate", { viewId, url: "https://first.example/" });
		await invoke("browser:openTab", { viewId });
		await invoke("browser:navigate", { viewId, url: "https://first.example/popup" });
		emit("browser:setBounds", { viewId, revision: 1, rect: panelRect, visible: true });
		await vi.advanceTimersByTimeAsync(limits.discardAfterMs * 2);
		expect(contentsOf(views[0]).forcefullyCrashRenderer).not.toHaveBeenCalled();
		await host.dispose();
	});
});


