import {
	describe,
	expect,
	it,
	vi,
} from "vitest";
import {
	type BrowserTabsState,
	browserShortcutAction,
	clampBoundsToWindow,
	createBrowserViewHost,
	isAllowedBrowserURL,
	normalizeBrowserURL,
	sanitizeBrowserTitle,
	sanitizeBrowserURL,
	scaleBoundsForZoom,
	shouldHandleAppShortcutInBrowserContext,
} from "./browser-view-host";
import {
	CLOSE_SHELL_TERMINAL_SHORTCUT_CHANNEL,
	APP_SHORTCUT_CHANNEL,
	NEW_SESSION_SHORTCUT_CHANNEL,
	NEW_SHELL_TERMINAL_SHORTCUT_CHANNEL,
} from "../shared/shortcuts";
import {
	InvokeHandler,
	setupHost,
} from "./browser-view-host-test-helpers";

vi.mock("./render-check-proxy", () => ({ startRenderCheckProxy: vi.fn(async () => 4321) }));

vi.mock("electron", async (importOriginal) => {
	const actual = await importOriginal<typeof import("electron")>();
	return { ...actual, net: { fetch: vi.fn() } };
});


describe("browser URL sanitization", () => {
	it.each([
		["ordinary URL", "https://example.test/docs/page", "https://example.test/docs/page"],
		[
			"query values and fragment",
			"https://example.test/access?token=opaque-high-entropy-value&state=another-secret#private",
			"https://example.test/access?token=%5Bredacted%5D&state=%5Bredacted%5D",
		],
		[
			"embedded credentials",
			"https://alice:password@example.test/private?next=secret",
			"https://example.test/private?next=%5Bredacted%5D",
		],
		["blank page", "about:blank", "about:blank"],
		[
			"malformed URL",
			"https://alice:password@example test/private?token=secret#private",
			"https://example test/private",
		],
	])("sanitizes %s", (_name, input, expected) => {
		expect(sanitizeBrowserURL(input)).toBe(expected);
	});

	it("sanitizes URL-shaped and URL-containing titles", () => {
		const signed = "https://example.test/access?token=opaque#private";
		expect(sanitizeBrowserTitle(signed)).toBe("https://example.test/access?token=%5Bredacted%5D");
		expect(sanitizeBrowserTitle(`Portal ${signed}`)).toBe(
			"Portal https://example.test/access?token=%5Bredacted%5D",
		);
		expect(sanitizeBrowserTitle("custom://alice:password@example.test/path?token=opaque#private")).toBe(
			"custom:[redacted]",
		);
		expect(sanitizeBrowserTitle("about:blank")).toBe("about:blank");
		expect(sanitizeBrowserTitle("mailto:operator@example.test?token=opaque")).toBe("mailto:[redacted]");
		expect(sanitizeBrowserTitle("data:text/plain,opaque")).toBe("data:[redacted]");
		expect(sanitizeBrowserTitle("javascript:alert('opaque')")).toBe("javascript:[redacted]");
		expect(sanitizeBrowserTitle("blob:https://example.test/opaque")).toBe("blob:[redacted]");
		expect(sanitizeBrowserTitle("Status: all systems operational")).toBe("Status: all systems operational");
		expect(sanitizeBrowserTitle("Government access portal")).toBe("Government access portal");
	});

	it.each([
		["compact status label", "Status:degraded", "Status:degraded"],
		[
			"compact URL label",
			"Docs:https://alice:password@example.test/path?token=opaque#private",
			"Docs:https://example.test/path?token=%5Bredacted%5D",
		],
	])("preserves %s while sanitizing embedded URLs", (_name, input, expected) => {
		expect(sanitizeBrowserTitle(input)).toBe(expected);
	});
});


