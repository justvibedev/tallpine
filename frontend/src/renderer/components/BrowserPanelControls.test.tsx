import {
	panelElement,
	renderPanel,
	postMock,
	hookState,
	session,
} from "./BrowserPanel.test.setup";

import {
	act,
	fireEvent,
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
import { resetDaemonTcpCache } from "../lib/daemon-tcp";
import { useUiStore } from "../stores/ui-store";
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

	async function openDevicePresets() {
		await openBrowserControls();
		await userEvent.click(screen.getByRole("menuitem", { name: "Device preset" }));
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

	it("navigates to the entered URL on submit", async () => {
		renderPanel();
		const input = screen.getByRole("textbox", { name: /browser url/i });

		await userEvent.clear(input);
		await userEvent.type(input, "localhost:5173{Enter}");

		expect(hookState.navigate).toHaveBeenCalledWith("localhost:5173");
		expect(input).not.toHaveFocus();
	});

	it("toggles find-in-page, searches as text changes, and supports keyboard navigation", async () => {
		let focusFrame: FrameRequestCallback | undefined;
		vi.spyOn(window, "requestAnimationFrame").mockImplementation((callback) => {
			focusFrame = callback;
			return 1;
		});
		const view = renderPanel();
		expect(screen.queryByRole("search")).not.toBeInTheDocument();

		hookState.findOpenRequest = 1;
		view.rerender(
			<TooltipProvider>{panelElement()}</TooltipProvider>,
		);
		const input = await screen.findByRole("textbox", { name: "Find in page" });
		act(() => focusFrame?.(0));
		expect(input).toHaveFocus();

		await userEvent.type(input, "alpha");
		expect(hookState.findInPage).toHaveBeenLastCalledWith("alpha", true, true);

		await userEvent.keyboard("{Enter}");
		expect(hookState.findInPage).toHaveBeenLastCalledWith("alpha", true, false);
		await userEvent.keyboard("{Shift>}{Enter}{/Shift}");
		expect(hookState.findInPage).toHaveBeenLastCalledWith("alpha", false, false);

		hookState.findOpenRequest = 2;
		view.rerender(
			<TooltipProvider>{panelElement()}</TooltipProvider>,
		);
		expect(screen.queryByRole("search")).not.toBeInTheDocument();
		expect(hookState.stopFindInPage).toHaveBeenCalledWith(true);

		hookState.findOpenRequest = 3;
		view.rerender(
			<TooltipProvider>{panelElement()}</TooltipProvider>,
		);
		expect(await screen.findByRole("textbox", { name: "Find in page" })).toBeInTheDocument();
		act(() => focusFrame?.(0));
		await userEvent.keyboard("{Escape}");
		expect(screen.queryByRole("search")).not.toBeInTheDocument();
		expect(hookState.stopFindInPage).toHaveBeenCalledTimes(2);
	});

	it("supports consecutive address-bar navigations after refocusing", async () => {
		renderPanel();
		const input = screen.getByRole("textbox", { name: /browser url/i });

		await userEvent.clear(input);
		await userEvent.type(input, "first.example{Enter}");
		await userEvent.click(input);
		await userEvent.clear(input);
		await userEvent.type(input, "second.example{Enter}");

		expect(hookState.navigate).toHaveBeenNthCalledWith(1, "first.example");
		expect(hookState.navigate).toHaveBeenNthCalledWith(2, "second.example");
		expect(input).not.toHaveFocus();
	});

	it("shows imported history in the shared dropdown and navigates from a suggestion", async () => {
		hookState.profileState = {
			viewId: "42:sess-1",
			profileId: "11111111-1111-4111-8111-111111111111",
			temporary: false,
		};
		window.tallpine!.browser.historySuggestions = vi.fn(async () => [
			{ url: "https://github.com/openai", title: "OpenAI" },
			{ url: "https://gitlab.com/example", title: "GitLab" },
			{ url: "https://github.blog/example", title: "GitHub Blog" },
			{ url: "https://gist.github.com/example", title: "Gist" },
			{ url: "https://githubstatus.com", title: "Fifth result" },
		]);
		let resolveGithubFavicon: (favicon: string | undefined) => void = () => undefined;
		window.tallpine!.browser.historyFavicon = vi.fn(({ url }) =>
			url.startsWith("https://github.com/")
				? new Promise<string | undefined>((resolve) => {
					resolveGithubFavicon = resolve;
				})
				: Promise.resolve(undefined),
		);
		renderPanel();
		const input = screen.getByRole("textbox", { name: /browser url/i });
		const addressBar = screen.getByTestId("browser-address-bar");
		expect(addressBar).not.toHaveClass("browser-panel__address-bar--editing");

		await userEvent.type(input, "g");
		expect(addressBar).toHaveClass("browser-panel__address-bar--editing");

		await waitFor(() => expect(window.tallpine!.browser.historySuggestions).toHaveBeenCalledWith({
			viewId: "42:sess-1",
			query: "g",
		}), { timeout: 2_000 });
		const menu = await screen.findByRole("listbox", { name: "Address suggestions" });
		expect(menu).toHaveAttribute("data-browser-native-overlay", "true");
		expect(menu).toHaveClass("browser-panel__history-suggestions");
		expect(screen.getAllByRole("option")).toHaveLength(4);
		expect(screen.getByText("OpenAI")).toBeInTheDocument();
		expect(screen.getByText("https://github.com/openai")).toBeInTheDocument();
		expect(screen.queryByText("Fifth result")).not.toBeInTheDocument();
		expect(screen.getAllByRole("option")[0]!.querySelector("img")).not.toBeInTheDocument();
		await act(async () => resolveGithubFavicon("data:image/png;base64,github"));
		await waitFor(() => expect(menu.querySelector("img")).toHaveAttribute("src", "data:image/png;base64,github"));
		expect(window.tallpine!.browser.historyFavicon).toHaveBeenCalledWith({
			viewId: "42:sess-1",
			url: "https://github.com/openai",
		});

		await userEvent.click(screen.getAllByRole("option")[0]!);
		expect(hookState.navigate).toHaveBeenCalledWith("https://github.com/openai");
		expect(input).not.toHaveFocus();
		expect(addressBar).not.toHaveClass("browser-panel__address-bar--editing");
		expect(screen.queryByRole("listbox", { name: "Address suggestions" })).not.toBeInTheDocument();
	});

	it("supports keyboard selection in address suggestions", async () => {
		hookState.profileState = {
			viewId: "42:sess-1",
			profileId: "11111111-1111-4111-8111-111111111111",
			temporary: false,
		};
		window.tallpine!.browser.historySuggestions = vi.fn(async () => [
			{ url: "https://github.com/openai", title: "OpenAI" },
			{ url: "https://github.com/justvibedev", title: "Tallpine" },
		]);
		renderPanel();
		const input = screen.getByRole("textbox", { name: /browser url/i });

		await userEvent.type(input, "git");
		await screen.findByRole("listbox", { name: "Address suggestions" });
		await userEvent.keyboard("{ArrowDown}{ArrowDown}{Enter}");

		expect(hookState.navigate).toHaveBeenCalledWith("https://github.com/justvibedev");
	});

	it("keeps address suggestions closed when an escaped request resolves late", async () => {
		hookState.profileState = {
			viewId: "42:sess-1",
			profileId: "11111111-1111-4111-8111-111111111111",
			temporary: false,
		};
		let resolveSuggestions: (suggestions: Array<{ url: string; title?: string }>) => void = () => undefined;
		window.tallpine!.browser.historySuggestions = vi.fn(() => new Promise<Array<{ url: string; title?: string }>>((resolve) => {
			resolveSuggestions = resolve;
		}));
		renderPanel();
		const input = screen.getByRole("textbox", { name: /browser url/i });

		await userEvent.type(input, "git");
		await waitFor(() => expect(window.tallpine!.browser.historySuggestions).toHaveBeenCalledOnce());
		await userEvent.keyboard("{Escape}");
		await act(async () => resolveSuggestions([{ url: "https://github.com/openai", title: "OpenAI" }]));

		expect(screen.queryByRole("listbox", { name: "Address suggestions" })).not.toBeInTheDocument();
	});

	it("does not search imported history until the address is edited", async () => {
		hookState.profileState = {
			viewId: "42:sess-1",
			profileId: "11111111-1111-4111-8111-111111111111",
			temporary: false,
		};
		hookState.navState = { ...hookState.navState, url: "https://example.com/current" };
		renderPanel();
		const input = screen.getByRole("textbox", { name: /browser url/i });

		fireEvent.focus(input);
		await new Promise((resolve) => window.setTimeout(resolve, 150));
		expect(window.tallpine!.browser.historySuggestions).not.toHaveBeenCalled();

		await userEvent.clear(input);
		await userEvent.type(input, "exa");
		await waitFor(() =>
			expect(window.tallpine!.browser.historySuggestions).toHaveBeenCalledWith({
				viewId: "42:sess-1",
				query: "exa",
			}),
		);
	});

	it("marks browser UI as used and focuses the address bar for a matching shortcut request", async () => {
		renderPanel();
		const input = screen.getByRole("textbox", { name: /browser url/i }) as HTMLInputElement;
		input.value = "http://localhost:5173/path";

		act(() => focusLocationListener?.("42:sess-1"));

		expect(input).toHaveFocus();
		expect(input.selectionStart).toBe(0);
		expect(input.selectionEnd).toBe(input.value.length);
		expect(window.tallpine!.browser.notifyPanelUsed).toHaveBeenCalledWith("42:sess-1");
	});

	it("keeps browser shortcuts targeted when the portaled address bar receives focus", () => {
		const topbarHost = document.createElement("div");
		document.body.appendChild(topbarHost);
		renderPanel({ topbarHost });
		vi.mocked(window.tallpine!.browser.notifyPanelUsed).mockClear();

		fireEvent.focus(screen.getByRole("textbox", { name: /browser url/i }));

		expect(window.tallpine!.browser.notifyPanelUsed).toHaveBeenCalledWith("42:sess-1");
	});

	it("does not clear the browser shortcut target when focus moves into the portaled address bar", () => {
		const topbarHost = document.createElement("div");
		document.body.appendChild(topbarHost);
		renderPanel({ topbarHost });
		const panel = screen.getByTestId("browser-panel");
		const input = screen.getByRole("textbox", { name: /browser url/i });
		vi.mocked(window.tallpine!.browser.notifyPanelBlur).mockClear();

		fireEvent.blur(panel, { relatedTarget: input });

		expect(window.tallpine!.browser.notifyPanelBlur).not.toHaveBeenCalled();
	});

	it("does not clear the browser shortcut target when focus blurs to body or leaves into native page", () => {
		renderPanel();
		const panel = screen.getByTestId("browser-panel");
		vi.mocked(window.tallpine!.browser.notifyPanelBlur).mockClear();

		fireEvent.blur(panel, { relatedTarget: document.body });
		expect(window.tallpine!.browser.notifyPanelBlur).not.toHaveBeenCalled();

		fireEvent.blur(panel, { relatedTarget: null });
		expect(window.tallpine!.browser.notifyPanelBlur).not.toHaveBeenCalled();

		const outside = document.createElement("button");
		document.body.appendChild(outside);
		fireEvent.blur(panel, { relatedTarget: outside });
		expect(window.tallpine!.browser.notifyPanelBlur).toHaveBeenCalledWith("42:sess-1");
		outside.remove();
	});

	it("reopens the most recently closed tab for a matching shortcut request", () => {
		hookState.closedTabs = [
			{ id: "latest", url: "http://localhost:5173/latest", title: "Latest" },
			{ id: "older", url: "http://localhost:5173/older", title: "Older" },
		];
		renderPanel();

		act(() => reopenClosedTabListener?.("42:sess-1"));

		expect(hookState.reopenClosedTab).toHaveBeenCalledWith();
	});

	it("shows the domain only in the URL input when unfocused", () => {
		hookState.navState = {
			...hookState.navState,
			url: "https://www.google.com/search?q=agent+orchestrator#results",
		};

		renderPanel();

		const input = screen.getByRole("textbox", { name: /browser url/i });
		expect(input).toHaveValue("google.com");
	});

	it("keeps non-HTTP URLs visible when unfocused", () => {
		hookState.navState = {
			...hookState.navState,
			url: "file:///tmp/preview/index.html",
		};

		renderPanel();

		expect(screen.getByRole("textbox", { name: /browser url/i })).toHaveValue(
			"file:///tmp/preview/index.html",
		);
	});

	it("reveals the full URL on focus, reverts to domain when the page takes focus", async () => {
		const url = "https://www.google.com/search?q=agent+orchestrator#results";
		hookState.navState = { ...hookState.navState, url, canGoBack: true };
		const user = userEvent.setup();
		renderPanel();
		const toolbar = screen.getByTestId("browser-toolbar");
		const input = screen.getByRole("textbox", { name: /browser url/i }) as HTMLInputElement;

		await user.click(input);

		await waitFor(() => {
			expect(input).toHaveValue(url);
			expect(input.selectionStart).toBe(0);
			expect(input.selectionEnd).toBe(url.length);
		});
		expect(within(toolbar).getByRole("button", { name: /back/i })).toHaveClass("browser-panel__navigation-btn");
		expect(within(toolbar).getByRole("button", { name: /forward/i })).toHaveClass("browser-panel__navigation-btn");
		expect(within(toolbar).getByRole("button", { name: /reload/i })).toHaveClass("browser-panel__navigation-btn");

		act(() => {
			for (const listener of pageFocusListeners) listener("42:sess-1");
		});

		expect(input).toHaveValue("google.com");
		expect(within(toolbar).getByRole("button", { name: /back/i })).toBeInTheDocument();
	});

	it("keeps the normal toolbar and full URL while maximized", async () => {
		const url = "https://www.google.com/search?q=agent+orchestrator";
		hookState.navState = { ...hookState.navState, url };
		const user = userEvent.setup();
		renderPanel({ poppedOut: true });
		const toolbar = screen.getByTestId("browser-toolbar");
		const input = screen.getByRole("textbox", { name: /browser url/i });

		expect(input).toHaveValue(url);
		expect(input).not.toHaveClass("text-center");
		await user.click(input);

		expect(toolbar).not.toHaveClass("browser-panel__toolbar--url-takeover");
		expect(within(toolbar).getByRole("button", { name: /back/i })).toBeInTheDocument();
		expect(within(toolbar).getByRole("button", { name: /reload/i })).toBeInTheDocument();
	});

	it("opens the current page in the system browser from the address bar", async () => {
		const url = "https://www.google.com/search?q=agent+orchestrator";
		hookState.navState = { ...hookState.navState, url };
		const openExternal = vi.spyOn(window.tallpine!.app, "openExternal").mockResolvedValue(undefined);
		renderPanel();

		fireEvent.click(screen.getByRole("button", { name: /open in system browser/i }));

		await waitFor(() => expect(openExternal).toHaveBeenCalledWith(url));
		openExternal.mockRestore();
	});

	it("disables the system-browser action for a local preview while loopback TCP is closed (B180)", async () => {
		resetDaemonTcpCache();
		hookState.navState = { ...hookState.navState, url: "http://app.s1.localhost:3101/" };
		vi.spyOn(window.tallpine!.app, "daemonTcpOpen").mockResolvedValue(false);
		const openExternal = vi.spyOn(window.tallpine!.app, "openExternal").mockResolvedValue(undefined);
		renderPanel();
		const button = screen.getByRole("button", { name: /open in system browser/i });
		await waitFor(() => expect(button).toHaveAttribute("aria-disabled", "true"));
		fireEvent.click(button);
		expect(openExternal).not.toHaveBeenCalled();
		vi.restoreAllMocks();
		resetDaemonTcpCache();
	});

	it("copies the full current URL from the address bar and confirms it", async () => {
		const url = "https://www.google.com/search?q=agent+orchestrator";
		hookState.navState = { ...hookState.navState, url };
		const writeText = vi.spyOn(window.tallpine!.clipboard, "writeText").mockResolvedValue(undefined);
		renderPanel();

		// The compact address bar displays only the host, but copy must retain the
		// path and query from the underlying navigation state.
		expect(screen.getByRole("textbox", { name: /browser url/i })).toHaveValue("google.com");
		await userEvent.click(screen.getByRole("button", { name: "Copy URL" }));

		expect(writeText).toHaveBeenCalledExactlyOnceWith(url);
		expect(screen.getByRole("button", { name: "URL copied" })).toBeInTheDocument();
		expect(useUiStore.getState().globalToast).toBeNull();
		writeText.mockRestore();
	});

	it("keeps the copy action at the trailing edge when no external action is available", () => {
		hookState.navState = { ...hookState.navState, url: "localhost:5173" };
		renderPanel();

		expect(screen.getByRole("button", { name: "Copy URL" })).toHaveClass("browser-panel__url-copy--only");
		expect(screen.queryByRole("button", { name: /open in system browser/i })).not.toBeInTheDocument();
	});

	it("does not show URL actions on a blank browser tab", () => {
		renderPanel();

		expect(screen.queryByRole("button", { name: "Copy URL" })).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /open in system browser/i })).not.toBeInTheDocument();
	});

	it("opens and copies a remote preview at the address a browser outside Tallpine can load", async () => {
		const url = "https://ao-preview-0123456789abcdef0123456789abcdef.localhost:41000/dash?x=1#top";
		const external = "http://ao-preview-0123456789abcdef0123456789abcdef.localhost:5000/dash?x=1#top";
		hookState.navState = { ...hookState.navState, url };
		const externalUrl = vi.spyOn(window.tallpine!.remotes, "externalUrl").mockResolvedValue(external);
		const openExternal = vi.spyOn(window.tallpine!.app, "openExternal").mockResolvedValue(undefined);
		const writeText = vi.spyOn(window.tallpine!.clipboard, "writeText").mockResolvedValue(undefined);
		renderPanel({ session: { ...session, hostId: "h_workbox" } });

		fireEvent.click(screen.getByRole("button", { name: /open in system browser/i }));
		await waitFor(() => expect(openExternal).toHaveBeenCalledWith(external));
		await userEvent.click(screen.getByRole("button", { name: "Copy URL" }));
		expect(writeText).toHaveBeenCalledExactlyOnceWith(external);
		expect(externalUrl).toHaveBeenCalledWith("h_workbox", url);
		externalUrl.mockRestore();
		openExternal.mockRestore();
		writeText.mockRestore();
	});

	it.each([
		["a page on the host's own localhost", "http://localhost:3000/settings", "R1"],
		["its viewer, before the first report", "https://ao-preview-0123456789abcdef0123456789abcdef.localhost:41000/v/cdp?embed=1", ""],
	])("disables Open in System Browser on an agent tab showing %s, and says why", async (_case, url, tabId) => {
		hookState.navState = { ...hookState.navState, url };
		const agentTab = { id: "a1", url, title: "", active: true, remote: { tabId } };
		hookState.tabs = [agentTab];
		const openExternal = vi.spyOn(window.tallpine!.app, "openExternal").mockResolvedValue(undefined);
		const writeText = vi.spyOn(window.tallpine!.clipboard, "writeText").mockResolvedValue(undefined);
		renderPanel({ session: { ...session, hostId: "h_workbox" } });

		const open = screen.getByRole("button", { name: /open in system browser/i });
		expect(open).toHaveAttribute("aria-disabled", "true");
		fireEvent.click(open);
		fireEvent.pointerMove(open, { pointerType: "mouse" });
		await waitFor(() =>
			expect(document.querySelector('[data-slot="tooltip-content"]')).toHaveTextContent("This page runs on the other computer"),
		);
		expect(openExternal).not.toHaveBeenCalled();
		// Copy stays: it copies the address as shown.
		await userEvent.click(screen.getByRole("button", { name: "Copy URL" }));
		expect(writeText).toHaveBeenCalledExactlyOnceWith(url);
		openExternal.mockRestore();
		writeText.mockRestore();
	});

	it("opens an agent tab's public page in the system browser", async () => {
		const url = "https://github.com/justvibedev/tallpine-dev";
		hookState.navState = { ...hookState.navState, url };
		const agentTab = { id: "a1", url, title: "", active: true, remote: { tabId: "R1" } };
		hookState.tabs = [agentTab];
		const openExternal = vi.spyOn(window.tallpine!.app, "openExternal").mockResolvedValue(undefined);
		renderPanel({ session: { ...session, hostId: "h_workbox" } });

		const open = screen.getByRole("button", { name: /open in system browser/i });
		expect(open).not.toHaveAttribute("aria-disabled");
		fireEvent.click(open);
		await waitFor(() => expect(openExternal).toHaveBeenCalledWith(url));
		openExternal.mockRestore();
	});

	it("keeps secondary browser controls compact until device presets are requested", async () => {
		renderPanel();

		await openBrowserControls();
		expect(screen.getByRole("menuitem", { name: "Device preset" })).toBeInTheDocument();
		expect(screen.getByRole("menuitem", { name: /Profile/ })).toBeInTheDocument();
		expect(screen.getByRole("menuitem", { name: "Open DevTools" })).toBeInTheDocument();
		expect(screen.queryByRole("menuitem", { name: /iPhone SE/ })).not.toBeInTheDocument();

		await userEvent.click(screen.getByRole("menuitem", { name: "Device preset" }));
		expect(screen.getByRole("menuitem", { name: /iPhone SE/ })).toBeInTheDocument();
		expect(screen.getByRole("menuitem", { name: /iPhone SE/ }).parentElement).toHaveClass("board-scrollbar");
	});

	it("captures the active page from the controls menu and confirms the clipboard copy", async () => {
		hookState.navState = { ...hookState.navState, url: "https://example.test/" };
		renderPanel();

		await openBrowserControls();
		await userEvent.click(screen.getByRole("menuitem", { name: "Take a screenshot" }));

		expect(window.tallpine!.browser.captureScreenshot).toHaveBeenCalledWith("42:sess-1");
		await waitFor(() =>
			expect(useUiStore.getState().globalToast?.title).toBe("Screenshot copied to clipboard"),
		);
		expect(useUiStore.getState().globalToast?.placement).toBe("top-center");
	});

	it("disables screenshots until the active tab has a page", async () => {
		renderPanel();

		await openBrowserControls();
		expect(screen.getByRole("menuitem", { name: "Take a screenshot" })).toHaveAttribute("data-disabled");
	});

	it("opens download history from the browser controls menu", async () => {
		renderPanel();
		await openBrowserControls();
		await userEvent.click(screen.getByRole("menuitem", { name: "Downloads" }));
		expect(useUiStore.getState().settingsModal).toEqual({ scope: "global", section: "browserProfiles" });
	});

	it("restores the shared Downloads tooltip when its menu returns focus", async () => {
		window.tallpine!.browser.downloads.list = vi.fn(async () => ({
			downloads: [{
				id: "download-1",
				fileName: "report.pdf",
				receivedBytes: 100,
				totalBytes: 100,
				status: "completed" as const,
				startedAt: 1,
				updatedAt: 2,
			}],
		}));
		renderPanel();

		// Previously retained downloads hydrate the browser controls without
		// opening the menu; only genuinely new downloads should interrupt.
		await waitFor(() => expect(window.tallpine!.browser.downloads.list).toHaveBeenCalled());
		const trigger = await screen.findByRole("button", { name: "Downloads" });
		expect(screen.queryByText("report.pdf")).not.toBeInTheDocument();
		await userEvent.click(trigger);
		expect(screen.getByText("report.pdf")).toBeInTheDocument();
		await userEvent.keyboard("{Escape}");
		await waitFor(() => expect(screen.queryByText("report.pdf")).not.toBeInTheDocument());
		expect(trigger).toHaveFocus();
		expect(document.querySelector('[data-slot="tooltip-content"]')).toHaveTextContent("Downloads");
	});

	it("opens the downloads menu for a new download while the browser panel is active", async () => {
		renderPanel();
		await waitFor(() => expect(window.tallpine!.browser.downloads.list).toHaveBeenCalled());

		act(() => {
			downloadListeners.forEach((listener) =>
				listener({
					downloads: [{
						id: "download-1",
						fileName: "report.pdf",
						receivedBytes: 25,
						totalBytes: 100,
						status: "progressing",
						startedAt: 1,
						updatedAt: 2,
					}],
				}),
			);
		});

		expect(await screen.findByText("report.pdf")).toBeInTheDocument();
	});

	it("does not open the downloads menu for new downloads while the browser panel is hidden", async () => {
		renderPanel({ active: false });
		await waitFor(() => expect(window.tallpine!.browser.downloads.list).toHaveBeenCalled());

		act(() => {
			downloadListeners.forEach((listener) =>
				listener({
					downloads: [{
						id: "download-1",
						fileName: "report.pdf",
						receivedBytes: 25,
						totalBytes: 100,
						status: "progressing",
						startedAt: 1,
						updatedAt: 2,
					}],
				}),
			);
		});

		await waitFor(() => expect(screen.getByRole("button", { name: "Downloads" })).toBeInTheDocument());
		expect(screen.queryByText("report.pdf")).not.toBeInTheDocument();
	});

	it("keeps browser profiles inside the Tallpine controls menu", async () => {
		window.tallpine!.browserProfiles.list = vi.fn(async () => ({
			profiles: [
				{
					id: "11111111-1111-4111-8111-111111111111",
					name: "Work",
					createdAt: "2026-01-01T00:00:00.000Z",
					updatedAt: "2026-01-01T00:00:00.000Z",
				},
			],
			defaultProfileId: null,
		}));
		renderPanel();

		expect(screen.queryByRole("button", { name: /Profile:/ })).not.toBeInTheDocument();
		await openBrowserControls();
	await userEvent.click(screen.getByRole("menuitem", { name: /Profile/ }));

		expect(await screen.findByRole("menuitem", { name: "Temporary" })).toBeInTheDocument();
		expect(screen.getByRole("menuitem", { name: "Work" })).toBeInTheDocument();
		expect(screen.getByRole("menuitem", { name: "Manage profiles" })).toBeInTheDocument();
		await userEvent.click(screen.getByRole("menuitem", { name: "Work" }));
		expect(window.tallpine!.browser.selectProfile).toHaveBeenCalledWith(
			expect.objectContaining({
				viewId: "42:sess-1",
				profileId: "11111111-1111-4111-8111-111111111111",
			}),
		);
	});

	it("constrains the device frame to a named preset's width, and clears it back to fit", async () => {
		renderPanel();
		const frame = screen.getByTestId("browser-device-frame").parentElement as HTMLElement;
		expect(frame.style.width).toBe("");

		await openDevicePresets();
		await userEvent.click(screen.getByRole("menuitem", { name: /iPhone SE/ }));
		expect(frame.style.width).toBe("375px");

		await openDevicePresets();
		await userEvent.click(screen.getByRole("menuitem", { name: /iPad Mini/ }));
		expect(frame.style.width).toBe("768px");

		await openDevicePresets();
		await userEvent.click(screen.getByRole("menuitem", { name: "Fit panel" }));
		expect(frame.style.width).toBe("");
	});

	it("applies a custom device-frame width typed into the dropdown, clamped to a sane range", async () => {
		renderPanel();
		const frame = screen.getByTestId("browser-device-frame").parentElement as HTMLElement;

		await openDevicePresets();
		const customWidthInput = screen.getByLabelText("Custom width") as HTMLInputElement;
		await userEvent.clear(customWidthInput);
		await userEvent.type(customWidthInput, "600");
		expect(frame.style.width).toBe("600px");

		await userEvent.clear(customWidthInput);
		await userEvent.type(customWidthInput, "10");
		expect(frame.style.width).toBe("240px");
	});

	it("reaches the custom width input from the keyboard", async () => {
		renderPanel();
		await openDevicePresets();
		const item = screen.getByRole("menuitem", { name: /Custom width/ });
		item.focus();
		await userEvent.keyboard("{Enter}");
		const input = screen.getByLabelText("Custom width");
		expect(input).toHaveFocus();
		expect(screen.getByRole("menu")).toBeInTheDocument();
	});

	// Regression: the reviewer flagged that the original 6-device list should
	// match Chrome DevTools' own "Standard" device list rather than a
	// hand-picked subset.
	it("offers Chrome DevTools' own standard device list", async () => {
		renderPanel();
		await openDevicePresets();

		for (const name of [
			"iPhone SE",
			"iPhone XR",
			"iPhone 12 Pro",
			"iPhone 14 Pro Max",
			"iPhone 15 Pro Max",
			"iPhone 16 Pro Max",
			"Pixel 7",
			"Pixel 8",
			"Pixel 9",
			"Pixel 10",
			"Samsung Galaxy S8+",
			"Samsung Galaxy S20 Ultra",
			"Samsung Galaxy A51/71",
			"iPad Mini",
			"iPad Air",
			"iPad Pro",
			"Surface Pro 7",
			"Surface Duo",
			"Galaxy Z Fold 5",
			"Asus Zenbook Fold",
			"Nest Hub Max",
		]) {
			expect(screen.getByRole("menuitem", { name: new RegExp(name.replace(/[+.]/g, "\\$&")) })).toBeInTheDocument();
		}
		// "Nest Hub" alone is a prefix of "Nest Hub Max" — assert it separately
		// with a negative lookahead so the two rows aren't ambiguous.
		expect(screen.getByRole("menuitem", { name: /Nest Hub(?! Max)/ })).toBeInTheDocument();
	});

	it("marks the device-preset dropdown as a browser overlay so it paints above the live page", async () => {
		renderPanel();

		await openDevicePresets();

		const menu = screen.getByRole("menu");
		expect(menu.getAttribute("data-browser-native-overlay")).toBe("true");
	});

	it("restores the shared tooltip when the dropdown returns focus to its trigger", async () => {
		renderPanel();
		const trigger = screen.getByRole("button", { name: "Browser controls" });

		await userEvent.click(trigger);
		expect(screen.getByRole("menuitem", { name: "Device preset" })).toBeInTheDocument();
		fireEvent.pointerDown(document.body);
		await waitFor(() => expect(screen.queryByRole("menuitem", { name: "Device preset" })).not.toBeInTheDocument());
		fireEvent.focus(trigger);

		expect(document.querySelector('[data-slot="tooltip-content"]')).toHaveTextContent("Browser controls");
	});

	it("uses the shared Tallpine tooltip for browser controls", async () => {
		renderPanel();
		const trigger = screen.getByRole("button", { name: "Browser controls" });

		expect(trigger).toHaveAttribute("data-slot", "tooltip-trigger");
		fireEvent.focus(trigger);
		await waitFor(() => expect(document.querySelector('[data-slot="tooltip-content"]')).not.toBeNull());
		expect(document.querySelector('[data-slot="tooltip-content"]')).toHaveAttribute("data-browser-native-overlay", "true");
		expect(document.querySelector('[data-slot="tooltip-content"]')).toHaveAttribute("data-side", "bottom");
	});

	it("keeps the URL input editable while the browser is maximized", async () => {
		hookState.navState = { ...hookState.navState, url: "http://localhost:5173/" };
		renderPanel({ poppedOut: true });
		const input = screen.getByRole("textbox", { name: /browser url/i });

		await userEvent.clear(input);
		await userEvent.type(input, "http://localhost:4173/");

		expect(input).toHaveValue("http://localhost:4173/");
	});

	it("threads the session preview URL into the browser view (which drives navigation)", () => {
		renderPanel({ session: { ...session, previewUrl: "file:///tmp/preview/index.html" } });

		expect(hookState.previewUrl).toBe("file:///tmp/preview/index.html");
	});
});
