import {
	createSlot,
	renderBrowserView,
	setupBridge,
	setFullscreenElement,
} from "./useBrowserView.test.setup";

import { useBrowserView } from "./useBrowserView";
import { resetClosedTabsForTest, resetConsumedPreviewTriggersForTest } from "./browser-view/browser-view-registry";
import { act, renderHook, waitFor } from "@testing-library/react";
import {
	afterEach,
	describe,
	expect,
	it,
	vi,
} from "vitest";



function tab(id: string, url: string, title: string, active: boolean) {
	return { id, url, title, active };
}

describe("useBrowserView", () => {
	afterEach(() => {
		vi.restoreAllMocks();
		setFullscreenElement(null);
		document.body.replaceChildren();
		resetConsumedPreviewTriggersForTest();
		resetClosedTabsForTest();
	});

	it("ensures a scoped browser view and reports the measured slot bounds", async () => {
		const bridge = setupBridge();
		const slot = createSlot();
		const { result } = renderBrowserView();

		await waitFor(() => expect(bridge.ensure).toHaveBeenCalledWith("sess-1", false));
		// Simulate the real IPC flow: after ensure, a navigate call sends a nav
		// state with a URL so the positioning effect considers the view visible.
		act(() =>
			bridge.emit({
				viewId: "42:sess-1",
				url: "http://localhost:3000/",
				title: "",
				canGoBack: false,
				canGoForward: false,
				isLoading: false,
			}),
		);
		act(() => result.current.slotRef(slot));

		await waitFor(() =>
			expect(bridge.setBounds).toHaveBeenCalledWith(expect.objectContaining({
				viewId: "42:sess-1",
				rect: { x: 12, y: 34, width: 320, height: 240 },
				visible: true,
			})),
		);
		const revisions = bridge.setBounds.mock.calls.map(([input]) => input.revision);
		expect(
			revisions.every(
				(revision, index) => Number.isSafeInteger(revision) && (index === 0 || revision > revisions[index - 1]!),
			),
		).toBe(true);
		expect(result.current.viewId).toBe("42:sess-1");
	});

	it("leaves the runtime link state to the Browser panel (#6466)", async () => {
		const bridge = setupBridge();
		createSlot();
		renderBrowserView();
		await act(async () => undefined);

		// SessionView owns this hook; a flapping link must not re-render it.
		expect(bridge.onRuntimeState).not.toHaveBeenCalled();
		expect(bridge.getRuntimeState).not.toHaveBeenCalled();
	});

	it("keeps bounds revisions increasing when the same native view remounts", async () => {
		const bridge = setupBridge();
		const firstSlot = createSlot();
		const first = renderBrowserView();

		await waitFor(() => expect(first.result.current.viewId).toBe("42:sess-1"));
		act(() => first.result.current.slotRef(firstSlot));
		await waitFor(() => expect(bridge.setBounds).toHaveBeenCalled());
		first.unmount();
		const firstRevision = Math.max(...bridge.setBounds.mock.calls.map(([input]) => input.revision));

		bridge.setBounds.mockClear();
		const secondSlot = createSlot();
		const second = renderBrowserView();
		await waitFor(() => expect(second.result.current.viewId).toBe("42:sess-1"));
		act(() => second.result.current.slotRef(secondSlot));
		await waitFor(() => expect(bridge.setBounds).toHaveBeenCalled());

		expect(bridge.setBounds.mock.calls.every(([input]) => input.revision > firstRevision)).toBe(true);
		second.unmount();
	});

	it("keeps an active blank target live", async () => {
		const bridge = setupBridge();
		const slot = createSlot();
		const { result } = renderBrowserView();

		await waitFor(() => expect(result.current.viewId).toBe("42:sess-1"));
		act(() => result.current.slotRef(slot));

		await waitFor(() =>
			expect(bridge.setBounds).toHaveBeenCalledWith(expect.objectContaining({
				viewId: "42:sess-1",
				rect: { x: 12, y: 34, width: 320, height: 240 },
				visible: true,
			})),
		);
	});

	it("restores native bounds synchronously when React hands the slot to a new host", async () => {
		const bridge = setupBridge();
		const dockedSlot = createSlot();
		const expandedSlot = createSlot();
		vi.spyOn(expandedSlot, "getBoundingClientRect").mockReturnValue({
			x: 8,
			y: 48,
			top: 48,
			right: 1192,
			bottom: 748,
			left: 8,
			width: 1184,
			height: 700,
			toJSON: () => ({}),
		});
		const { result } = renderBrowserView();

		await waitFor(() => expect(result.current.viewId).toBe("42:sess-1"));
		act(() => result.current.slotRef(dockedSlot));
		bridge.setBounds.mockClear();

		act(() => {
			result.current.slotRef(null);
			result.current.slotRef(expandedSlot);
		});

		expect(bridge.setBounds).toHaveBeenLastCalledWith(expect.objectContaining({
			viewId: "42:sess-1",
			rect: { x: 8, y: 48, width: 1184, height: 700 },
			visible: true,
		}));
	});

	it("tracks popup tabs and routes manual select and close actions", async () => {
		const bridge = setupBridge();
		const { result } = renderBrowserView();

		await waitFor(() => expect(result.current.tabs.map((tab) => tab.id)).toEqual(["t1"]));
		act(() =>
			bridge.emitTabs({
				viewId: "42:sess-1",
				activeTabId: "t2",
				tabs: [
					tab("t1", "http://localhost:3000/", "First", false),
					tab("t2", "http://localhost:4173/", "Popup", true),
				],
				change: { kind: "popup", tabId: "t2" },
			}),
		);

		expect(result.current.activeTabId).toBe("t2");
		expect(result.current.tabNotice).toBe("Opened new tab");

		await act(() => result.current.selectTab("t1"));
		expect(bridge.selectTab).toHaveBeenCalledWith({
			viewId: "42:sess-1",
			tabId: "t1",
		});
		await act(() => result.current.closeTab("t2"));
		expect(bridge.closeTab).toHaveBeenCalledWith({
			viewId: "42:sess-1",
			tabId: "t2",
		});
	});

	it("reuses the active blank tab when opening a chat link", async () => {
		const bridge = setupBridge();
		const { result } = renderBrowserView();

		await waitFor(() => expect(result.current.tabs.map((tab) => tab.id)).toEqual(["t1"]));
		await act(() => result.current.openLink("http://localhost:5173/"));

		expect(bridge.navigate).toHaveBeenCalledWith({ viewId: "42:sess-1", url: "http://localhost:5173/" });
		expect(bridge.openTab).not.toHaveBeenCalledWith({ viewId: "42:sess-1", url: "http://localhost:5173/" });
	});

	it("selects an existing matching tab instead of opening a duplicate", async () => {
		const bridge = setupBridge();
		const { result } = renderBrowserView();

		await waitFor(() => expect(result.current.tabs.map((tab) => tab.id)).toEqual(["t1"]));
		act(() => bridge.emitTabs({
			viewId: "42:sess-1",
			activeTabId: "t2",
			tabs: [
				{ id: "t1", url: "https://instagram.com/", title: "Instagram", active: false },
				{ id: "t2", url: "https://example.com/", title: "Example", active: true },
			],
			change: { kind: "popup", tabId: "t2" },
		}));
		bridge.getTabs.mockResolvedValue({
			viewId: "42:sess-1",
			activeTabId: "t2",
			tabs: [
				{ id: "t1", url: "https://instagram.com/", title: "Instagram", active: false },
				{ id: "t2", url: "https://example.com/", title: "Example", active: true },
			],
		});

		await act(() => result.current.openLink("https://www.instagram.com/#inbox"));
		expect(bridge.selectTab).toHaveBeenCalledWith({ viewId: "42:sess-1", tabId: "t1" });
		expect(bridge.openTab).not.toHaveBeenCalledWith({
			viewId: "42:sess-1",
			url: "https://www.instagram.com/#inbox",
		});
	});

	it("remembers a closed tab so it can be reopened, and forgets it once reopened", async () => {
		const bridge = setupBridge();
		const { result } = renderBrowserView();

		await waitFor(() => expect(result.current.tabs.map((tab) => tab.id)).toEqual(["t1"]));
		act(() =>
			bridge.emitTabs({
				viewId: "42:sess-1",
				activeTabId: "t2",
				tabs: [
					tab("t1", "http://localhost:3000/", "First", false),
					tab("t2", "http://localhost:4173/", "Second", true),
				],
				change: { kind: "popup", tabId: "t2" },
			}),
		);

		await act(() => result.current.closeTab("t2"));
		expect(result.current.closedTabs).toEqual([
			{
				id: "t2",
				url: "http://localhost:4173/",
				title: "Second",
				favicon: undefined,
			},
		]);

		await act(() => result.current.reopenClosedTab("t2"));
		expect(bridge.openTab).toHaveBeenCalledWith({
			viewId: "42:sess-1",
			url: "http://localhost:4173/",
		});
		expect(result.current.closedTabs).toEqual([]);
	});

	it("remembers a tab closed by a main-process keyboard shortcut", async () => {
		const bridge = setupBridge();
		const { result } = renderBrowserView();
		await waitFor(() => expect(result.current.tabs.map((tab) => tab.id)).toEqual(["t1"]));

		act(() =>
			bridge.emitTabs({
				viewId: "42:sess-1",
				activeTabId: "t1",
				tabs: [
					tab("t1", "http://localhost:3000/", "First", true),
				],
				change: {
					kind: "closed",
					tabId: "t2",
					tab: tab("t2", "http://localhost:4173/", "Keyboard closed", false),
				},
			}),
		);

		expect(result.current.closedTabs).toEqual([
			{
				id: "t2",
				url: "http://localhost:4173/",
				title: "Keyboard closed",
				favicon: undefined,
			},
		]);
		await act(() => result.current.reopenClosedTab("t2"));
		expect(bridge.openTab).toHaveBeenCalledWith({
			viewId: "42:sess-1",
			url: "http://localhost:4173/",
		});
	});

	it("can reopen a tab immediately after receiving its keyboard-close event", async () => {
		const bridge = setupBridge();
		const { result } = renderBrowserView();
		await waitFor(() => expect(result.current.tabs.map((tab) => tab.id)).toEqual(["t1"]));

		await act(async () => {
			bridge.emitTabs({
				viewId: "42:sess-1",
				activeTabId: "t1",
				tabs: [
					tab("t1", "http://localhost:3000/", "First", true),
				],
				change: {
					kind: "closed",
					tabId: "t2",
					tab: tab("t2", "http://localhost:4173/", "Keyboard closed", false),
				},
			});
			await result.current.reopenClosedTab("t2");
		});

		expect(bridge.openTab).toHaveBeenCalledWith({
			viewId: "42:sess-1",
			url: "http://localhost:4173/",
		});
	});

	it("reopens a closed tab beyond the former tab cap", async () => {
		const bridge = setupBridge();
		const { result } = renderBrowserView();

		await waitFor(() => expect(result.current.tabs.map((tab) => tab.id)).toEqual(["t1"]));
		act(() =>
			bridge.emitTabs({
				viewId: "42:sess-1",
				activeTabId: "t2",
				tabs: [
					tab("t1", "http://localhost:3000/", "First", false),
					tab("t2", "http://localhost:4173/", "Second", true),
				],
				change: { kind: "popup", tabId: "t2" },
			}),
		);
		await act(() => result.current.closeTab("t2"));
		expect(result.current.closedTabs).toHaveLength(1);

		// Simulate many other tabs having opened since (e.g. via agent activity).
		act(() =>
			bridge.emitTabs({
				viewId: "42:sess-1",
				activeTabId: "t1",
				tabs: Array.from({ length: 16 }, (_, i) => ({
					id: `t${i + 10}`,
					url: `http://localhost:3000/${i}`,
					title: `Tab ${i}`,
					active: i === 0,
				})),
			}),
		);

		await act(() => result.current.reopenClosedTab("t2"));

		expect(bridge.openTab).toHaveBeenCalledWith({
			viewId: "42:sess-1",
			url: "http://localhost:4173/",
		});
		expect(result.current.closedTabs).toHaveLength(0);
	});

	// Regression: the same drop-before-await bug also loses the entry on any
	// other openTab failure (e.g. a race where the cap is hit between the row
	// rendering and the click) — restore it instead.
	it("restores a closed-tab entry if reopening it fails", async () => {
		const bridge = setupBridge();
		const { result } = renderBrowserView();

		await waitFor(() => expect(result.current.tabs.map((tab) => tab.id)).toEqual(["t1"]));
		act(() =>
			bridge.emitTabs({
				viewId: "42:sess-1",
				activeTabId: "t2",
				tabs: [
					tab("t1", "http://localhost:3000/", "First", false),
					tab("t2", "http://localhost:4173/", "Second", true),
				],
				change: { kind: "popup", tabId: "t2" },
			}),
		);
		await act(() => result.current.closeTab("t2"));
		expect(result.current.closedTabs).toHaveLength(1);

		bridge.openTab.mockRejectedValueOnce(new Error("Browser tab creation failed"));
		await act(() => result.current.reopenClosedTab("t2"));

		expect(result.current.closedTabs).toHaveLength(1);
		expect(result.current.tabNotice).toBe("Couldn't reopen that tab");
	});

	// Closing from the tab strip is fire-and-forget, so a rejected close
	// must be handled without becoming an unhandled rejection.
	it("surfaces a notice instead of throwing when closing a tab fails", async () => {
		const bridge = setupBridge();
		const { result } = renderBrowserView();
		await waitFor(() => expect(result.current.tabs.map((tab) => tab.id)).toEqual(["t1"]));

		bridge.closeTab.mockRejectedValueOnce(new Error("boom"));
		await expect(act(() => result.current.closeTab("t1"))).resolves.toBeUndefined();
		expect(result.current.tabNotice).toBe("Couldn't close that tab");
	});

	it("resyncs a failed tab selection without showing a switch-failure notice", async () => {
		const bridge = setupBridge();
		const { result } = renderBrowserView();
		await waitFor(() => expect(result.current.activeTabId).toBe("t1"));

		bridge.selectTab.mockRejectedValueOnce(new Error("automation sync failed"));
		bridge.getTabs.mockResolvedValueOnce({
			viewId: "42:sess-1",
			activeTabId: "t2",
			tabs: [{ id: "t2", url: "https://example.test/", title: "Selected", active: true }],
		});
		await expect(act(() => result.current.selectTab("t2"))).resolves.toBeUndefined();
		expect(result.current.activeTabId).toBe("t2");
		expect(result.current.tabNotice).toBe("");

		bridge.selectTab.mockRejectedValueOnce(new Error("tab unavailable"));
		bridge.getTabs.mockRejectedValueOnce(new Error("view unavailable"));
		await expect(act(() => result.current.selectTab("t1"))).resolves.toBeUndefined();
		expect(result.current.tabNotice).toBe("");
	});

	// Regression: the main process can mutate its own tab state (e.g. the
	// automation runtime's closeTarget callback removes the tab) before still
	// reporting the close as failed. Without a resync, the renderer kept
	// showing the already-closed tab's row forever — every retry re-failed
	// identically since the main process had nothing left to close.
	it("resyncs tab state from the main process after a failed close, instead of leaving a ghost row", async () => {
		const bridge = setupBridge();
		const { result } = renderBrowserView();
		await waitFor(() => expect(result.current.tabs.map((tab) => tab.id)).toEqual(["t1"]));
		act(() =>
			bridge.emitTabs({
				viewId: "42:sess-1",
				activeTabId: "t2",
				tabs: [
					tab("t1", "http://localhost:3000/", "First", false),
					tab("t2", "http://localhost:4173/", "Second", true),
				],
				change: { kind: "popup", tabId: "t2" },
			}),
		);

		// The close call rejects, but the main process actually removed t2
		// already — getTabs reflects that reality.
		bridge.closeTab.mockRejectedValueOnce(new Error("agent-browser lost the connection mid-command"));
		bridge.getTabs.mockResolvedValueOnce({
			viewId: "42:sess-1",
			activeTabId: "t1",
			tabs: [
				tab("t1", "http://localhost:3000/", "First", true),
			],
		});

		await act(() => result.current.closeTab("t2"));

		await waitFor(() => expect(result.current.tabs.map((tab) => tab.id)).toEqual(["t1"]));
	});

	// Regression: closeTab can silently no-op — the underlying native close
	// fails and the tab stays in session.tabs — and the recently-closed
	// capture used to run before ever checking the response, so a tab that
	// visibly failed to close still showed up as "closed" anyway.
	it("does not remember a tab as closed when the main process reports it is still open", async () => {
		const bridge = setupBridge();
		const { result } = renderBrowserView();

		await waitFor(() => expect(result.current.tabs.map((tab) => tab.id)).toEqual(["t1"]));
		act(() =>
			bridge.emitTabs({
				viewId: "42:sess-1",
				activeTabId: "t2",
				tabs: [
					tab("t1", "http://localhost:3000/", "First", false),
					tab("t2", "http://localhost:4173/", "Second", true),
				],
				change: { kind: "popup", tabId: "t2" },
			}),
		);

		bridge.closeTab.mockResolvedValueOnce({
			viewId: "42:sess-1",
			activeTabId: "t2",
			tabs: [
				tab("t1", "http://localhost:3000/", "First", false),
				tab("t2", "http://localhost:4173/", "Second", true),
			],
		});
		await act(() => result.current.closeTab("t2"));

		expect(result.current.closedTabs).toEqual([]);
		expect(result.current.tabs.map((tab) => tab.id)).toEqual(["t1", "t2"]);
	});

	it("does not remember a closed blank tab, since there is nothing to reopen", async () => {
		const bridge = setupBridge();
		const { result } = renderBrowserView();

		await waitFor(() => expect(result.current.tabs.map((tab) => tab.id)).toEqual(["t1"]));
		act(() =>
			bridge.emitTabs({
				viewId: "42:sess-1",
				activeTabId: "t2",
				tabs: [
					{ id: "t1", url: "", title: "", active: false },
					tab("t2", "http://localhost:4173/", "Second", true),
				],
				change: { kind: "popup", tabId: "t2" },
			}),
		);

		bridge.closeTab.mockResolvedValueOnce({
			viewId: "42:sess-1",
			activeTabId: "t2",
			tabs: [
				tab("t2", "http://localhost:4173/", "Second", true),
			],
		});
		await act(() => result.current.closeTab("t1"));
		expect(result.current.closedTabs).toEqual([]);
	});

	// Regression: the main process reports a freshly-opened tab as the literal
	// string "about:blank" once its initial load settles (see
	// isBlankBrowserEntry in browser-view-host.ts), not an empty string. A
	// plain truthiness check on the url treated that as "real" content, so
	// closing a tab nobody had navigated in showed up in Recently Closed.
	it("does not remember a closed about:blank tab, since there is nothing to reopen", async () => {
		const bridge = setupBridge();
		const { result } = renderBrowserView();

		await waitFor(() => expect(result.current.tabs.map((tab) => tab.id)).toEqual(["t1"]));
		act(() =>
			bridge.emitTabs({
				viewId: "42:sess-1",
				activeTabId: "t2",
				tabs: [
					{ id: "t1", url: "about:blank", title: "", active: false },
					tab("t2", "http://localhost:4173/", "Second", true),
				],
				change: { kind: "popup", tabId: "t2" },
			}),
		);

		bridge.closeTab.mockResolvedValueOnce({
			viewId: "42:sess-1",
			activeTabId: "t2",
			tabs: [
				tab("t2", "http://localhost:4173/", "Second", true),
			],
		});
		await act(() => result.current.closeTab("t1"));
		expect(result.current.closedTabs).toEqual([]);
	});

	// Regression: unlike live tabs (kept alive in the main process and simply
	// re-fetched), Recently Closed was built up purely from this hook's own
	// state — so switching sessions and back lost the list for good, even
	// though nothing about that session actually changed.
	it("keeps a session's Recently Closed list across switching away and back", async () => {
		const bridge = setupBridge();
		const { result, rerender } = renderHook(
			({ sessionId }) => useBrowserView({ sessionId, active: true, poppedOut: false }),
			{ initialProps: { sessionId: "sess-1" } },
		);
		await waitFor(() => expect(result.current.tabs.map((tab) => tab.id)).toEqual(["t1"]));
		act(() =>
			bridge.emitTabs({
				viewId: "42:sess-1",
				activeTabId: "t2",
				tabs: [
					tab("t1", "http://localhost:3000/", "First", false),
					tab("t2", "http://localhost:4173/", "Second", true),
				],
				change: { kind: "popup", tabId: "t2" },
			}),
		);
		await act(() => result.current.closeTab("t2"));
		expect(result.current.closedTabs).toEqual([
			{
				id: "t2",
				url: "http://localhost:4173/",
				title: "Second",
				favicon: undefined,
			},
		]);

		rerender({ sessionId: "sess-2" });
		await waitFor(() => expect(result.current.viewId).toBe("42:sess-2"));
		expect(result.current.closedTabs).toEqual([]);

		rerender({ sessionId: "sess-1" });
		await waitFor(() => expect(result.current.viewId).toBe("42:sess-1"));
		expect(result.current.closedTabs).toEqual([
			{
				id: "t2",
				url: "http://localhost:4173/",
				title: "Second",
				favicon: undefined,
			},
		]);
	});

	it("forgets a session's Recently Closed list once the session is genuinely terminated", async () => {
		const bridge = setupBridge();
		const { result, rerender } = renderHook(
			({ terminated }) =>
				useBrowserView({
					sessionId: "sess-1",
					active: true,
					poppedOut: false,
					terminated,
				}),
			{ initialProps: { terminated: false } },
		);
		await waitFor(() => expect(result.current.tabs.map((tab) => tab.id)).toEqual(["t1"]));
		act(() =>
			bridge.emitTabs({
				viewId: "42:sess-1",
				activeTabId: "t2",
				tabs: [
					tab("t1", "http://localhost:3000/", "First", false),
					tab("t2", "http://localhost:4173/", "Second", true),
				],
				change: { kind: "popup", tabId: "t2" },
			}),
		);
		await act(() => result.current.closeTab("t2"));
		expect(result.current.closedTabs).toHaveLength(1);

		rerender({ terminated: true });
		await waitFor(() => expect(result.current.viewId).toBe(""));

		const revived = renderBrowserView();
		await waitFor(() => expect(revived.result.current.tabs.map((tab) => tab.id)).toEqual(["t1"]));
		expect(revived.result.current.closedTabs).toEqual([]);
	});

	it("remeasures the live native view while moving between panel and maximized browser slots", async () => {
		const bridge = setupBridge();
		const slot = createSlot();
		const { result, rerender } = renderHook(
			({ poppedOut }) => useBrowserView({ sessionId: "sess-1", active: true, poppedOut }),
			{ initialProps: { poppedOut: false } },
		);
		await waitFor(() => expect(result.current.viewId).toBe("42:sess-1"));
		act(() =>
			bridge.emit({
				viewId: "42:sess-1",
				url: "http://localhost:3000/",
				title: "",
				canGoBack: false,
				canGoForward: false,
				isLoading: false,
			}),
		);
		act(() => result.current.slotRef(slot));
		await waitFor(() =>
			expect(bridge.setBounds).toHaveBeenCalledWith(expect.objectContaining({
				viewId: "42:sess-1",
				rect: { x: 12, y: 34, width: 320, height: 240 },
				visible: true,
			})),
		);
		bridge.setBounds.mockClear();

		act(() => {
			rerender({ poppedOut: true });
		});

		await waitFor(() =>
			expect(bridge.setBounds).toHaveBeenCalledWith(expect.objectContaining({
				viewId: "42:sess-1",
				rect: { x: 12, y: 34, width: 320, height: 240 },
				visible: true,
			})),
		);
	});

	it("clamps the native view to its resizable-panel column when the slot overspills", async () => {
		const bridge = setupBridge();
		// The slot is wider than its column (e.g. the `min-w-[280px]` wrapper on a
		// narrower inspector panel). The native overlay isn't clipped by DOM
		// overflow, so the reported bounds must be intersected with the column.
		const column = document.createElement("div");
		column.setAttribute("data-panel", "");
		column.getBoundingClientRect = vi.fn(() => ({
			x: 100,
			y: 0,
			width: 150,
			height: 600,
			top: 0,
			right: 250,
			bottom: 600,
			left: 100,
			toJSON: () => ({}),
		}));
		const slot = createSlot();
		column.appendChild(slot);
		document.body.appendChild(column);

		const { result } = renderBrowserView();
		await waitFor(() => expect(bridge.ensure).toHaveBeenCalledWith("sess-1", false));
		act(() =>
			bridge.emit({
				viewId: "42:sess-1",
				url: "http://localhost:3000/",
				title: "",
				canGoBack: false,
				canGoForward: false,
				isLoading: false,
			}),
		);
		act(() => result.current.slotRef(slot));

		await waitFor(() =>
			expect(bridge.setBounds).toHaveBeenCalledWith(expect.objectContaining({
				viewId: "42:sess-1",
				// Left edge is inset by the resize handle's reserved 6px (see
				// RESIZE_HANDLE_RESERVE_PX in useBrowserView.ts) so the handle's
				// hit area, inside this same column, is never covered by the view.
				rect: { x: 106, y: 34, width: 144, height: 240 },
				visible: true,
			})),
		);
	});

	it("re-measures after a layout transition settles, catching a position-only shift", async () => {
		// A ResizeObserver fires on size changes only; entering pop-out / opening the
		// inspector moves the slot to a new x without resizing it, so the transition
		// itself must drive a settle re-measure or the native overlay keeps stale
		// (spilled) bounds. This is the regression behind the preview covering the
		// terminal until an unrelated window resize fixed it.
		vi.useFakeTimers();
		try {
			const bridge = setupBridge();
			const slot = createSlot();
			const { result, rerender } = renderHook(
				({ poppedOut }) => useBrowserView({ sessionId: "sess-1", active: true, poppedOut }),
				{ initialProps: { poppedOut: false } },
			);
			// ensure() resolves on a microtask; flush it without advancing timers.
			await act(async () => {
				await Promise.resolve();
			});
			// Simulate a real nav state with URL so the positioning effect shows the view.
			act(() =>
				bridge.emit({
					viewId: "42:sess-1",
					url: "http://localhost:3000/",
					title: "",
					canGoBack: false,
					canGoForward: false,
					isLoading: false,
				}),
			);
			act(() => result.current.slotRef(slot));
			// Flush the mount measure (immediate frame + settle timer).
			await act(async () => {
				vi.advanceTimersByTime(300);
			});
			expect(bridge.setBounds).toHaveBeenCalled();

			// Pop-out transition: the immediate frame captures the still-animating
			// geometry; the final position only lands once the panel has settled.
			act(() => rerender({ poppedOut: true }));
			await act(async () => {
				vi.advanceTimersByTime(20);
			});
			bridge.setBounds.mockClear();
			slot.getBoundingClientRect = vi.fn(() => ({
				x: 240,
				y: 34,
				width: 320,
				height: 240,
				top: 34,
				right: 560,
				bottom: 274,
				left: 240,
				toJSON: () => ({}),
			}));
			await act(async () => {
				vi.advanceTimersByTime(300);
			});
			expect(bridge.setBounds).toHaveBeenCalledWith(
				expect.objectContaining({
					rect: expect.objectContaining({ x: 240, width: 320 }),
				}),
			);
		} finally {
			vi.useRealTimers();
		}
	});
});