describe("browser shortcut matching", () => {
	it("matches contextual browser shortcuts with platform-native primary modifiers", () => {
		const input = { control: true, meta: false, shift: false, alt: false, type: "keyDown" };
		expect(browserShortcutAction({ ...input, key: "T" }, false)).toBe("new-tab");
		expect(browserShortcutAction({ ...input, key: "w" }, false)).toBe("close-tab");
		expect(browserShortcutAction({ ...input, key: "l" }, false)).toBe("focus-location");
		expect(browserShortcutAction({ ...input, key: "f" }, false)).toBe("find");
		expect(browserShortcutAction({ ...input, key: "r" }, false)).toBe("reload");
		expect(browserShortcutAction({ ...input, key: "t", shift: true }, false)).toBe("reopen-tab");
		expect(browserShortcutAction({ ...input, key: "t", control: false, meta: true }, true)).toBe("new-tab");
	});

	it("maps page history to ⌘[ / ⌘] on macOS and Alt+←/→ elsewhere, without stealing the session chords", () => {
		const input = { control: false, meta: false, shift: false, alt: false, type: "keyDown" };
		expect(browserShortcutAction({ ...input, meta: true, key: "[" }, true)).toBe("back");
		expect(browserShortcutAction({ ...input, meta: true, key: "]" }, true)).toBe("forward");
		expect(browserShortcutAction({ ...input, alt: true, key: "ArrowLeft" }, false)).toBe("back");
		expect(browserShortcutAction({ ...input, alt: true, key: "ArrowRight" }, false)).toBe("forward");
		// ⌘⇧[ is "previous session" and ⌘⌥← is "previous tab": not the page's to take.
		expect(browserShortcutAction({ ...input, meta: true, shift: true, key: "{" }, true)).toBeNull();
		expect(browserShortcutAction({ ...input, meta: true, alt: true, key: "ArrowLeft" }, true)).toBeNull();
		// Wrong platform modifier.
		expect(browserShortcutAction({ ...input, control: true, key: "[" }, true)).toBeNull();
		expect(browserShortcutAction({ ...input, alt: true, key: "ArrowLeft" }, true)).toBeNull();
	});

	it("keeps app route history out of the Browser panel so ⌘[ turns the page, not the route", () => {
		const chord = { key: "[", code: "BracketLeft", ctrl: false, meta: true, shift: false, alt: false };
		expect(shouldHandleAppShortcutInBrowserContext("history-back", chord, true)).toBe(false);
		expect(shouldHandleAppShortcutInBrowserContext("history-forward", { ...chord, key: "]", code: "BracketRight" }, true)).toBe(false);
		expect(shouldHandleAppShortcutInBrowserContext("next-session", { ...chord, key: "}", code: "BracketRight", shift: true }, true)).toBe(true);
	});

	it("leaves Space and Shift+Space to Chromium so browser form controls can accept spaces", () => {
		const input = { key: " ", control: false, meta: false, shift: false, alt: false, type: "keyDown" };
		expect(browserShortcutAction(input, false)).toBeNull();
		expect(browserShortcutAction({ ...input, shift: true }, false)).toBeNull();
	});

	it("rejects extra and wrong-platform modifiers", () => {
		const input = { key: "t", control: true, meta: false, shift: false, alt: false, type: "keyDown" };
		expect(browserShortcutAction({ ...input, key: "r", shift: true }, false)).toBeNull();
		expect(browserShortcutAction({ ...input, meta: true }, false)).toBeNull();
		expect(browserShortcutAction(input, true)).toBeNull();
	});
});


