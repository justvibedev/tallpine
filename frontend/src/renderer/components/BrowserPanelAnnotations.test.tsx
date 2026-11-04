import {
	render,
	postMock,
	hookState,
	session,
	renderPanel,
	rerenderPanel,
	annotationPayload,
	PersistentBrowserPanelView,
} from "./BrowserPanel.test.setup";

import {
	act,
	fireEvent,
	renderHook,
	screen,
	waitFor,
	within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {
	beforeEach,
	describe,
	expect,
	it,
	vi,
} from "vitest";
import { BrowserPanel, useBrowserAnnotationQueue } from "./BrowserPanel";
import { BrowserTopTabDragOverlay } from "./browser/BrowserTabs";
import { useUiStore } from "../stores/ui-store";
import { tallpineBridge } from "../lib/bridge";
import { TooltipProvider } from "./ui/tooltip";
import type { BrowserAnnotationCancelPayload, BrowserAnnotationSubmitPayload } from "../../shared/browser-annotations";
import type { BrowserDownloadsState } from "../../shared/browser-downloads";



describe("BrowserPanel", () => {
	const annotationSubmitListeners = new Set<(payload: BrowserAnnotationSubmitPayload) => void>();
	const annotationCancelListeners = new Set<(payload: BrowserAnnotationCancelPayload) => void>();
	let focusLocationListener: ((viewId: string) => void) | undefined;
	let reopenClosedTabListener: ((viewId: string) => void) | undefined;
	const pageFocusListeners = new Set<(viewId: string) => void>();
	const downloadListeners = new Set<(state: BrowserDownloadsState) => void>();

	async function openBrowserControls() {
		await userEvent.click(screen.getByRole("button", { name: "Browser controls" }));
	}

	beforeEach(() => {
		useUiStore.setState({ globalToast: null, settingsModal: null });
		hookState.navigate.mockReset();
		hookState.goBack.mockReset();
		hookState.goForward.mockReset();
		hookState.reload.mockReset();
		hookState.stop.mockReset();
		hookState.findInPage.mockReset();
		hookState.stopFindInPage.mockReset();
		hookState.findOpenRequest = 0;
		hookState.findState = {
			viewId: "42:sess-1", tabId: "t1", query: "", activeMatchOrdinal: 0, matches: 0, finalUpdate: true,
		};
		hookState.selectTab.mockReset();
		hookState.closeTab.mockReset();
		hookState.openTab.mockReset();
		hookState.reopenClosedTab.mockReset();
		hookState.closedTabs = [];
		hookState.openDevTools.mockReset();
		hookState.closeDevTools.mockReset();
		hookState.reconnectRuntime.mockReset();
		hookState.reconnectRuntime.mockResolvedValue(undefined);
		hookState.devtoolsState = { viewId: "42:sess-1", open: false, activeTabId: "t1" };
		hookState.navState = {
			viewId: "42:sess-1",
			url: "",
			title: "",
			canGoBack: false,
			canGoForward: false,
			isLoading: false,
		};
		hookState.setAnnotationMode.mockReset();
		hookState.setAnnotationMode.mockResolvedValue(undefined);
		hookState.annotationAction.mockReset();
		hookState.annotationAction.mockResolvedValue(undefined);
		hookState.annotationMode = false;
		hookState.annotationState = { count: 0, screenshotCount: 0, hasDraft: false };
		hookState.runtimeState = { connected: true, connecting: false, rejected: false };
		postMock.mockReset();
		postMock.mockResolvedValue({ data: {} });
		annotationSubmitListeners.clear();
		annotationCancelListeners.clear();
		pageFocusListeners.clear();
		downloadListeners.clear();
		window.tallpine!.browser.onPageFocus = vi.fn((listener: (viewId: string) => void) => {
			pageFocusListeners.add(listener);
			return () => pageFocusListeners.delete(listener);
		});
		window.tallpine!.browser.onAnnotationSubmit = vi.fn((listener: (payload: BrowserAnnotationSubmitPayload) => void) => {
			annotationSubmitListeners.add(listener);
			return () => {
				annotationSubmitListeners.delete(listener);
			};
		});
		window.tallpine!.browser.onAnnotationCancel = vi.fn((listener: (payload: BrowserAnnotationCancelPayload) => void) => {
			annotationCancelListeners.add(listener);
			return () => {
				annotationCancelListeners.delete(listener);
			};
		});
		window.tallpine!.browser.reconnectRuntime = hookState.reconnectRuntime;
		window.tallpine!.browser.historySuggestions = vi.fn(async () => []);
		window.tallpine!.browser.historyFavicon = vi.fn(async () => undefined);
		window.tallpine!.browser.captureScreenshot = vi.fn(async () => undefined);
		window.tallpine!.browser.downloads.list = vi.fn(async () => ({ downloads: [] }));
		window.tallpine!.browser.downloads.onChanged = vi.fn((listener) => {
			downloadListeners.add(listener);
			return () => downloadListeners.delete(listener);
		});
		window.tallpine!.browser.selectProfile = vi.fn(async () => undefined);
		window.tallpine!.browserProfiles.list = vi.fn(async () => ({ profiles: [], defaultProfileId: null }));
		window.tallpine!.browser.notifyPanelUsed = vi.fn();
		window.tallpine!.browser.notifyPanelBlur = vi.fn();
		window.tallpine!.browser.onFocusLocation = vi.fn((listener: (viewId: string) => void) => {
			focusLocationListener = listener;
			return () => {
				if (focusLocationListener === listener) focusLocationListener = undefined;
			};
		});
		window.tallpine!.browser.onReopenClosedTab = vi.fn((listener: (viewId: string) => void) => {
			reopenClosedTabListener = listener;
			return () => {
				if (reopenClosedTabListener === listener) reopenClosedTabListener = undefined;
			};
		});
		hookState.previewUrl = undefined;
		hookState.profileState = { viewId: "42:sess-1", profileId: null, temporary: true };
		hookState.tabs = [{ id: "t1", url: "", title: "", active: true }];
		hookState.activeTabId = "t1";
		hookState.tabNotice = "";
		hookState.navState = {
			viewId: "42:sess-1",
			url: "",
			title: "",
			canGoBack: false,
			canGoForward: false,
			isLoading: false,
		};
	});

	it("uses the active app theme for the static browser preview", () => {
		hookState.navState = { ...hookState.navState, url: "http://localhost:5173/" };
		const tallpine = window.tallpine;
		Object.defineProperty(window, "tallpine", { configurable: true, value: undefined });
		try {
			renderPanel();

			const preview = screen.getByText("Demo app preview").closest(".bg-preview, .bg-background");
			expect(preview).toHaveClass("bg-background", "text-foreground");
		} finally {
			Object.defineProperty(window, "tallpine", { configurable: true, value: tallpine });
		}
	});

	it("binds navigation controls to nav state", async () => {
		hookState.navState = {
			viewId: "42:sess-1",
			url: "http://localhost:5173/",
			title: "Local app",
			canGoBack: true,
			canGoForward: false,
			isLoading: true,
		};
		renderPanel();

		await userEvent.click(screen.getByRole("button", { name: /back/i }));
		await userEvent.click(screen.getByRole("button", { name: /stop/i }));

		expect(hookState.goBack).toHaveBeenCalled();
		expect(screen.getByRole("button", { name: /forward/i })).toBeDisabled();
		expect(hookState.stop).toHaveBeenCalled();
	});

	it("uses shared Tallpine tooltips for toolbar controls", async () => {
		hookState.navState = {
			viewId: "42:sess-1",
			url: "http://localhost:5173/",
			title: "Local app",
			canGoBack: true,
			canGoForward: false,
			isLoading: false,
		};
		renderPanel();

		const backButton = screen.getByRole("button", { name: /back/i });
		expect(backButton.parentElement).toHaveAttribute("data-slot", "tooltip-trigger");
		fireEvent.focus(backButton.parentElement!);
		await waitFor(() => expect(document.querySelector('[data-slot="tooltip-content"]')).not.toBeNull());
	});

	it("uses shared Tallpine tooltips while maximized", async () => {
		hookState.navState = {
			viewId: "42:sess-1",
			url: "http://localhost:5173/",
			title: "Local app",
			canGoBack: true,
			canGoForward: false,
			isLoading: false,
		};
		renderPanel({ poppedOut: true });

		const annotateButton = screen.getByRole("button", { name: /annotate page/i });
		expect(annotateButton.parentElement).toHaveAttribute("data-slot", "tooltip-trigger");
		fireEvent.focus(annotateButton.parentElement!);
		await waitFor(() => expect(document.querySelector('[data-slot="tooltip-content"]')).not.toBeNull());
	});

	it("keeps a shared Tallpine tooltip trigger around a disabled toolbar button", () => {
		// Disabled buttons never dispatch pointer/focus events natively, so the
		// hover listener has to live on a wrapping span around the button rather
		// than on the (potentially disabled) button itself.
		hookState.navState = {
			viewId: "42:sess-1",
			url: "http://localhost:5173/",
			title: "Local app",
			canGoBack: false,
			canGoForward: false,
			isLoading: false,
		};
		renderPanel();

		const backButton = screen.getByRole("button", { name: /back/i });
		expect(backButton).toBeDisabled();
		const wrapper = backButton.parentElement;
		expect(wrapper?.tagName).toBe("SPAN");

		expect(wrapper).toHaveAttribute("data-slot", "tooltip-trigger");
	});

	it("shows browser tabs in a horizontal tab strip and selects them", async () => {
		hookState.tabs = [
			{ id: "t1", url: "http://localhost:3000/", title: "First app", active: false },
			{ id: "t2", url: "http://localhost:4173/", title: "Second app", active: true },
		];
		hookState.activeTabId = "t2";
		renderPanel();

		const tabList = screen.getByRole("tablist", { name: "Browser tabs" });
		const firstTab = within(tabList).getByRole("tab", { name: "First app" });
		const secondTab = within(tabList).getByRole("tab", { name: "Second app" });

		expect(firstTab).toHaveAttribute("aria-selected", "false");
		expect(secondTab).toHaveAttribute("aria-selected", "true");
		expect(firstTab).toHaveAttribute("data-slot", "tooltip-trigger");
		expect(screen.getByRole("button", { name: "Close tab First app" })).toHaveAttribute("data-slot", "tooltip-trigger");
		await userEvent.click(firstTab);
		expect(hookState.selectTab).toHaveBeenCalledWith("t1");
	});

	it("renders the complete tab chrome in the drag overlay", () => {
		render(
			<BrowserTopTabDragOverlay
				tab={{ id: "t1", url: "http://localhost:3000/", title: "First app", active: true }}
			/>,
		);

		const overlay = screen.getByTestId("browser-tab-drag-overlay");
		expect(overlay).toHaveClass("browser-panel__tab--drag-overlay");
		expect(overlay).toHaveTextContent("First app");
		expect(overlay.querySelector(".browser-panel__tab-icon")).not.toBeNull();
		expect(overlay.querySelector(".browser-panel__tab-close")).not.toBeNull();
	});

	it("moves browser tab focus and selection with arrow keys", async () => {
		hookState.tabs = [
			{ id: "t1", url: "http://localhost:3000/", title: "First app", active: true },
			{ id: "t2", url: "http://localhost:4173/", title: "Second app", active: false },
		];
		hookState.activeTabId = "t1";
		renderPanel({ poppedOut: true });

		const tabList = screen.getByRole("tablist", { name: "Browser tabs" });
		const firstTab = within(tabList).getByRole("tab", { name: "First app" });
		const secondTab = within(tabList).getByRole("tab", { name: "Second app" });
		firstTab.focus();
		await userEvent.keyboard("{ArrowRight}");

		expect(secondTab).toHaveFocus();
		expect(hookState.selectTab).toHaveBeenCalledWith("t2");
	});

	it("closes a browser tab from the horizontal tab strip", async () => {
		hookState.tabs = [
			{ id: "t1", url: "http://localhost:3000/", title: "First app", active: true },
			{ id: "t2", url: "http://localhost:4173/", title: "Second app", active: false },
		];
		hookState.activeTabId = "t1";
		renderPanel({ poppedOut: true });

		const tabList = screen.getByRole("tablist", { name: "Browser tabs" });
		await userEvent.click(within(tabList).getByRole("button", { name: "Close tab Second app" }));

		expect(hookState.closeTab).toHaveBeenCalledWith("t2");
	});

	it.each([
		["one tab", [{ id: "t1", url: "https://example.test/", title: "Last page", active: true }]],
		["an empty tab", [{ id: "t1", url: "about:blank", title: "", active: true }]],
		["no tabs", []],
	])("renders no tab strip with %s", (_name, tabs) => {
		hookState.tabs = tabs;
		hookState.activeTabId = tabs[0]?.id ?? "";
		renderPanel();

		expect(screen.queryByTestId("browser-tabs-row")).not.toBeInTheDocument();
		expect(screen.queryByTestId("browser-tab-bar")).not.toBeInTheDocument();
		expect(screen.queryByRole("tablist", { name: "Browser tabs" })).not.toBeInTheDocument();
	});

	it("renders the compact tab strip under the toolbar once there are two tabs", () => {
		hookState.tabs = [
			{ id: "t1", url: "http://localhost:3000/", title: "First app", active: true },
			{ id: "t2", url: "http://localhost:4173/", title: "Second app", active: false },
		];
		hookState.activeTabId = "t1";
		renderPanel();

		const row = screen.getByTestId("browser-tabs-row");
		expect(within(row).getAllByRole("tab")).toHaveLength(2);
		expect(screen.getByTestId("browser-tab-row").compareDocumentPosition(row) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
		// The strip holds tabs only: new tab lives in the toolbar.
		expect(within(row).queryByRole("button", { name: "Open new tab" })).not.toBeInTheDocument();
	});

	it("puts navigation, the address field, new tab, annotate and the menu in one toolbar row", () => {
		renderPanel();

		const row = screen.getByTestId("browser-tab-row");
		for (const name of ["Back", "Forward", "Reload", "Open new tab", "Annotate page", "Browser controls"]) {
			expect(within(row).getByRole("button", { name })).toBeInTheDocument();
		}
		expect(row.querySelector(".browser-panel__address-bar")).not.toBeNull();
	});

	it("shows the empty tab's close icon in its drag overlay", () => {
		render(<BrowserTopTabDragOverlay tab={{ id: "t1", url: "about:blank", title: "", active: true }} />);
		expect(screen.getByTestId("browser-tab-drag-overlay").querySelector(".browser-panel__tab-close")).not.toBeNull();
	});

	it("offers the new tab button from the toolbar", async () => {
		renderPanel();

		await userEvent.click(within(screen.getByTestId("browser-toolbar")).getByRole("button", { name: "Open new tab" }));

		expect(hookState.openTab).toHaveBeenCalledOnce();
	});

	it("does not render a tab-specific agent marker in the tab strip", () => {
		hookState.navState = { ...hookState.navState, url: "http://localhost:5173/" };
		hookState.tabs = [
			{ id: "t1", url: "http://localhost:3000/", title: "First app", active: false },
			{ id: "t2", url: "http://localhost:4173/", title: "Second app", active: true },
		];
		hookState.activeTabId = "t2";
		renderPanel();

		expect(screen.queryByText("Agent", { exact: true })).not.toBeInTheDocument();
	});

	it("closes the only tab from the controls menu, where no tab strip exists", async () => {
		hookState.tabs = [{ id: "t1", url: "https://example.test/", title: "Last page", active: true }];
		hookState.activeTabId = "t1";
		renderPanel();

		await openBrowserControls();
		await userEvent.click(screen.getByRole("menuitem", { name: "Close tab" }));

		expect(hookState.closeTab).toHaveBeenCalledWith("t1");
	});

	it("offers no Close tab item when there is no active tab", async () => {
		hookState.tabs = [];
		hookState.activeTabId = "";
		renderPanel();

		await openBrowserControls();
		expect(screen.queryByRole("menuitem", { name: "Close tab" })).not.toBeInTheDocument();
	});

	it("opens DevTools from the compact browser controls menu", async () => {
		hookState.navState = { ...hookState.navState, url: "http://localhost:3000/" };
		const { rerender } = renderPanel();
		const toolbarButtonCount = screen.getAllByRole("button").length;

		const controls = screen.getByRole("button", { name: "Browser controls" });
		expect(controls).not.toHaveAttribute("aria-pressed");
		await openBrowserControls();
		await userEvent.click(screen.getByRole("menuitem", { name: "Open DevTools" }));
		expect(hookState.openDevTools).toHaveBeenCalledOnce();

		hookState.devtoolsState = { viewId: "42:sess-1", open: true, activeTabId: "t1" };
		rerenderPanel(rerender);
		expect(screen.getAllByRole("button")).toHaveLength(toolbarButtonCount);
		expect(controls).not.toHaveAttribute("aria-pressed");
		expect(controls).not.toHaveClass("bg-accent-strong", "text-accent-foreground");
		await openBrowserControls();
		await userEvent.click(screen.getByRole("menuitem", { name: "Close DevTools" }));
		expect(hookState.closeDevTools).toHaveBeenCalledOnce();
	});

	it("does not highlight browser controls when a saved profile is active", () => {
		hookState.profileState = {
			viewId: "42:sess-1",
			profileId: "11111111-1111-4111-8111-111111111111",
			temporary: false,
		};
		renderPanel();

		const controls = screen.getByRole("button", { name: "Browser controls" });
		expect(controls).not.toHaveAttribute("aria-pressed");
		expect(controls).not.toHaveClass("bg-accent-strong", "text-accent-foreground");
	});

	it("disables DevTools in the controls menu until the active tab has a page", async () => {
		const { rerender } = renderPanel();
		await openBrowserControls();
		expect(screen.getByRole("menuitem", { name: "Open DevTools" })).toHaveAttribute("data-disabled");

		hookState.navState = { ...hookState.navState, url: "http://localhost:3000/" };
		rerenderPanel(rerender);
		expect(screen.getByRole("menuitem", { name: "Open DevTools" })).not.toHaveAttribute("data-disabled");
	});

	it("marks blank native panels as opaque and loaded panels as live", () => {
		const { rerender } = renderPanel();
		expect(screen.getByTestId("browser-panel")).toHaveAttribute("data-browser-native-page", "empty");

		hookState.navState = { ...hookState.navState, url: "http://localhost:3000/" };
		rerenderPanel(rerender);
		expect(screen.getByTestId("browser-panel")).toHaveAttribute("data-browser-native-page", "live");
	});

	it("swallows a failed tab selection from the tab strip", async () => {
		hookState.tabs = [
			{ id: "t1", url: "http://localhost:3000/", title: "First app", active: true },
			{ id: "t2", url: "http://localhost:4173/", title: "Second app", active: false },
		];
		hookState.selectTab.mockRejectedValueOnce(new Error("selection failed"));
		renderPanel();

		const tabList = screen.getByRole("tablist", { name: "Browser tabs" });
		await userEvent.click(within(tabList).getByRole("tab", { name: "Second app" }));

		await waitFor(() => expect(hookState.selectTab).toHaveBeenCalledWith("t2"));
	});

	it("reopens the most recently closed tab from the controls menu", async () => {
		hookState.closedTabs = [{ id: "t3", url: "http://localhost:5173/", title: "Closed app" }];
		renderPanel();

		await openBrowserControls();
		await userEvent.click(screen.getByRole("menuitem", { name: "Reopen closed tab" }));

		expect(hookState.reopenClosedTab).toHaveBeenCalledWith();
	});

	it("hides the reopen action when nothing has been closed", async () => {
		renderPanel();
		await openBrowserControls();
		expect(screen.queryByRole("menuitem", { name: "Reopen closed tab" })).not.toBeInTheDocument();
	});

	it("keeps opening and reopening tabs available with many tabs", async () => {
		hookState.tabs = Array.from({ length: 20 }, (_, i) => ({
			id: `t${i}`,
			url: `http://localhost:3000/${i}`,
			title: `Tab ${i}`,
			active: i === 0,
		}));
		hookState.closedTabs = [{ id: "closed", url: "http://localhost:5173/", title: "Closed app" }];
		renderPanel();

		expect(screen.getByRole("button", { name: "Open new tab" })).toBeEnabled();

		await openBrowserControls();
		await userEvent.click(screen.getByRole("menuitem", { name: "Reopen closed tab" }));
		expect(hookState.reopenClosedTab).toHaveBeenCalledWith();
	});

	it("surfaces a popup-created tab notice in the toolbar", () => {
		hookState.tabNotice = "Opened new tab";
		renderPanel();

		expect(screen.getByText("Opened new tab")).toBeInTheDocument();
	});

	it("shows empty and error states", () => {
		hookState.navState = { ...hookState.navState, error: "Connection refused" };
		renderPanel();

		expect(screen.getByText("Enter a URL or click one in the terminal.")).toBeInTheDocument();
		expect(screen.getByText("Connection refused")).toBeInTheDocument();
	});

	it("toggles pop-out mode", async () => {
		const onTogglePopOut = vi.fn();
		hookState.navState = { ...hookState.navState, url: "http://localhost:5173/" };
		render(<BrowserPanel active onTogglePopOut={onTogglePopOut} poppedOut={false} session={session} />);

		await openBrowserControls();
		await userEvent.click(screen.getByRole("menuitem", { name: /pop out/i }));

		expect(onTogglePopOut).toHaveBeenCalledWith(true);
	});

	it("shows a one-click return button while popped out", async () => {
		const onTogglePopOut = vi.fn();
		render(<BrowserPanel active onTogglePopOut={onTogglePopOut} poppedOut session={session} />);

		const returnButton = screen.getByRole("button", { name: "Return to panel" });
		await openBrowserControls();
		expect(screen.queryByRole("menuitem", { name: "Return to panel" })).not.toBeInTheDocument();
		await userEvent.keyboard("{Escape}");
		await userEvent.click(returnButton);

		expect(onTogglePopOut).toHaveBeenCalledWith(false);
	});

	it("keeps the one-click return action out of the docked toolbar", () => {
		renderPanel();

		expect(screen.queryByRole("button", { name: "Return to panel" })).not.toBeInTheDocument();
	});

	it("keeps workspace sizing controls out of the browser toolbar", async () => {
		renderPanel();

		expect(screen.queryByRole("button", { name: /focus browser workspace/i })).not.toBeInTheDocument();
		await openBrowserControls();
		expect(screen.getByRole("menuitem", { name: /pop out/i })).toBeInTheDocument();
	});

	it("pops out an empty browser", async () => {
		const onTogglePopOut = vi.fn();
		render(<BrowserPanel active onTogglePopOut={onTogglePopOut} poppedOut={false} session={session} />);

		await openBrowserControls();
		const popOut = screen.getByRole("menuitem", { name: /pop out/i });
		expect(popOut).not.toBeDisabled();
		await userEvent.click(popOut);

		expect(onTogglePopOut.mock.calls[0]?.[0]).toBe(true);
	});

	it("enables annotation mode from the toolbar when a page is loaded", async () => {
		hookState.navState = { ...hookState.navState, url: "http://localhost:5173/" };
		renderPanel();

		await userEvent.click(screen.getByRole("button", { name: /annotate/i }));

		expect(hookState.setAnnotationMode).toHaveBeenCalledWith(true);
	});

	it("does not render a global browser activity status", () => {
		hookState.navState = { ...hookState.navState, url: "http://localhost:5173/" };

		renderPanel();

		expect(screen.queryByText("Agent clicking")).not.toBeInTheDocument();
		expect(screen.queryByText("Agent using browser")).not.toBeInTheDocument();
	});

	it("renders the premium browser shell hooks in the default view", () => {
		renderPanel();

		expect(screen.getByTestId("browser-toolbar")).toHaveClass("browser-panel__toolbar");
		expect(screen.getByTestId("browser-viewport")).toHaveClass("browser-panel__viewport");
	});

	it("offers to reconnect when the browser runtime is disconnected", async () => {
		hookState.runtimeState = { connected: false, connecting: false, rejected: false };
		renderPanel();

		expect(screen.getByTestId("browser-runtime-alert")).toHaveTextContent("Browser runtime disconnected.");
		await userEvent.click(screen.getByRole("button", { name: "Reconnect" }));

		expect(hookState.reconnectRuntime).toHaveBeenCalledOnce();
	});

	it.each([
		{
			rejectionCode: "BROWSER_RUNTIME_TOKEN_REJECTED",
			message:
				"The Tallpine daemon refused the browser runtime because another Tallpine launch started it. Run `tallpine stop`, then reopen Tallpine.",
		},
		{
			rejectionCode: "BROWSER_RUNTIME_PROTOCOL_MISMATCH",
			message:
				"The Tallpine daemon refused the browser runtime because it comes from a different Tallpine version than this app. Run `tallpine stop`, then reopen Tallpine.",
		},
		// A malformed hello, a code this app doesn't know, or none: no cause is claimed.
		{ rejectionCode: "BROWSER_RUNTIME_BAD_HELLO", message: "The Tallpine daemon refused the browser runtime. Run `tallpine stop`, then reopen Tallpine." },
		{ rejectionCode: undefined, message: "The Tallpine daemon refused the browser runtime. Run `tallpine stop`, then reopen Tallpine." },
	])(
		"says why the daemon refused the runtime ($rejectionCode), without a Reconnect that can't help (#6466)",
		({ rejectionCode, message }) => {
			hookState.runtimeState = { connected: false, connecting: false, rejected: true, rejectionCode };
			renderPanel();

			const alert = screen.getByTestId("browser-runtime-alert");
			expect(alert).toHaveTextContent(message);
			expect(alert.querySelector("span")).toHaveAttribute("title", message);
			// Reconnecting resends the same token, which the daemon would refuse again.
			expect(screen.queryByRole("button", { name: "Reconnect" })).not.toBeInTheDocument();
		},
	);

	it("shows no runtime alert while a handshake is in flight", () => {
		hookState.runtimeState = { connected: false, connecting: true, rejected: false };
		renderPanel();

		expect(screen.queryByTestId("browser-runtime-alert")).not.toBeInTheDocument();
	});

	it("shows no runtime alert before the first state arrives", () => {
		hookState.runtimeState = null;
		renderPanel();

		expect(screen.queryByTestId("browser-runtime-alert")).not.toBeInTheDocument();
	});

	it("disables annotation mode when no page is loaded", () => {
		renderPanel();

		expect(screen.getByRole("button", { name: /annotate/i })).toBeDisabled();
	});

	it("disables annotation mode on an agent tab, whose page is its viewer's picture", async () => {
		hookState.navState = { ...hookState.navState, url: "https://en.wikipedia.org/wiki/Starfish" };
		const agentTab = { id: "a1", url: "https://en.wikipedia.org/wiki/Starfish", title: "Starfish", active: true, remote: { tabId: "R1" } };
		hookState.tabs = [agentTab];
		renderPanel();

		const button = screen.getByRole("button", { name: /annotate/i });
		expect(button).toBeDisabled();
		// The tooltip says why.
		fireEvent.pointerMove(button.closest('[data-slot="tooltip-trigger"]')!, { pointerType: "mouse" });
		await waitFor(() =>
			expect(document.querySelector('[data-slot="tooltip-content"]')).toHaveTextContent(
				"Annotate works in your tabs, not the agent's",
			),
		);
	});

	it("adds an annotation control row below the toolbar", async () => {
		hookState.navState = { ...hookState.navState, url: "https://example.test/docs" };
		hookState.annotationMode = true;
		hookState.annotationState = { count: 2, screenshotCount: 1, hasDraft: false };

		renderPanel();

		expect(screen.getByTestId("browser-toolbar")).toBeInTheDocument();
		const annotationToolbar = screen.getByTestId("browser-annotation-toolbar");
		expect(annotationToolbar.querySelector(".browser-panel__toolbar--annotation")).not.toBeNull();
		expect(screen.queryByRole("button", { name: "Exit annotation mode" })).not.toBeInTheDocument();
		expect(screen.getByText("2 annotations")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Send annotations" })).toHaveTextContent("2");
		expect(screen.getByRole("button", { name: "Send annotations" })).toHaveClass(
			"browser-panel__annotation-send",
			"bg-primary",
			"text-primary-foreground",
		);

		await userEvent.click(screen.getByRole("button", { name: "Take a screenshot" }));
		expect(hookState.annotationAction).toHaveBeenCalledWith("capture");

		await userEvent.click(screen.getByRole("button", { name: "Discard all comments" }));
		expect(hookState.annotationAction).toHaveBeenCalledWith("discard-all");
	});

	it("previews the original page while Enter or Space is held on the eye button", async () => {
		hookState.navState = { ...hookState.navState, url: "https://example.test/docs" };
		hookState.annotationMode = true;
		hookState.annotationState = { count: 2, screenshotCount: 0, hasDraft: false };
		renderPanel();
		const eye = screen.getAllByRole("button").find((b) => /original/i.test(b.getAttribute("aria-label") ?? ""))!;
		eye.focus();
		hookState.annotationAction.mockClear();

		fireEvent.keyDown(eye, { key: "Enter" });
		fireEvent.keyDown(eye, { key: "Enter", repeat: true });
		expect(hookState.annotationAction.mock.calls).toEqual([["preview-original"]]);
		fireEvent.keyUp(eye, { key: "Enter" });
		expect(hookState.annotationAction).toHaveBeenLastCalledWith("restore-preview");
	});

	it("sends submitted annotation instructions to the session agent", async () => {
		hookState.navState = { ...hookState.navState, url: "http://localhost:5173/" };
		render(
			<BrowserPanel
				active
				onTogglePopOut={() => undefined}
				poppedOut={false}
				session={{ ...session, status: "idle" }}
			/>,
		);

		act(() => {
			annotationSubmitListeners.forEach((listener) =>
				listener(annotationPayload("Make this button blue.", { selector: "button#save", width: 140, height: 36 })),
			);
		});

		expect(await screen.findByText("Sent")).toBeInTheDocument();
		expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/send", {
			params: { path: { sessionId: "sess-1" } },
			body: {
				message: expect.stringContaining("Make this button blue."),
				userAuthored: true,
			},
		});
		const body = postMock.mock.calls[0][1].body as { message: string };
		expect(body.message).toContain("button#save");
		expect(body.message.length).toBeLessThanOrEqual(4096);
	});

	it("stages the captured snapshot and references it in the annotation message", async () => {
		postMock
			.mockResolvedValueOnce({ data: { paths: [".tallpine/attachments/browser-annotation.png"] } })
			.mockResolvedValueOnce({ data: {} });
		hookState.navState = { ...hookState.navState, url: "http://localhost:5173/" };
		render(
			<BrowserPanel active onTogglePopOut={() => undefined} poppedOut={false} session={{ ...session, status: "idle" }} />,
		);

		act(() => {
			annotationSubmitListeners.forEach((listener) =>
				listener({
					...annotationPayload("Make this button blue."),
					snapshot: { mimeType: "image/png", data: "cG5nLWJ5dGVz" },
				}),
			);
		});

		expect(await screen.findByText("Sent")).toBeInTheDocument();
		expect(postMock).toHaveBeenNthCalledWith(1, "/api/v1/sessions/{sessionId}/attachments", {
			params: { path: { sessionId: "sess-1" } },
			body: { attachments: [{ mimeType: "image/png", data: "cG5nLWJ5dGVz" }] },
		});
		const sendBody = postMock.mock.calls[1][1].body as { message: string; attachment?: unknown };
		expect(sendBody.attachment).toBeUndefined();
		expect(sendBody.message).toContain(".tallpine/attachments/browser-annotation.png");
	});

	it("omits the attachment field when the payload has no snapshot", async () => {
		hookState.navState = { ...hookState.navState, url: "http://localhost:5173/" };
		render(
			<BrowserPanel active onTogglePopOut={() => undefined} poppedOut={false} session={{ ...session, status: "idle" }} />,
		);

		act(() => {
			annotationSubmitListeners.forEach((listener) => listener(annotationPayload("Make this button blue.")));
		});

		expect(await screen.findByText("Sent")).toBeInTheDocument();
		const body = postMock.mock.calls[0][1].body as { attachment?: unknown };
		expect(body.attachment).toBeUndefined();
	});

	it("sends a follow-up annotation without waiting for an activity-state cycle", async () => {
		hookState.navState = { ...hookState.navState, url: "http://localhost:5173/" };
		renderPanel();

		act(() => {
			annotationSubmitListeners.forEach((listener) => listener(annotationPayload("Make this button blue.")));
		});
		expect(await screen.findByText("Sent")).toBeInTheDocument();
		expect(postMock).toHaveBeenCalledTimes(1);

		act(() => {
			annotationSubmitListeners.forEach((listener) => listener(annotationPayload("Make this button green.")));
		});

		expect(await screen.findByText("Sent")).toBeInTheDocument();
		expect(postMock).toHaveBeenCalledTimes(2);
		expect((postMock.mock.calls[1][1].body as { message: string }).message).toContain("Make this button green.");
	});

	it("serializes annotations in order exactly once while status remains working", async () => {
		let resolveFirstPost: (value: unknown) => void = () => undefined;
		let resolveSecondPost: (value: unknown) => void = () => undefined;
		postMock
			.mockReturnValueOnce(
				new Promise((resolve) => {
					resolveFirstPost = resolve;
				}),
			)
			.mockReturnValueOnce(
				new Promise((resolve) => {
					resolveSecondPost = resolve;
				}),
			)
			.mockResolvedValueOnce({ data: {} });
		hookState.navState = { ...hookState.navState, url: "http://localhost:5173/" };
		render(
			<BrowserPanel
				active
				onTogglePopOut={() => undefined}
				poppedOut={false}
				session={{ ...session, status: "working" }}
			/>,
		);
		const instructions = ["Make this button blue.", "Make this heading shorter.", "Reduce the card padding."];

		act(() => {
			annotationSubmitListeners.forEach((listener) => {
				instructions.forEach((instruction) => listener(annotationPayload(instruction)));
			});
		});

		expect(postMock).toHaveBeenCalledTimes(1);
		await act(async () => {
			resolveFirstPost({ data: {} });
		});
		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(2));
		expect(postMock).toHaveBeenCalledTimes(2);
		await act(async () => {
			resolveSecondPost({ data: {} });
		});
		expect(await screen.findByText("Sent")).toBeInTheDocument();
		expect(postMock).toHaveBeenCalledTimes(3);
		expect(
			postMock.mock.calls.map(
				(call) => (call[1].body as { message: string }).message.match(/Comment: (.+)/)?.[1],
			),
		).toEqual(instructions);
	});

	it("preserves queued annotations while the BrowserPanelView is unmounted", async () => {
		let resolvePost: (value: unknown) => void = () => undefined;
		postMock
			.mockReturnValueOnce(
				new Promise((resolve) => {
					resolvePost = resolve;
				}),
			)
			.mockResolvedValueOnce({ data: {} });
		hookState.navState = { ...hookState.navState, url: "http://localhost:5173/" };
		const { rerender } = render(<PersistentBrowserPanelView currentSession={session} visible />);

		act(() => {
			annotationSubmitListeners.forEach((listener) => {
				listener(annotationPayload("Make this button blue."));
				listener(annotationPayload("Make this heading shorter."));
			});
		});
		expect(postMock).toHaveBeenCalledTimes(1);

		rerender(<TooltipProvider><PersistentBrowserPanelView currentSession={session} visible={false} /></TooltipProvider>);
		expect(postMock).toHaveBeenCalledTimes(1);

		await act(async () => {
			resolvePost({ data: {} });
		});
		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(2));
		expect(postMock).toHaveBeenCalledTimes(2);
		expect((postMock.mock.calls[0][1].body as { message: string }).message).toContain("Make this button blue.");
		expect((postMock.mock.calls[1][1].body as { message: string }).message).toContain("Make this heading shorter.");

		rerender(<TooltipProvider><PersistentBrowserPanelView currentSession={session} visible /></TooltipProvider>);
		expect(await screen.findByText("Sent")).toBeInTheDocument();
		expect((postMock.mock.calls[1][1].body as { message: string }).message).toContain("Make this heading shorter.");
	});

	it("continues queued delivery across activity status changes", async () => {
		let resolvePost: (value: unknown) => void = () => undefined;
		postMock
			.mockReturnValueOnce(
				new Promise((resolve) => {
					resolvePost = resolve;
				}),
			)
			.mockResolvedValueOnce({ data: {} });
		hookState.navState = { ...hookState.navState, url: "http://localhost:5173/" };
		const { rerender } = renderPanel();
		const payload = annotationPayload("Make this button yellow.");

		act(() => {
			annotationSubmitListeners.forEach((listener) => {
				listener(payload);
				listener(annotationPayload("Make this button blue."));
			});
		});
		rerender(
			<TooltipProvider>
				<BrowserPanel
					active
					onTogglePopOut={() => undefined}
					poppedOut={false}
					session={{ ...session, status: "working" }}
				/>
			</TooltipProvider>,
		);
		await act(async () => {
			resolvePost({ data: {} });
		});
		rerender(
			<TooltipProvider>
				<BrowserPanel
					active
					onTogglePopOut={() => undefined}
					poppedOut={false}
					session={{ ...session, status: "idle" }}
				/>
			</TooltipProvider>,
		);
		expect(await screen.findByText("Sent")).toBeInTheDocument();
		expect(postMock).toHaveBeenCalledTimes(2);
	});

	it("sends submitted annotations while the session status is working", async () => {
		hookState.navState = { ...hookState.navState, url: "http://localhost:5173/" };
		render(
			<BrowserPanel
				active
				onTogglePopOut={() => undefined}
				poppedOut={false}
				session={{ ...session, status: "working" }}
			/>,
		);

		act(() => {
			annotationSubmitListeners.forEach((listener) =>
				listener(annotationPayload("Move this card higher.", { selector: "section", tag: "section", width: 320, height: 180 })),
			);
		});

		expect(await screen.findByText("Sent")).toBeInTheDocument();
		expect(postMock).toHaveBeenCalledTimes(1);
	});

	it.each([
		{ viewedHost: `ao-preview-${"a".repeat(32)}.localhost`, expectedUrl: "http://localhost:5173/design?x=1" },
		{ viewedHost: `ao-preview-${"b".repeat(32)}.localhost`, expectedUrl: "(unknown)" },
	])("maps only this session's preview URL in annotation chat ($viewedHost)", async ({ viewedHost, expectedUrl }) => {
		const ownPreview = `http://ao-preview-${"a".repeat(32)}.localhost:4321/`;
		const resolvePreviewUrl = vi.spyOn(tallpineBridge.remotes, "resolvePreviewUrl").mockResolvedValue(expectedUrl === "(unknown)" ? "" : expectedUrl);
		const previewUrl = vi.spyOn(tallpineBridge.remotes, "previewUrl");
		try {
			const { result } = renderHook(() => useBrowserAnnotationQueue({
				sessionId: "sess-1", hostId: "host-1", sourcePreviewUrl: "http://localhost:5173/", navUrl: ownPreview,
			}));
			const payload = annotationPayload("Fix this.");
			payload.session.page.url = `http://${viewedHost}:4321/design?x=1`;
			act(() => result.current.enqueue(payload));
			await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
			const message = (postMock.mock.calls[0][1].body as { message: string }).message;
			expect(message).toContain(`URL: ${expectedUrl}`);
			expect(message).not.toContain(viewedHost);
			expect(resolvePreviewUrl).toHaveBeenCalledWith("host-1", "sess-1", payload.session.page.url);
			expect(previewUrl).not.toHaveBeenCalled();
		} finally {
			resolvePreviewUrl.mockRestore();
			previewUrl.mockRestore();
		}
	});

	it("clears the annotation delivery confirmation after two seconds", async () => {
		vi.useFakeTimers();
		try {
			const { result } = renderHook(() =>
				useBrowserAnnotationQueue({
					sessionId: "sess-1",
					navUrl: "http://localhost:5173/",
				}),
			);

			act(() => {
				result.current.enqueue(annotationPayload("Make this button blue."));
			});
			await act(async () => {
				await Promise.resolve();
				await Promise.resolve();
			});
			expect(result.current.status).toBe("sent");

			act(() => {
				vi.advanceTimersByTime(1_999);
			});
			expect(result.current.status).toBe("sent");

			act(() => {
				vi.advanceTimersByTime(1);
			});
			expect(result.current.status).toBe("idle");
		} finally {
			vi.useRealTimers();
		}
	});

	it("shows annotation send errors", async () => {
		postMock.mockResolvedValue({ error: { message: "Tallpine daemon is not ready." } });
		hookState.navState = { ...hookState.navState, url: "http://localhost:5173/" };
		renderPanel();

		act(() => {
			annotationSubmitListeners.forEach((listener) => listener(annotationPayload("Make this button blue.")));
		});

		expect(await screen.findByText("Tallpine daemon is not ready.")).toBeInTheDocument();
	});

	it("keeps a failed annotation queued so the user can retry it", async () => {
		postMock
			.mockResolvedValueOnce({ error: { message: "Tallpine daemon is not ready." } })
			.mockResolvedValueOnce({ data: {} });
		hookState.navState = { ...hookState.navState, url: "http://localhost:5173/" };
		renderPanel();

		act(() => {
			annotationSubmitListeners.forEach((listener) =>
				listener(annotationPayload("Keep my original annotation request.", { selector: "button#save" })),
			);
		});

		expect(await screen.findByText("Tallpine daemon is not ready.")).toBeInTheDocument();
		expect(postMock).toHaveBeenCalledTimes(1);

		await userEvent.click(screen.getByRole("button", { name: /retry annotation/i }));

		expect(await screen.findByText("Sent")).toBeInTheDocument();
		expect(postMock).toHaveBeenCalledTimes(2);
		const retryBody = postMock.mock.calls[1][1].body as { message: string };
		expect(retryBody.message).toContain("Keep my original annotation request.");
		expect(retryBody.message).toContain("button#save");
	});

	it("clears picking state when the page cancels annotation mode", async () => {
		hookState.navState = { ...hookState.navState, url: "http://localhost:5173/" };
		renderPanel();

		await userEvent.click(screen.getByRole("button", { name: /annotate/i }));
		expect(screen.getByText("Pick element")).toBeInTheDocument();

		act(() => {
			annotationCancelListeners.forEach((listener) => listener({ viewId: "42:sess-1", reason: "escape" }));
		});

		expect(screen.queryByText("Pick element")).not.toBeInTheDocument();
	});

	it("uses Tallpine orange for the active annotation status dot", async () => {
		hookState.navState = { ...hookState.navState, url: "http://localhost:5173/" };
		renderPanel();

		const annotateButton = screen.getByRole("button", { name: /annotate/i });
		await userEvent.click(annotateButton);

		expect(annotateButton.querySelector('span[aria-hidden="true"]')).toHaveClass("bg-status-needs-you-dot");
	});

	it("keeps the browser viewport transparent once a native page is loaded, so overlays don't blank it", () => {
		hookState.navState = { ...hookState.navState, url: "http://localhost:5173/" };
		renderPanel();
		expect(screen.getByTestId("browser-viewport")).not.toHaveAttribute("data-placeholder");
	});

	it("keeps an opaque background behind the empty-URL placeholder", () => {
		renderPanel();
		expect(screen.getByTestId("browser-viewport")).toHaveAttribute("data-placeholder", "true");
	});

	it("keeps an opaque background for the static preview fallback when there is no native browser bridge", () => {
		hookState.navState = { ...hookState.navState, url: "http://localhost:5173/" };
		const tallpine = window.tallpine;
		Object.defineProperty(window, "tallpine", { configurable: true, value: undefined });
		try {
			renderPanel();
			expect(screen.getByTestId("browser-viewport")).toHaveAttribute("data-placeholder", "true");
		} finally {
			Object.defineProperty(window, "tallpine", { configurable: true, value: tallpine });
		}
	});
});