describe("browser shortcut routing", () => {
	it("opens find-in-page from the native page and the shell", async () => {
		const { emitBeforeInput, emitShellBeforeInput, invoke, shellSend } = setupHost();
		const state = await invoke("browser:ensure", "sess-1");
		shellSend.mockClear();

		const nativeEvent = emitBeforeInput({ key: "f", control: true });
		expect(nativeEvent.preventDefault).toHaveBeenCalledOnce();
		expect(shellSend).toHaveBeenCalledWith(
			"browser:findOpen",
			expect.objectContaining({ viewId: state.viewId, tabId: "t1", query: "" }),
		);

		shellSend.mockClear();
		const shellEvent = emitShellBeforeInput({ key: "f", control: true });
		expect(shellEvent.preventDefault).toHaveBeenCalledOnce();
		expect(shellSend).toHaveBeenCalledWith("browser:findOpen", expect.objectContaining({ viewId: state.viewId }));
	});

	it("runs native per-tab find sessions and ignores stale result events", async () => {
		const { invoke, shellSend, webContents, webContentsListeners } = setupHost();
		const state = await invoke("browser:ensure", "sess-1");
		await invoke("browser:navigate", { viewId: state.viewId, url: "https://example.test/" });
		shellSend.mockClear();

		await invoke("browser:find", {
			viewId: state.viewId,
			query: "alpha",
			forward: true,
			newSession: true,
		});
		expect(webContents.findInPage).toHaveBeenCalledWith("alpha", {
			forward: true,
			findNext: true,
			matchCase: false,
		});

		webContentsListeners.get("found-in-page")?.({} as never, {
			requestId: 9,
			activeMatchOrdinal: 8,
			matches: 8,
			finalUpdate: true,
		} as never);
		expect(shellSend).not.toHaveBeenCalledWith(
			"browser:findState",
			expect.objectContaining({ activeMatchOrdinal: 8 }),
		);

		webContentsListeners.get("found-in-page")?.({} as never, {
			requestId: 17,
			activeMatchOrdinal: 2,
			matches: 4,
			finalUpdate: true,
		} as never);
		expect(shellSend).toHaveBeenCalledWith(
			"browser:findState",
			expect.objectContaining({ query: "alpha", activeMatchOrdinal: 2, matches: 4 }),
		);

		await invoke("browser:find:stop", { viewId: state.viewId, focusPage: true });
		expect(webContents.stopFindInPage).toHaveBeenCalledWith("clearSelection");
		expect(webContents.focus).toHaveBeenCalled();
	});

	it("flushes Chromium's delay for find queries shorter than four characters", async () => {
		const { invoke, webContents } = setupHost();
		const state = await invoke("browser:ensure", "sess-1");
		await invoke("browser:navigate", { viewId: state.viewId, url: "https://example.test/" });
		webContents.findInPage.mockClear();

		await invoke("browser:find", {
			viewId: state.viewId,
			query: "api",
			forward: true,
			newSession: true,
		});
		expect(webContents.findInPage).toHaveBeenNthCalledWith(1, "api", {
			forward: true,
			findNext: true,
			matchCase: false,
		});
		expect(webContents.findInPage).toHaveBeenNthCalledWith(2, "api", {
			forward: true,
			findNext: false,
			matchCase: false,
		});

		webContents.findInPage.mockClear();
		await invoke("browser:find", {
			viewId: state.viewId,
			query: "apis",
			forward: true,
			newSession: true,
		});
		expect(webContents.findInPage).toHaveBeenCalledOnce();
	});

	it("closes the last tab without a replacement on Ctrl+W", async () => {
		const { emitBeforeInput, host, invoke, shellSend } = setupHost();
		const state = await invoke("browser:ensure", "sess-1");
		await invoke("browser:navigate", { viewId: state.viewId, url: "https://example.test/" });
		const before = (await invoke("browser:getTabs", state.viewId)) as unknown as BrowserTabsState;
		const oldTabId = before.activeTabId;
		shellSend.mockClear();

		const closeEvent = emitBeforeInput({ key: "w", control: true });
		expect(closeEvent.preventDefault).toHaveBeenCalled();
		expect(shellSend).not.toHaveBeenCalledWith("browser:closePanel", state.viewId);
		expect(shellSend).not.toHaveBeenCalledWith(CLOSE_SHELL_TERMINAL_SHORTCUT_CHANNEL);
		expect(host.isLastUsedBrowser()).toBe(true);
		await vi.waitFor(() => {
			expect(shellSend).toHaveBeenCalledWith(
				"browser:tabsState",
				expect.objectContaining({ change: expect.objectContaining({ kind: "closed", tabId: oldTabId }) }),
			);
		});
		const tabs = (await invoke("browser:getTabs", state.viewId)) as unknown as BrowserTabsState;
		expect(tabs.tabs).toHaveLength(0);
		expect(tabs.activeTabId).toBe("");
		expect(host.isLastUsedBrowser()).toBe(false);
	});

	it("opens, focuses, and closes browser tabs without dispatching terminal shortcuts", async () => {
		const { emitBeforeInput, invoke, shellSend, webContents } = setupHost();
		const state = await invoke("browser:ensure", "sess-1");
		shellSend.mockClear();

		const focusEvent = emitBeforeInput({ key: "l", control: true });
		expect(focusEvent.preventDefault).toHaveBeenCalledOnce();
		expect(shellSend).toHaveBeenCalledWith("browser:focusLocation", state.viewId);

		const reopenEvent = emitBeforeInput({ key: "t", control: true, shift: true });
		expect(reopenEvent.preventDefault).toHaveBeenCalled();
		await vi.waitFor(() => {
			expect(shellSend).toHaveBeenCalledWith("browser:reopenClosedTab", state.viewId);
		});
		expect(shellSend).not.toHaveBeenCalledWith(APP_SHORTCUT_CHANNEL, { id: "reopen-closed-tab" });

		const openEvent = emitBeforeInput({ key: "t", control: true });
		// App-shortcut rejection + browser new-tab handler both consume the chord.
		expect(openEvent.preventDefault).toHaveBeenCalled();
		await vi.waitFor(async () => {
			const tabs = (await invoke("browser:getTabs", state.viewId)) as unknown as BrowserTabsState;
			expect(tabs.tabs).toHaveLength(2);
		});
		expect(shellSend).not.toHaveBeenCalledWith(NEW_SHELL_TERMINAL_SHORTCUT_CHANNEL);

		const closeEvent = emitBeforeInput({ key: "w", control: true });
		expect(closeEvent.preventDefault).toHaveBeenCalled();
		await vi.waitFor(async () => {
			const tabs = (await invoke("browser:getTabs", state.viewId)) as unknown as BrowserTabsState;
			expect(tabs.tabs).toHaveLength(1);
		});
		expect(webContents.close).toHaveBeenCalledOnce();
		expect(shellSend).toHaveBeenCalledWith(
			"browser:tabsState",
			expect.objectContaining({
				change: expect.objectContaining({
					kind: "closed",
					tabId: expect.any(String),
					tab: expect.objectContaining({ active: false }),
				}),
			}),
		);

		emitBeforeInput({ key: "w", control: true });
		await vi.waitFor(async () => {
			expect((await invoke("browser:getTabs", state.viewId) as unknown as BrowserTabsState).tabs).toHaveLength(0);
		});
		expect(webContents.close).toHaveBeenCalledTimes(2);
	});

	it("keeps browser shortcuts targeted after a blank-tab hide clears native focus", async () => {
		const { emit, emitBeforeInput, emitShellBeforeInput, host, invoke, send, shellSend } = setupHost();
		const state = await invoke("browser:ensure", "sess-1");
		send("browser:panelUsed", 1, state.viewId);
		expect(host.isLastUsedBrowser()).toBe(true);

		emitBeforeInput({ key: "t", control: true });
		await vi.waitFor(async () => {
			const tabs = (await invoke("browser:getTabs", state.viewId)) as unknown as BrowserTabsState;
			expect(tabs.tabs).toHaveLength(2);
		});
		expect(host.isLastUsedBrowser()).toBe(true);

		// Blank tabs hide the native surface via setBounds(visible:false). That must
		// not drop the shortcut target or the next ⌘T opens a shell terminal instead.
		emit("browser:setBounds", 1, {
			viewId: state.viewId,
			revision: 1,
			rect: { x: 0, y: 0, width: 10, height: 10 },
			visible: false,
		});
		expect(host.getLastFocusedPanelContents()).toBeNull();
		expect(host.isLastUsedBrowser()).toBe(true);

		shellSend.mockClear();
		emitShellBeforeInput({ key: "t", control: true });
		await vi.waitFor(async () => {
			const tabs = (await invoke("browser:getTabs", state.viewId)) as unknown as BrowserTabsState;
			expect(tabs.tabs).toHaveLength(3);
		});
		expect(shellSend).not.toHaveBeenCalledWith(NEW_SHELL_TERMINAL_SHORTCUT_CHANNEL);
	});

	it("keeps ⌘W closing browser tabs through the full open-focus-close sequence", async () => {
		const { emit, emitBeforeInput, emitShellBeforeInput, host, invoke, send, shellSend } = setupHost();
		const state = await invoke("browser:ensure", "sess-1");
		// Click into the browser view.
		send("browser:panelUsed", 1, state.viewId);

		// ⌘T from the native page opens a tab and focuses the omnibox.
		emitBeforeInput({ key: "t", control: true });
		await vi.waitFor(async () => {
			const tabs = (await invoke("browser:getTabs", state.viewId)) as unknown as BrowserTabsState;
			expect(tabs.tabs).toHaveLength(2);
		});
		// Blank-tab hide must not drop the shortcut target.
		emit("browser:setBounds", 1, {
			viewId: state.viewId,
			revision: 1,
			rect: { x: 0, y: 0, width: 10, height: 10 },
			visible: false,
		});
		expect(host.isLastUsedBrowser()).toBe(true);

		// First ⌘W from the shell omnibox closes the browser tab (not a terminal).
		shellSend.mockClear();
		emitShellBeforeInput({ key: "w", control: true });
		await vi.waitFor(async () => {
			const tabs = (await invoke("browser:getTabs", state.viewId)) as unknown as BrowserTabsState;
			expect(tabs.tabs).toHaveLength(1);
		});
		expect(shellSend).not.toHaveBeenCalledWith(CLOSE_SHELL_TERMINAL_SHORTCUT_CHANNEL);
		expect(host.isLastUsedBrowser()).toBe(true);

		// Second ⌘W removes the final tab and releases the browser shortcut target.
		shellSend.mockClear();
		const secondClose = emitShellBeforeInput({ key: "w", control: true });
		expect(secondClose.preventDefault).toHaveBeenCalled();
		expect(shellSend).not.toHaveBeenCalledWith("browser:closePanel", state.viewId);
		expect(shellSend).not.toHaveBeenCalledWith(CLOSE_SHELL_TERMINAL_SHORTCUT_CHANNEL);
		expect(host.isLastUsedBrowser()).toBe(true);
		await vi.waitFor(() => {
			expect(shellSend).toHaveBeenCalledWith(
				"browser:tabsState",
				expect.objectContaining({ change: expect.objectContaining({ kind: "closed" }) }),
			);
		});
		const tabs = (await invoke("browser:getTabs", state.viewId)) as unknown as BrowserTabsState;
		expect(tabs.tabs).toHaveLength(0);
		expect(host.isLastUsedBrowser()).toBe(false);
	});

	it("consumes auto-repeat browser chords without re-firing", async () => {
		const { emitBeforeInput, invoke, shellSend } = setupHost();
		const state = await invoke("browser:ensure", "sess-1");

		// Both native listeners (app-shortcut rejection + browser handler) consume.
		const repeatClose = emitBeforeInput({ key: "w", control: true, isAutoRepeat: true });
		expect(repeatClose.preventDefault).toHaveBeenCalled();
		const repeatNewTab = emitBeforeInput({ key: "t", control: true, isAutoRepeat: true });
		expect(repeatNewTab.preventDefault).toHaveBeenCalled();
		await Promise.resolve();
		const tabs = (await invoke("browser:getTabs", state.viewId)) as unknown as BrowserTabsState;
		expect(tabs.tabs).toHaveLength(1);
		expect(shellSend).not.toHaveBeenCalledWith("browser:focusLocation", state.viewId);
	});

	it("moves focus to the replacement tab synchronously on keyboard close", async () => {
		const { emitBeforeInput, invoke, shellSend, webContents } = setupHost();
		const state = await invoke("browser:ensure", "sess-1");
		emitBeforeInput({ key: "t", control: true });
		await vi.waitFor(async () => {
			const tabs = (await invoke("browser:getTabs", state.viewId)) as unknown as BrowserTabsState;
			expect(tabs.tabs).toHaveLength(2);
		});
		vi.mocked(webContents.focus).mockClear();

		// The focused view is destroyed asynchronously; focus must already have
		// moved before the close resolves or a fast second ⌘W hits menu Close.
		// Harness tabs are always blank (hidden native surface), so the fallback
		// goes to the omnibox synchronously — still a handled surface.
		shellSend.mockClear();
		emitBeforeInput({ key: "w", control: true });
		expect(shellSend).toHaveBeenCalledWith("browser:focusLocation", state.viewId);
		await vi.waitFor(async () => {
			const tabs = (await invoke("browser:getTabs", state.viewId)) as unknown as BrowserTabsState;
			expect(tabs.tabs).toHaveLength(1);
		});
	});

	it("focuses a live replacement tab synchronously on keyboard close", async () => {
		const { emitBeforeInput, invoke, webContents } = setupHost();
		const state = await invoke("browser:ensure", "sess-1");
		emitBeforeInput({ key: "t", control: true });
		await vi.waitFor(async () => {
			const tabs = (await invoke("browser:getTabs", state.viewId)) as unknown as BrowserTabsState;
			expect(tabs.tabs).toHaveLength(2);
		});
		// The harness shares one webContents mock across tabs, so navigate the
		// active tab to get a non-blank read for the replacement branch.
		await invoke("browser:navigate", { viewId: state.viewId, url: "https://example.test/" });
		vi.mocked(webContents.focus).mockClear();

		// Focus moves before the async close resolves, so it is never stranded
		// on the dying view where a fast second ⌘W would hit menu Close.
		emitBeforeInput({ key: "w", control: true });
		expect(webContents.focus).toHaveBeenCalled();
		await vi.waitFor(async () => {
			const tabs = (await invoke("browser:getTabs", state.viewId)) as unknown as BrowserTabsState;
			expect(tabs.tabs).toHaveLength(1);
		});
	});

	it("closes tab from shell shortcut and keeps focus on browser replacement", async () => {
		const { emitShellBeforeInput, host, invoke, send, shellSend } = setupHost();
		const state = await invoke("browser:ensure", "sess-1");
		send("browser:panelUsed", 1, state.viewId);
		emitShellBeforeInput({ key: "t", control: true });
		await vi.waitFor(async () => {
			const tabs = (await invoke("browser:getTabs", state.viewId)) as unknown as BrowserTabsState;
			expect(tabs.tabs).toHaveLength(2);
		});
		shellSend.mockClear();
		const closeEvent = emitShellBeforeInput({ key: "w", control: true });
		expect(closeEvent.preventDefault).toHaveBeenCalled();
		expect(host.isLastUsedBrowser()).toBe(true);
		await vi.waitFor(async () => {
			const tabs = (await invoke("browser:getTabs", state.viewId)) as unknown as BrowserTabsState;
			expect(tabs.tabs).toHaveLength(1);
			expect(shellSend).toHaveBeenCalledWith("browser:focusLocation", state.viewId);
		});
		expect(host.isLastUsedBrowser()).toBe(true);
	});

	it("routes shell key input to the browser only while its panel is last used", async () => {
		const { emitShellBeforeInput, host, invoke, send, shellSend } = setupHost();
		const state = await invoke("browser:ensure", "sess-1");
		shellSend.mockClear();

		send("browser:panelUsed", 1, state.viewId);
		const event = emitShellBeforeInput({ key: "l", control: true });
		expect(event.preventDefault).toHaveBeenCalledOnce();
		expect(shellSend).toHaveBeenCalledWith("browser:focusLocation", state.viewId);
		expect(host.isLastUsedBrowser()).toBe(true);

		host.forgetLastFocusedPanel();
		const ignored = emitShellBeforeInput({ key: "l", control: true });
		expect(ignored.preventDefault).not.toHaveBeenCalled();
	});

	it("reloads the active native page without intercepting Chromium's Space behavior", async () => {
		const { emitBeforeInput, invoke, webContents } = setupHost();
		await invoke("browser:ensure", "sess-1");

		emitBeforeInput({ key: "r", control: true });
		expect(webContents.reload).toHaveBeenCalledOnce();

		const spaceEvent = emitBeforeInput({ key: " " });
		const shiftSpaceEvent = emitBeforeInput({ key: " ", shift: true });
		expect(spaceEvent.preventDefault).not.toHaveBeenCalled();
		expect(shiftSpaceEvent.preventDefault).not.toHaveBeenCalled();
	});

	it("orders a reopen request after an in-flight keyboard tab close", async () => {
		const { emitBeforeInput, invoke, shellSend } = setupHost();
		const state = await invoke("browser:ensure", "sess-1");

		emitBeforeInput({ key: "t", control: true });
		await vi.waitFor(async () => {
			const tabs = (await invoke("browser:getTabs", state.viewId)) as unknown as BrowserTabsState;
			expect(tabs.tabs).toHaveLength(2);
		});
		shellSend.mockClear();

		emitBeforeInput({ key: "w", control: true });
		emitBeforeInput({ key: "t", control: true, shift: true });

		await vi.waitFor(() => {
			const channels = shellSend.mock.calls.map(([channel]) => channel);
			expect(channels.indexOf("browser:tabsState")).toBeGreaterThanOrEqual(0);
			expect(channels.indexOf("browser:reopenClosedTab")).toBeGreaterThan(
				channels.indexOf("browser:tabsState"),
			);
		});
	});
});


describe("browser scrollbar styling", () => {
	it("injects Tallpine-styled horizontal and vertical scrollbars whenever a browser page becomes ready", async () => {
		const { insertCSS, invoke, removeInsertedCSS, setBrowserZoomFactor, webContentsListeners } = setupHost();

		await invoke("browser:ensure", "sess-1");
		webContentsListeners.get("dom-ready")?.();

		await vi.waitFor(() => expect(insertCSS).toHaveBeenCalledOnce());
		const css = insertCSS.mock.calls[0]?.[0] ?? "";
		expect(insertCSS.mock.calls[0]?.[1]).toEqual({ cssOrigin: "user" });
		expect(css).toContain("::-webkit-scrollbar-thumb");
		expect(css).toContain("border-radius: 999px");
		expect(css).toContain("background: rgba(232, 232, 232, 0.72)");
		expect(css).toContain("::-webkit-scrollbar-track");
		expect(css).toContain("background: transparent");
		expect(css).toContain("width: 8px");
		expect(css).toContain("height: 8px");
		expect(css).not.toContain("min-height");
		expect(css).not.toContain("min-width");

		webContentsListeners.get("dom-ready")?.();
		await vi.waitFor(() => expect(insertCSS).toHaveBeenCalledTimes(2));

		setBrowserZoomFactor(4);
		webContentsListeners.get("zoom-changed")?.();
		await vi.waitFor(() => expect(insertCSS).toHaveBeenCalledTimes(3));
		const zoomedCss = insertCSS.mock.calls[2]?.[0] ?? "";
		expect(zoomedCss).toContain("width: 2px");
		expect(zoomedCss).toContain("height: 2px");
		await vi.waitFor(() => expect(removeInsertedCSS).toHaveBeenCalledWith("tallpine-browser-scrollbars-2"));
	});
});


describe("browser page drag regions", () => {
	it("keeps page-declared app-region drag areas from swallowing native input", async () => {
		const { insertCSS, invoke, setBrowserZoomFactor, webContentsListeners } = setupHost();

		await invoke("browser:ensure", "sess-1");
		webContentsListeners.get("dom-ready")?.();

		await vi.waitFor(() => expect(insertCSS).toHaveBeenCalledOnce());
		const css = insertCSS.mock.calls[0]?.[0] ?? "";
		// Only a user-origin !important declaration outranks the page's own
		// `app-region: drag !important` and inline styles.
		expect(insertCSS.mock.calls[0]?.[1]).toEqual({ cssOrigin: "user" });
		expect(css).toMatch(/\*,\s*\*::before,\s*\*::after\s*\{\s*app-region: no-drag !important;\s*\}/);

		// Every new document and zoom change re-applies the sheet.
		webContentsListeners.get("dom-ready")?.();
		setBrowserZoomFactor(2);
		webContentsListeners.get("zoom-changed")?.();
		await vi.waitFor(() => expect(insertCSS).toHaveBeenCalledTimes(3));
		for (const [injected] of insertCSS.mock.calls) {
			expect(injected).toContain("app-region: no-drag !important");
		}
	});
});

describe("new-session shortcut forwarding", () => {
	it("focuses the shell before forwarding a matching preview chord", async () => {
		const { emitBeforeInput, invoke, shellFocus, shellSend } = setupHost();
		await invoke("browser:ensure", "sess-1");
		shellFocus.mockClear();
		shellSend.mockClear();

		const event = emitBeforeInput({ key: "N", control: true, shift: true });

		expect(event.preventDefault).toHaveBeenCalledTimes(1);
		expect(shellFocus).toHaveBeenCalledTimes(1);
		expect(shellSend).toHaveBeenCalledWith(NEW_SESSION_SHORTCUT_CHANNEL);
		expect(shellFocus.mock.invocationCallOrder[0]).toBeLessThan(shellSend.mock.invocationCallOrder[0]);
	});

	it("does not focus or forward auto-repeat and non-matching preview input", async () => {
		const { emitBeforeInput, invoke, shellFocus, shellSend } = setupHost();
		await invoke("browser:ensure", "sess-1");
		shellFocus.mockClear();
		shellSend.mockClear();

		emitBeforeInput({ key: "N", control: true, shift: true, isAutoRepeat: true });
		emitBeforeInput({ key: "N", control: true });

		expect(shellFocus).not.toHaveBeenCalled();
		expect(shellSend).not.toHaveBeenCalledWith(NEW_SESSION_SHORTCUT_CHANNEL);
	});
});


describe("normalizeBrowserURL", () => {
	it("defaults localhost-style inputs to http", () => {
		expect(normalizeBrowserURL("localhost:5173").href).toBe("http://localhost:5173/");
		expect(normalizeBrowserURL("127.0.0.1:3000").href).toBe("http://127.0.0.1:3000/");
		expect(normalizeBrowserURL("[::1]:4173").href).toBe("http://[::1]:4173/");
	});

	it("defaults ordinary bare hosts to https", () => {
		expect(normalizeBrowserURL("example.com").href).toBe("https://example.com/");
		expect(normalizeBrowserURL("example.com/path?q=1").href).toBe("https://example.com/path?q=1");
		expect(normalizeBrowserURL("192.168.1.5:8080").href).toBe("https://192.168.1.5:8080/");
	});

	it("routes non-URL input to a web search", () => {
		expect(normalizeBrowserURL("hi").href).toBe("https://www.google.com/search?q=hi");
		expect(normalizeBrowserURL("how do i center a div").href).toBe(
			"https://www.google.com/search?q=how%20do%20i%20center%20a%20div",
		);
		// A dot-less token with a trailing colon is text, not a scheme, once it
		// carries whitespace, so it still searches rather than throwing on new URL().
		expect(normalizeBrowserURL("time: now").href).toBe("https://www.google.com/search?q=time%3A%20now");
	});

	it("rejects file URLs because the browser target is automatable", () => {
		expect(() => normalizeBrowserURL("file:///tmp/preview/index.html")).toThrow("Unsupported browser URL scheme");
		expect(() => normalizeBrowserURL("file:///C:/tmp/index.html")).toThrow("Unsupported browser URL scheme");
	});

	it("rejects absolute local paths rather than converting them into automatable files", () => {
		expect(() => normalizeBrowserURL("C:\\Users\\Lenovo\\Downloads\\sm5\\paper_explainer.html")).toThrow(
			"Unsupported browser URL scheme",
		);
		expect(() => normalizeBrowserURL("C:/Users/Lenovo/My File.html")).toThrow("Unsupported browser URL scheme");
		expect(() => normalizeBrowserURL("/tmp/preview/index.html")).toThrow("Unsupported browser URL scheme");
	});

	it("rejects privileged or unsupported schemes", () => {
		expect(() => normalizeBrowserURL("app://renderer/index.html")).toThrow(/unsupported/i);
		expect(() => normalizeBrowserURL("javascript:alert(1)")).toThrow(/unsupported/i);
	});
});


describe("isAllowedBrowserURL", () => {
	it("rejects file URLs even when a renderer origin is set", () => {
		expect(isAllowedBrowserURL("file:///tmp/preview/index.html", "http://localhost:5173")).toBe(false);
	});

	it("still blocks the renderer's own http origin", () => {
		expect(isAllowedBrowserURL("http://localhost:5173/", "http://localhost:5173")).toBe(false);
	});
});


describe("getLastFocusedPanelContents", () => {
	// Mock that captures each panel's "focus" listener so the test can fire it.
	function setup() {
		let focusListener: (() => void) | undefined;
		const shellSend = vi.fn();
		const webContents = {
			canGoBack: () => false,
			canGoForward: () => false,
			clearHistory: () => undefined,
			getTitle: () => "",
			getURL: () => "",
			goBack: () => undefined,
			goForward: () => undefined,
			isLoading: () => false,
			loadURL: async () => undefined,
			reload: () => undefined,
			send: () => undefined,
			setWindowOpenHandler: () => undefined,
			stop: () => undefined,
			close: () => undefined,
			isDestroyed: () => false,
			on: (event: string, listener: () => void) => {
				if (event === "focus") focusListener = listener;
			},
		};
		const view = { webContents, setBounds: () => undefined, setVisible: () => undefined };
		const handlers = new Map<string, InvokeHandler>();
		const record = (channel: string, fn: InvokeHandler) => handlers.set(channel, fn);
		const host = createBrowserViewHost({
			mainWindow: {
				contentView: { addChildView: () => undefined, removeChildView: () => undefined },
				getContentBounds: () => ({ x: 0, y: 0, width: 800, height: 600 }),
				webContents: { id: 1, send: shellSend },
			} as never,
			ipcMain: { handle: record, on: record, removeHandler: () => undefined, off: () => undefined } as never,
			shell: { openExternal: async () => undefined },
			WebContentsView: function () {
				return view;
			} as never,
			annotatePreloadPath: "/preload.js",
			rendererOrigin: "http://localhost:5173",
		});
		const call = (channel: string, ...args: unknown[]) =>
			handlers.get(channel)!({ sender: { id: 1, getZoomFactor: () => 1, send: shellSend } }, ...args);
		return { host, call, shellSend, webContents, focus: () => focusListener?.() };
	}

	it("is null until a panel is focused", async () => {
		const { host, call } = setup();
		await call("browser:ensure", "s");
		expect(host.getLastFocusedPanelContents()).toBeNull();
	});

	it("tracks the focused panel, then clears on hide and destroy", async () => {
		const { host, call, shellSend, webContents, focus } = setup();
		await call("browser:ensure", "s");

		focus();
		expect(host.getLastFocusedPanelContents()).toBe(webContents);
		expect(shellSend).toHaveBeenCalledWith("browser:pageFocus", "1:s");

		call("browser:setBounds", { viewId: "1:s", revision: 1, rect: { x: 0, y: 0, width: 10, height: 10 }, visible: false });
		expect(host.getLastFocusedPanelContents()).toBeNull();

		focus();
		expect(host.getLastFocusedPanelContents()).toBe(webContents);

		call("browser:destroy", "1:s");
		expect(host.getLastFocusedPanelContents()).toBeNull();
	});
});


describe("clampBoundsToWindow", () => {
	it("rounds and clamps bounds to the window content area", () => {
		expect(
			clampBoundsToWindow({ x: -10.4, y: 20.6, width: 900.2, height: 700.8 }, { width: 800, height: 600 }),
		).toEqual({ x: 0, y: 21, width: 800, height: 579 });
	});

	it("returns a zero-sized rectangle when the slot is outside the window", () => {
		expect(clampBoundsToWindow({ x: 900, y: 10, width: 100, height: 100 }, { width: 800, height: 600 })).toEqual({
			x: 800,
			y: 10,
			width: 0,
			height: 100,
		});
	});
});


describe("scaleBoundsForZoom", () => {
	it("converts renderer CSS-pixel bounds into Electron view bounds", () => {
		expect(scaleBoundsForZoom({ x: 100, y: 20, width: 320, height: 240 }, 1.25)).toEqual({
			x: 125,
			y: 25,
			width: 400,
			height: 300,
		});
	});

	it("ignores invalid zoom factors", () => {
		const rect = { x: 100, y: 20, width: 320, height: 240 };

		expect(scaleBoundsForZoom(rect, 1)).toBe(rect);
		expect(scaleBoundsForZoom(rect, 0)).toBe(rect);
		expect(scaleBoundsForZoom(rect, Number.NaN)).toBe(rect);
	});
});

