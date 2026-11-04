import {
	state,
	setNavigatorPlatform,
} from "./XtermTerminal.test.setup";

import {
	act,
	fireEvent,
	render,
	screen,
	waitFor,
} from "@testing-library/react";
import {
	beforeEach,
	describe,
	expect,
	it,
	vi,
} from "vitest";
import { type AttachableTerminal } from "../hooks/useTerminalSession";
import { useUiStore } from "../stores/ui-store";
import { safeTerminalFind } from "./TerminalSearch";
import { XtermTerminal } from "./XtermTerminal";



describe("XtermTerminal", () => {
	beforeEach(() => {
		state.fit.mockReset();
		state.lifecycle.length = 0;
		state.lastTerminal = null;
		state.linkHandler = null;
		state.queueViewportSyncOnOpen = false;
		state.sessionLinkProvider = null;
		state.searchAddon = null;
		state.mouseMoveListener.mockClear();
		setNavigatorPlatform("Linux x86_64");
		window.tallpine!.clipboard.writeText = vi.fn().mockResolvedValue(undefined);
		window.tallpine!.clipboard.readText = vi.fn().mockResolvedValue("");
		window.tallpine!.terminal.setFocused = vi.fn();
		window.tallpine!.terminal.onFontSizeShortcut = () => () => undefined;
	});

	function renderScrollableMacTerminal() {
		setNavigatorPlatform("MacIntel");
		vi.useFakeTimers();
		const { container } = render(<XtermTerminal theme="dark" />);
		const scrollbar = container.querySelector<HTMLElement>(".terminal-scrollbar")!;
		Object.defineProperty(scrollbar, "clientHeight", { configurable: true, value: 400 });
		state.lastTerminal!.buffer.active.baseY = 100;
		// Settle mount-time frames and timers so the test starts idle.
		act(() => vi.runOnlyPendingTimers());
		// The scrollbar follows scrolling once per frame, not once per line feed.
		const scroll = () => act(() => state.lastTerminal!.scrollListeners.forEach((listener) => listener()));
		const nextFrame = () => act(() => vi.advanceTimersToNextFrame());
		return { scrollbar, scroll, nextFrame };
	}

	it("reports visible text only after xterm renders nonblank cells", () => {
		const onVisibleContent = vi.fn();
		render(<XtermTerminal onVisibleContent={onVisibleContent} theme="dark" />);
		const terminal = state.lastTerminal!;
		terminal.bufferLines = [{ translateToString: () => "   ", isWrapped: false }];
		act(() => terminal.renderListeners.forEach((listener) => listener()));
		expect(onVisibleContent).not.toHaveBeenCalled();
		terminal.bufferLines = [{ translateToString: () => "Codex", isWrapped: false }];
		act(() => terminal.renderListeners.forEach((listener) => listener()));
		expect(onVisibleContent).toHaveBeenCalledTimes(1);
		act(() => terminal.renderListeners.forEach((listener) => listener()));
		expect(onVisibleContent).toHaveBeenCalledTimes(1);
	});
	// FitAddon proposes its 2-column minimum for a host with no layout box (a
	// tab parked before it was laid out). Adopting it would start the shell at
	// 2 columns.
	it("never adopts a fitted grid from a host with no layout box", async () => {
		state.fit.mockReturnValue({ cols: 2, rows: 5 });
		let terminal!: AttachableTerminal;
		render(<XtermTerminal theme="dark" onReady={(ready) => { terminal = ready; }} />);

		act(() => { window.dispatchEvent(new Event("resize")); });
		await act(async () => { await new Promise((resolve) => setTimeout(resolve, 60)); });

		expect(state.lastTerminal!.resize).not.toHaveBeenCalledWith(2, 5);
		expect(terminal.hasMeasuredGrid).toBe(false);
	});

	it("publishes its first measured grid even when it equals xterm's default", async () => {
		const width = vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockReturnValue(800);
		const height = vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockReturnValue(600);
		try {
			let terminal!: AttachableTerminal;
			const onVisibleSize = vi.fn();
			render(<XtermTerminal theme="dark" onVisibleSize={onVisibleSize} onReady={(ready) => { terminal = ready; }} />);
			const { cols, rows } = state.lastTerminal!;
			state.fit.mockReturnValue({ cols, rows });

			await waitFor(() => expect(onVisibleSize).toHaveBeenCalledWith(cols, rows));
			expect(terminal.hasMeasuredGrid).toBe(true);
			expect(state.lastTerminal!.resize).not.toHaveBeenCalled();
		} finally {
			width.mockRestore();
			height.mockRestore();
		}
	});

	it("coalesces live terminal resize observer deliveries into the next frame", () => {
		const callbacks: ResizeObserverCallback[] = [];
		const frames: FrameRequestCallback[] = [];
		const originalResizeObserver = window.ResizeObserver;
		const requestAnimationFrameSpy = vi.spyOn(window, "requestAnimationFrame").mockImplementation((callback) => {
			frames.push(callback);
			return frames.length;
		});
		class CapturingResizeObserver implements ResizeObserver {
			constructor(callback: ResizeObserverCallback) {
				callbacks.push(callback);
			}
			disconnect() {}
			observe() {}
			unobserve() {}
		}
		Object.defineProperty(window, "ResizeObserver", {
			configurable: true,
			writable: true,
			value: CapturingResizeObserver,
		});
		const width = vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockReturnValue(800);
		const height = vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockReturnValue(600);
		try {
			render(
				<div data-terminal-live-resize="true">
					<XtermTerminal theme="dark" />
				</div>,
			);
			state.fit.mockClear();
			frames.length = 0;

			act(() => callbacks.at(-1)?.([], {} as ResizeObserver));
			act(() => callbacks.at(-1)?.([], {} as ResizeObserver));

			expect(state.fit).not.toHaveBeenCalled();
			expect(frames).toHaveLength(1);
			act(() => frames.shift()?.(performance.now()));
			expect(state.fit).toHaveBeenCalledTimes(1);
		} finally {
			width.mockRestore();
			height.mockRestore();
			requestAnimationFrameSpy.mockRestore();
			Object.defineProperty(window, "ResizeObserver", {
				configurable: true,
				writable: true,
				value: originalResizeObserver,
			});
		}
	});

	it.each([
		["Linux x86_64", 100],
		["MacIntel", 99],
	])("resizes before paint from the observer box on %s without reading layout", (platform, cols) => {
		setNavigatorPlatform(platform);
		const callbacks: ResizeObserverCallback[] = [];
		const original = window.ResizeObserver;
		class CapturingResizeObserver implements ResizeObserver {
			constructor(callback: ResizeObserverCallback) { callbacks.push(callback); }
			disconnect() {}
			observe() {}
			unobserve() {}
		}
		window.ResizeObserver = CapturingResizeObserver;
		try {
			const { container, rerender } = render(<XtermTerminal theme="dark" />);
			const host = container.querySelector(".terminal-xterm-host")!;
			const terminal = state.lastTerminal!;
			terminal.dimensions = { css: { cell: { width: 8, height: 16 } } };
			const entry = (width: number, height = 640) => [{ target: host, contentRect: { width, height } }] as ResizeObserverEntry[];
			const styleSpy = vi.spyOn(window, "getComputedStyle");
			const rectSpy = vi.spyOn(host, "getBoundingClientRect");
			state.fit.mockClear();
			try {
				act(() => callbacks.at(-1)?.(entry(800.9), {} as ResizeObserver));
				expect(terminal.resize).toHaveBeenLastCalledWith(cols, 40);
				act(() => callbacks.at(-1)?.(entry(801), {} as ResizeObserver));
				act(() => callbacks.at(-1)?.(entry(0, 0), {} as ResizeObserver));
				expect(terminal.resize).toHaveBeenCalledTimes(1);
				expect(state.fit).not.toHaveBeenCalled();
				expect(styleSpy).not.toHaveBeenCalled();
				expect(rectSpy).not.toHaveBeenCalled();
			} finally { styleSpy.mockRestore(); rectSpy.mockRestore(); }
			rerender(<XtermTerminal theme="dark" isVisible={false} />);
			act(() => callbacks.at(-1)?.(entry(1000), {} as ResizeObserver));
			expect(terminal.resize).toHaveBeenCalledTimes(1);
		} finally { window.ResizeObserver = original; }
	});

	it("finishes retained activation when xterm emits no render event", async () => {
		vi.useFakeTimers();
		vi.stubGlobal("requestAnimationFrame", (callback: FrameRequestCallback) =>
			window.setTimeout(() => callback(performance.now()), 0),
		);
		vi.stubGlobal("cancelAnimationFrame", (id: number) => window.clearTimeout(id));
		try {
			let terminal: AttachableTerminal | undefined;
			render(<XtermTerminal theme="dark" onReady={(ready) => { terminal = ready; }} />);
			state.lastTerminal!.buffer.active.baseY = 1;
			const preparation = terminal!.prepareForActivation();
			await act(async () => {
				vi.advanceTimersByTime(250);
				vi.runAllTimers();
				await preparation;
			});
			expect(state.lastTerminal!.scrollToBottom).toHaveBeenCalled();
			expect(state.lastTerminal!.refresh).not.toHaveBeenCalled();
		} finally {
			vi.useRealTimers();
			vi.unstubAllGlobals();
		}
	});

	it.each([true, false])("lets xterm drain viewport initialization before disposal (DEV=%s)", (development) => {
		vi.stubEnv("DEV", development);
		vi.useFakeTimers();
		state.queueViewportSyncOnOpen = true;
		try {
			const view = render(<XtermTerminal theme="dark" />);
			const terminal = state.lastTerminal!;
			view.unmount();

			expect(terminal.dispose).not.toHaveBeenCalled();
			expect(state.lifecycle).toEqual([]);
			act(() => vi.runOnlyPendingTimers());
			expect(state.lifecycle).toEqual(["viewport-sync", "dispose"]);
			expect(terminal.dispose).toHaveBeenCalledOnce();
		} finally {
			vi.useRealTimers();
			vi.unstubAllEnvs();
		}
	});

	it("preserves the agent TUI palette without contrast remapping", () => {
		render(<XtermTerminal theme="dark" />);

		expect(state.lastTerminal!.options.drawBoldTextInBrightColors).toBe(true);
		expect(state.lastTerminal!.options.minimumContrastRatio).toBe(1);
	});

	it("focuses the terminal when human input is requested", async () => {
		const { rerender } = render(<XtermTerminal theme="dark" />);

		rerender(<XtermTerminal focusRequested theme="dark" />);

		await waitFor(() => expect(state.lastTerminal!.focus).toHaveBeenCalled());
	});

	it("defers an already-permitted focus outside the discrete render update", () => {
		vi.useFakeTimers();
		try {
			const { rerender } = render(<XtermTerminal theme="dark" />);
			state.lastTerminal!.focus.mockClear();

			rerender(<XtermTerminal focusRequested theme="dark" />);
			expect(state.lastTerminal!.focus).not.toHaveBeenCalled();

			act(() => vi.runOnlyPendingTimers());
			expect(state.lastTerminal!.focus).toHaveBeenCalledOnce();
		} finally {
			vi.useRealTimers();
		}
	});

	it("does not steal focus from an open dialog or another control", () => {
		const { rerender } = render(<XtermTerminal theme="dark" />);
		const dialog = document.createElement("div");
		dialog.setAttribute("role", "dialog");
		dialog.dataset.state = "open";
		const dialogInput = document.createElement("input");
		dialog.appendChild(dialogInput);
		document.body.appendChild(dialog);
		dialogInput.focus();
		state.lastTerminal!.focus.mockClear();

		rerender(<XtermTerminal focusRequested theme="dark" />);

		expect(state.lastTerminal!.focus).not.toHaveBeenCalled();

		dialog.remove();
		const control = document.createElement("button");
		document.body.appendChild(control);
		control.focus();
		rerender(<XtermTerminal theme="dark" />);
		state.lastTerminal!.focus.mockClear();
		rerender(<XtermTerminal focusRequested theme="dark" />);

		expect(state.lastTerminal!.focus).not.toHaveBeenCalled();
		control.remove();
	});

	it("allows the session navigation button to hand focus to the destination TUI", async () => {
		const { rerender } = render(<XtermTerminal theme="dark" />);
		const sessionButton = document.createElement("button");
		sessionButton.setAttribute("aria-current", "page");
		document.body.appendChild(sessionButton);
		sessionButton.focus();
		state.lastTerminal!.focus.mockClear();

		rerender(<XtermTerminal focusRequested theme="dark" />);

		await waitFor(() => expect(state.lastTerminal!.focus).toHaveBeenCalled());
		sessionButton.remove();
	});

	it("allows a PTY-launching button to hand focus to the opened terminal", async () => {
		const { rerender } = render(<XtermTerminal theme="dark" />);
		const loginButton = document.createElement("button");
		loginButton.dataset.terminalFocusHandoff = "true";
		document.body.appendChild(loginButton);
		loginButton.focus();
		state.lastTerminal!.focus.mockClear();

		rerender(<XtermTerminal focusRequested theme="dark" />);

		await waitFor(() => expect(state.lastTerminal!.focus).toHaveBeenCalled());
		loginButton.remove();
	});

	it("allows an in-pane terminal tab to hand focus to the destination TUI", async () => {
		const { rerender } = render(<XtermTerminal theme="dark" />);
		const topbar = document.createElement("div");
		topbar.dataset.testid = "session-workspace-topbar";
		const tab = document.createElement("button");
		tab.setAttribute("aria-current", "true");
		tab.setAttribute("role", "tab");
		topbar.appendChild(tab);
		document.body.appendChild(topbar);
		tab.focus();
		state.lastTerminal!.focus.mockClear();

		rerender(<XtermTerminal focusRequested theme="dark" />);

		await waitFor(() => expect(state.lastTerminal!.focus).toHaveBeenCalled());
		topbar.remove();
	});

	it("retries terminal focus after a closing dialog releases focus", async () => {
		const frames: FrameRequestCallback[] = [];
		const requestAnimationFrameSpy = vi.spyOn(window, "requestAnimationFrame").mockImplementation((callback) => {
			frames.push(callback);
			return frames.length;
		});
		const { rerender } = render(<XtermTerminal theme="dark" />);
		frames.length = 0;
		const dialog = document.createElement("div");
		dialog.setAttribute("role", "dialog");
		dialog.dataset.state = "open";
		const dialogInput = document.createElement("input");
		dialog.appendChild(dialogInput);
		document.body.appendChild(dialog);
		dialogInput.focus();
		state.lastTerminal!.focus.mockClear();

		rerender(<XtermTerminal focusRequested theme="dark" />);
		dialogInput.blur();
		dialog.remove();
		expect(frames).toHaveLength(1);
		act(() => frames.shift()?.(performance.now()));

		expect(state.lastTerminal!.focus).toHaveBeenCalled();
		requestAnimationFrameSpy.mockRestore();
	});

	it("focuses a retained terminal when it becomes visible", async () => {
		const { rerender } = render(<XtermTerminal focusRequested isVisible={false} theme="dark" />);
		state.lastTerminal!.focus.mockClear();

		rerender(<XtermTerminal focusRequested isVisible theme="dark" />);

		await waitFor(() => expect(state.lastTerminal!.focus).toHaveBeenCalled());
	});

	it("restores focus when the cache requests activation focus", async () => {
		let terminal: AttachableTerminal | undefined;
		render(<XtermTerminal theme="dark" onReady={(ready) => { terminal = ready; }} />);
		state.lastTerminal!.focus.mockClear();

		act(() => terminal!.requestActivationFocus());

		await waitFor(() => expect(state.lastTerminal!.focus).toHaveBeenCalled());
	});

	it("respects an explicit focus opt-out when the cache requests activation focus", () => {
		vi.useFakeTimers();
		try {
			let terminal: AttachableTerminal | undefined;
			render(<XtermTerminal focusRequested={false} theme="dark" onReady={(ready) => { terminal = ready; }} />);
			state.lastTerminal!.focus.mockClear();

			act(() => terminal!.requestActivationFocus());
			act(() => vi.runOnlyPendingTimers());

			expect(state.lastTerminal!.focus).not.toHaveBeenCalled();
		} finally {
			vi.useRealTimers();
		}
	});

	it("rechecks the focus opt-out before a queued activation focus runs", () => {
		vi.useFakeTimers();
		try {
			let terminal: AttachableTerminal | undefined;
			const onReady = (ready: AttachableTerminal) => { terminal = ready; };
			const { rerender } = render(<XtermTerminal focusRequested theme="dark" onReady={onReady} />);
			act(() => vi.runOnlyPendingTimers());
			state.lastTerminal!.focus.mockClear();

			act(() => terminal!.requestActivationFocus());
			rerender(<XtermTerminal focusRequested={false} theme="dark" onReady={onReady} />);
			act(() => vi.runOnlyPendingTimers());

			expect(state.lastTerminal!.focus).not.toHaveBeenCalled();
		} finally {
			vi.useRealTimers();
		}
	});

	it("does not steal focus from a dialog when the cache requests activation focus", () => {
		vi.useFakeTimers();
		try {
			let terminal: AttachableTerminal | undefined;
			render(<XtermTerminal theme="dark" onReady={(ready) => { terminal = ready; }} />);
			state.lastTerminal!.focus.mockClear();
			const dialog = document.createElement("div");
			dialog.setAttribute("role", "dialog");
			dialog.dataset.state = "open";
			const dialogInput = document.createElement("input");
			dialog.appendChild(dialogInput);
			document.body.appendChild(dialog);
			dialogInput.focus();

			act(() => terminal!.requestActivationFocus());
			act(() => vi.runAllTimers());

			expect(state.lastTerminal!.focus).not.toHaveBeenCalled();
			dialog.remove();
		} finally {
			vi.useRealTimers();
		}
	});

	it("allows a tab close button marked as a tab action to hand focus to the terminal", async () => {
		const { rerender } = render(<XtermTerminal theme="dark" />);
		const topbar = document.createElement("div");
		topbar.dataset.testid = "session-workspace-topbar";
		const close = document.createElement("button");
		close.setAttribute("data-terminal-tab-action", "true");
		topbar.appendChild(close);
		document.body.appendChild(topbar);
		close.focus();
		state.lastTerminal!.focus.mockClear();

		rerender(<XtermTerminal focusRequested theme="dark" />);

		await waitFor(() => expect(state.lastTerminal!.focus).toHaveBeenCalled());
		topbar.remove();
	});

	it("does not steal focus from other topbar buttons like the session-actions trigger", async () => {
		const frames: FrameRequestCallback[] = [];
		const requestAnimationFrameSpy = vi.spyOn(window, "requestAnimationFrame").mockImplementation((callback) => {
			frames.push(callback);
			return frames.length;
		});
		try {
			const { rerender } = render(<XtermTerminal theme="dark" />);
			const topbar = document.createElement("div");
			topbar.dataset.testid = "session-workspace-topbar";
			// ⋮ trigger and embedded shell chrome are plain buttons in the
			// topbar with no opt-in marker — they must keep focus.
			const actionsTrigger = document.createElement("button");
			actionsTrigger.setAttribute("data-session-actions-trigger", "");
			topbar.appendChild(actionsTrigger);
			document.body.appendChild(topbar);
			actionsTrigger.focus();
			state.lastTerminal!.focus.mockClear();

			rerender(<XtermTerminal focusRequested theme="dark" />);
			act(() => frames.splice(0).forEach((callback) => callback(performance.now())));

			expect(state.lastTerminal!.focus).not.toHaveBeenCalled();
			topbar.remove();
		} finally {
			requestAnimationFrameSpy.mockRestore();
		}
	});

	it("updates the live terminal palette when the named color theme changes", () => {
		const style = document.createElement("style");
		style.textContent = `
			:root {
				--color-bg-terminal-opaque: #101317;
				--color-text-terminal: #d7d7d2;
				--color-working: #60a5fa;
			}
		`;
		document.head.appendChild(style);
		delete document.documentElement.dataset.styleTheme;
		useUiStore.setState({ themeStyle: "orchestrate" });

		try {
			render(<XtermTerminal theme="dark" />);
			expect(state.lastTerminal!.options.theme).toMatchObject({ background: "#101317" });

			act(() => useUiStore.getState().setThemeStyle("github"));

			// Named palettes come from the injected theme catalog stylesheet.
			expect(state.lastTerminal!.options.theme).toMatchObject({
				background: "#0d1117",
				cursor: "#4493f8",
				foreground: "#e6edf3",
				red: "#f85149",
			});
		} finally {
			style.remove();
			delete document.documentElement.dataset.styleTheme;
			act(() => useUiStore.setState({ themeStyle: "orchestrate" }));
		}
	});

	it.each(["dark", "light"] as const)("toggles transparency on a retained %s terminal", (theme) => {
		setNavigatorPlatform("MacIntel");
		useUiStore.setState({ windowTranslucency: true, themeStyle: "github" });
		const view = render(<XtermTerminal theme={theme} />);
		const terminal = state.lastTerminal!;
		const background = theme === "dark" ? "#0d1117" : "#ffffff";
		try {
			expect(terminal.options.allowTransparency).toBe(true);
			expect(terminal.options.theme).toMatchObject({
				background: "#00000000",
				black: background,
				cursorAccent: background,
			});

			act(() => useUiStore.getState().setWindowTranslucency(false));
			expect(terminal.options.theme).toMatchObject({ background });
			act(() => useUiStore.getState().setWindowTranslucency(true));
			expect(terminal.options.theme).toMatchObject({ background: "#00000000" });
			act(() => useUiStore.getState().setThemeStyle("orchestrate"));
			expect(terminal.options.theme).toMatchObject({ background: "#00000000" });
			expect(state.lastTerminal).toBe(terminal);
			expect(terminal.dispose).not.toHaveBeenCalled();
			expect(terminal.clear).not.toHaveBeenCalled();
		} finally {
			view.unmount();
			act(() => useUiStore.setState({ windowTranslucency: false, themeStyle: "orchestrate" }));
		}
	});

	it("keeps terminals opaque outside macOS even with a saved transparency preference", () => {
		useUiStore.setState({ windowTranslucency: true, themeStyle: "github" });
		const view = render(<XtermTerminal theme="dark" />);
		try {
			expect(state.lastTerminal!.options.allowTransparency).toBe(false);
			expect(state.lastTerminal!.options.theme).toMatchObject({ background: "#0d1117" });
		} finally {
			view.unmount();
			act(() => useUiStore.setState({ windowTranslucency: false, themeStyle: "orchestrate" }));
		}
	});

	it("uses the terminal foreground for the light-mode block cursor", () => {
		const style = document.createElement("style");
		style.textContent = `
			:root {
				--color-bg-terminal-opaque: #f5f5f4;
				--color-text-terminal: #24292f;
				--color-working: #2563eb;
			}
		`;
		document.head.appendChild(style);
		delete document.documentElement.dataset.styleTheme;
		useUiStore.setState({ themeStyle: "orchestrate" });

		try {
			render(<XtermTerminal theme="light" />);
			expect(state.lastTerminal!.options.theme).toMatchObject({
				background: "#f5f5f4",
				foreground: "#24292f",
				cursor: "#24292f",
				cursorAccent: "#f5f5f4",
			});
		} finally {
			style.remove();
			delete document.documentElement.dataset.styleTheme;
			act(() => useUiStore.setState({ themeStyle: "orchestrate" }));
		}
	});

	it("does not reserve width for the hidden terminal scrollbar outside macOS", () => {
		const { container } = render(<XtermTerminal theme="dark" />);

		expect(state.lastTerminal!.options.scrollbar).toEqual({ showScrollbar: false, width: 7 });
		expect(container.querySelector(".terminal-scrollbar")).toBeNull();
	});

	it("reserves a slim draggable scrollbar gutter on macOS", () => {
		setNavigatorPlatform("MacIntel");
		const { container } = render(<XtermTerminal theme="dark" />);

		expect(state.lastTerminal!.options.scrollbar).toEqual({ showScrollbar: true, width: 7 });
		expect(container.querySelector(".terminal-xterm-host--mac")).not.toBeNull();
		expect(container.querySelector(".terminal-scrollbar")).not.toBeNull();
	});

	it("fades the macOS scrollbar after scrolling goes idle", () => {
		const { scrollbar, scroll, nextFrame } = renderScrollableMacTerminal();

		scroll();
		nextFrame();
		expect(scrollbar.dataset.active).toBe("true");

		act(() => vi.advanceTimersByTime(699));
		expect(scrollbar.dataset.active).toBe("true");
		act(() => vi.advanceTimersByTime(1));
		expect(scrollbar.dataset.active).toBe("false");

		vi.useRealTimers();
	});

	// Streaming output scrolls continuously; the scrollbar stays up until the
	// last scroll goes idle, and per-scroll work stays cheap (one frame, then
	// one pending timer).
	it("keeps the macOS scrollbar up through continuous scrolling with one pending timer", () => {
		const { scrollbar, scroll, nextFrame } = renderScrollableMacTerminal();

		const idleTimers = vi.getTimerCount();
		for (let i = 0; i < 50; i++) scroll();
		expect(vi.getTimerCount()).toBe(idleTimers + 1);
		nextFrame();
		expect(scrollbar.dataset.active).toBe("true");
		for (let i = 0; i < 50; i++) scroll();
		nextFrame();
		expect(vi.getTimerCount()).toBe(idleTimers + 1);
		act(() => vi.advanceTimersByTime(400));
		scroll();
		nextFrame();
		act(() => vi.advanceTimersByTime(400));
		scroll();
		nextFrame();
		act(() => vi.advanceTimersByTime(699));
		expect(scrollbar.dataset.active).toBe("true");
		act(() => vi.advanceTimersByTime(1));
		expect(scrollbar.dataset.active).toBe("false");

		vi.useRealTimers();
	});

	it("copies selected terminal text on the terminal copy shortcut", () => {
		render(<XtermTerminal theme="dark" />);
		state.lastTerminal!.selection = "copied selection";

		const event = {
			key: "c",
			metaKey: true,
			ctrlKey: false,
			shiftKey: false,
			preventDefault: vi.fn(),
			stopPropagation: vi.fn(),
		} as unknown as KeyboardEvent;
		const allowed = state.lastTerminal!.keyHandler!(event);

		expect(allowed).toBe(false);
		expect(event.preventDefault).toHaveBeenCalled();
		expect(window.tallpine!.clipboard.writeText).toHaveBeenCalledWith("copied selection");
	});

	it.each([
		["Command", "MacIntel", false, true],
		["Ctrl", "Linux x86_64", true, false],
	])("uses %s plus/minus for terminal font size on %s", (_label, platform, ctrlKey, metaKey) => {
		setNavigatorPlatform(platform);
		const onChangeFontSize = vi.fn();
		render(<XtermTerminal onChangeFontSize={onChangeFontSize} theme="dark" />);

		const increase = {
			altKey: false,
			code: "Equal",
			ctrlKey,
			key: "=",
			metaKey,
			preventDefault: vi.fn(),
			shiftKey: false,
			stopPropagation: vi.fn(),
			type: "keydown",
		} as unknown as KeyboardEvent;
		const decrease = {
			...increase,
			code: "Minus",
			key: "-",
			preventDefault: vi.fn(),
			stopPropagation: vi.fn(),
		} as unknown as KeyboardEvent;
		const otherPlatformModifier = { ...increase, ctrlKey: !ctrlKey, metaKey: !metaKey };

		expect(state.lastTerminal!.keyHandler!(increase)).toBe(false);
		expect(state.lastTerminal!.keyHandler!(decrease)).toBe(false);
		expect(state.lastTerminal!.keyHandler!(otherPlatformModifier)).toBe(true);
		expect(onChangeFontSize).toHaveBeenNthCalledWith(1, 1);
		expect(onChangeFontSize).toHaveBeenNthCalledWith(2, -1);
		expect(increase.preventDefault).toHaveBeenCalledOnce();
		expect(decrease.preventDefault).toHaveBeenCalledOnce();
	});

	it("reports terminal focus and applies main-process font-size shortcuts only there", () => {
		let fontSizeShortcut: ((delta: -1 | 1) => void) | undefined;
		window.tallpine!.terminal.onFontSizeShortcut = (listener) => {
			fontSizeShortcut = listener;
			return () => undefined;
		};
		const onChangeFontSize = vi.fn();
		const { container } = render(<XtermTerminal onChangeFontSize={onChangeFontSize} theme="dark" />);
		const textarea = container.querySelector("textarea")!;

		textarea.focus();
		expect(window.tallpine!.terminal.setFocused).toHaveBeenLastCalledWith(true, true);
		fontSizeShortcut?.(1);
		expect(onChangeFontSize).toHaveBeenCalledWith(1);

		const outside = document.createElement("button");
		document.body.appendChild(outside);
		outside.focus();
		expect(window.tallpine!.terminal.setFocused).toHaveBeenLastCalledWith(false, false);
		fontSizeShortcut?.(-1);
		expect(onChangeFontSize).toHaveBeenCalledTimes(1);
		outside.remove();
	});

	it("reports focus for a terminal that does not resize its font, without claiming the zoom chords", () => {
		const { container } = render(<XtermTerminal theme="dark" />);
		container.querySelector("textarea")!.focus();
		expect(window.tallpine!.terminal.setFocused).toHaveBeenLastCalledWith(true, false);
	});

	it("handles native copy events from inside the terminal", () => {
		const { container } = render(<XtermTerminal theme="dark" />);
		state.lastTerminal!.selection = "native copied selection";
		const setData = vi.fn();
		const event = new Event("copy", { bubbles: true, cancelable: true }) as ClipboardEvent;
		Object.defineProperty(event, "clipboardData", {
			value: { setData },
		});

		container.firstElementChild!.dispatchEvent(event);

		expect(event.defaultPrevented).toBe(true);
		expect(setData).toHaveBeenCalledWith("text/plain", "native copied selection");
		expect(window.tallpine!.clipboard.writeText).toHaveBeenCalledWith("native copied selection");
	});

	it("copies from the focused xterm textarea when the window receives the copy shortcut", () => {
		const { container } = render(<XtermTerminal theme="dark" />);
		state.lastTerminal!.selection = "focused copied selection";
		container.querySelector("textarea")!.focus();

		const event = new KeyboardEvent("keydown", {
			bubbles: true,
			cancelable: true,
			key: "c",
			metaKey: true,
		});
		window.dispatchEvent(event);

		expect(event.defaultPrevented).toBe(true);
		expect(window.tallpine!.clipboard.writeText).toHaveBeenCalledWith("focused copied selection");
	});

	it("opens a themed context menu on right-click and disables Copy without a selection", async () => {
		const { container } = render(<XtermTerminal theme="dark" />);
		const host = container.firstElementChild!;

		expect(fireEvent.contextMenu(host, { clientX: 120, clientY: 88 })).toBe(false);

		expect(await screen.findByText("Paste")).toBeInTheDocument();
		expect(screen.getByText("Copy")).toHaveAttribute("data-disabled");
		const trigger = container.querySelector("button[aria-hidden='true']") as HTMLButtonElement;
		expect(trigger.style.left).toBe("120px");
		expect(trigger.style.top).toBe("88px");
	});

	it("opens terminal search from the context menu with readable light-theme query text", async () => {
		const { container } = render(<XtermTerminal theme="light" />);

		fireEvent.contextMenu(container.firstElementChild!);
		fireEvent.click(await screen.findByText("Search terminal"));

		const input = await screen.findByRole("searchbox", { name: "Search terminal" });
		expect(input).toHaveFocus();
		expect(input).toHaveClass("text-foreground");
		expect(input).not.toHaveClass("text-terminal");
	});

	it.each([
		["Command+F", "MacIntel", false, true],
		["Ctrl+F", "Linux x86_64", true, false],
	])("opens search with %s and consumes the terminal shortcut", async (_label, platform, ctrlKey, metaKey) => {
		setNavigatorPlatform(platform);
		render(<XtermTerminal theme="dark" />);
		const event = {
			altKey: false,
			ctrlKey,
			key: "f",
			metaKey,
			preventDefault: vi.fn(),
			shiftKey: false,
			stopPropagation: vi.fn(),
			type: "keydown",
		} as unknown as KeyboardEvent;

		let allowed = true;
		act(() => {
			allowed = state.lastTerminal!.keyHandler!(event);
		});

		expect(allowed).toBe(false);
		expect(event.preventDefault).toHaveBeenCalledOnce();
		expect(event.stopPropagation).toHaveBeenCalledOnce();
		expect(await screen.findByRole("searchbox", { name: "Search terminal" })).toHaveFocus();
	});

	it("searches incrementally, reports matches, and navigates in both directions", async () => {
		setNavigatorPlatform("MacIntel");
		render(<XtermTerminal theme="dark" />);
		const frames = new Map<number, FrameRequestCallback>();
		let nextFrame = 0;
		const requestAnimationFrameSpy = vi.spyOn(window, "requestAnimationFrame").mockImplementation((callback) => {
			const id = ++nextFrame;
			frames.set(id, callback);
			return id;
		});
		const cancelAnimationFrameSpy = vi.spyOn(window, "cancelAnimationFrame").mockImplementation((id) => {
			frames.delete(id);
		});
		const flushSearchFrame = () => {
			const frame = frames.entries().next().value as [number, FrameRequestCallback] | undefined;
			expect(frame).toBeDefined();
			if (!frame) return;
			frames.delete(frame[0]);
			act(() => frame[1](performance.now()));
		};
		try {
		const shortcut = {
			altKey: false,
			ctrlKey: false,
			key: "f",
			metaKey: true,
			preventDefault: vi.fn(),
			shiftKey: false,
			stopPropagation: vi.fn(),
			type: "keydown",
		} as unknown as KeyboardEvent;
		act(() => state.lastTerminal!.keyHandler!(shortcut));
		const input = await screen.findByRole("searchbox", { name: "Search terminal" });
		state.searchAddon!.findNext.mockClear();

		fireEvent.change(input, { target: { value: "needle" } });
		flushSearchFrame();
		expect(state.searchAddon!.findNext).toHaveBeenLastCalledWith(
			"needle",
			expect.objectContaining({ incremental: true, caseSensitive: false, regex: false }),
		);

		fireEvent.click(screen.getByRole("button", { name: "Match case" }));
		fireEvent.click(screen.getByRole("button", { name: "Use regular expression" }));
		flushSearchFrame();
		expect(state.searchAddon!.findNext).toHaveBeenLastCalledWith(
			"needle",
			expect.objectContaining({ incremental: true, caseSensitive: true, regex: true }),
		);

		act(() =>
			state.searchAddon!.resultListeners.forEach((listener) => listener({ resultCount: 3, resultIndex: 1 })),
		);
		expect(screen.getByText("2/3")).toHaveAccessibleName("Match 2 of 3");

		state.searchAddon!.findNext.mockClear();
		fireEvent.keyDown(input, { key: "Enter" });
		expect(state.searchAddon!.findNext).toHaveBeenCalledWith(
			"needle",
			expect.objectContaining({ incremental: false, caseSensitive: true, regex: true }),
		);
		state.lastTerminal!.selection = "needle";
		state.lastTerminal!.selectionListeners.forEach((listener) => listener());
		await new Promise((resolve) => window.setTimeout(resolve, 0));
		expect(window.tallpine!.clipboard.writeText).not.toHaveBeenCalled();
		expect(screen.queryByRole("status")).not.toBeInTheDocument();

		fireEvent.keyDown(input, { key: "Enter", shiftKey: true });
		expect(state.searchAddon!.findPrevious).toHaveBeenCalledWith(
			"needle",
			expect.objectContaining({ incremental: false, caseSensitive: true, regex: true }),
		);

		state.searchAddon!.findNext.mockClear();
		fireEvent.keyDown(input, { key: "g", metaKey: true });
		expect(state.searchAddon!.findNext).toHaveBeenCalledWith("needle", expect.anything());
		state.searchAddon!.findPrevious.mockClear();
		fireEvent.keyDown(input, { key: "g", metaKey: true, shiftKey: true });
		expect(state.searchAddon!.findPrevious).toHaveBeenCalledWith("needle", expect.anything());
		} finally {
			requestAnimationFrameSpy.mockRestore();
			cancelAnimationFrameSpy.mockRestore();
		}
	});

	it("coalesces rapid terminal-search input into the latest per-frame xterm scan", async () => {
		setNavigatorPlatform("MacIntel");
		render(<XtermTerminal theme="dark" />);
		const frames = new Map<number, FrameRequestCallback>();
		let nextFrame = 0;
		const requestAnimationFrameSpy = vi.spyOn(window, "requestAnimationFrame").mockImplementation((callback) => {
			const id = ++nextFrame;
			frames.set(id, callback);
			return id;
		});
		const cancelAnimationFrameSpy = vi.spyOn(window, "cancelAnimationFrame").mockImplementation((id) => {
			frames.delete(id);
		});
		try {
		act(() =>
			state.lastTerminal!.keyHandler!({
				altKey: false,
				ctrlKey: false,
				key: "f",
				metaKey: true,
				preventDefault: vi.fn(),
				shiftKey: false,
				stopPropagation: vi.fn(),
				type: "keydown",
			} as unknown as KeyboardEvent),
		);
		const input = await screen.findByRole("searchbox", { name: "Search terminal" });
		state.searchAddon!.findNext.mockClear();

		fireEvent.change(input, { target: { value: "n" } });
		fireEvent.change(input, { target: { value: "ne" } });
		fireEvent.change(input, { target: { value: "needle" } });

		expect(frames.size).toBe(1);
		const frame = frames.entries().next().value as [number, FrameRequestCallback];
		frames.delete(frame[0]);
		act(() => frame[1](performance.now()));
		expect(state.searchAddon!.findNext).toHaveBeenCalledTimes(1);
		expect(state.searchAddon!.findNext).toHaveBeenCalledWith(
			"needle",
			expect.objectContaining({ incremental: true }),
		);
		} finally {
			requestAnimationFrameSpy.mockRestore();
			cancelAnimationFrameSpy.mockRestore();
		}
	});

	it("marks an invalid regular expression without calling xterm search", async () => {
		setNavigatorPlatform("MacIntel");
		render(<XtermTerminal theme="dark" />);
		act(() =>
			state.lastTerminal!.keyHandler!({
				altKey: false,
				ctrlKey: false,
				key: "f",
				metaKey: true,
				preventDefault: vi.fn(),
				shiftKey: false,
				stopPropagation: vi.fn(),
				type: "keydown",
			} as unknown as KeyboardEvent),
		);
		const input = await screen.findByRole("searchbox", { name: "Search terminal" });
		fireEvent.click(screen.getByRole("button", { name: "Use regular expression" }));
		state.searchAddon!.findNext.mockClear();

		fireEvent.change(input, { target: { value: "[" } });

		expect(input).toHaveAttribute("aria-invalid", "true");
		expect(screen.getByText("—")).toHaveAccessibleName("Invalid regular expression");
		expect(state.searchAddon!.findNext).not.toHaveBeenCalledWith("[", expect.anything());
	});

	it("clears search highlights and returns focus to xterm on Escape", async () => {
		setNavigatorPlatform("MacIntel");
		render(<XtermTerminal theme="dark" />);
		act(() =>
			state.lastTerminal!.keyHandler!({
				altKey: false,
				ctrlKey: false,
				key: "f",
				metaKey: true,
				preventDefault: vi.fn(),
				shiftKey: false,
				stopPropagation: vi.fn(),
				type: "keydown",
			} as unknown as KeyboardEvent),
		);
		const input = await screen.findByRole("searchbox", { name: "Search terminal" });

		fireEvent.keyDown(input, { key: "Escape" });

		expect(screen.queryByRole("searchbox", { name: "Search terminal" })).not.toBeInTheDocument();
		expect(state.searchAddon!.clearDecorations).toHaveBeenCalled();
		await waitFor(() => expect(state.lastTerminal!.focus).toHaveBeenCalled());
	});

	it("contains xterm's known decoration failure without hiding other search failures", () => {
		expect(
			safeTerminalFind(() => {
				throw new Error("This API only accepts positive integers");
			}, "needle"),
		).toBe(false);
		expect(() =>
			safeTerminalFind(() => {
				throw new Error("unexpected search failure");
			}, "needle"),
		).toThrow("unexpected search failure");
	});

	it("toggles terminal fullscreen from the right-click menu", async () => {
		const onToggleFullscreen = vi.fn();
		const { container, rerender } = render(
			<div className="terminal-pane-frame">
				<XtermTerminal isFullscreen={false} onToggleFullscreen={onToggleFullscreen} theme="dark" />
			</div>,
		);

		fireEvent.contextMenu(container.querySelector(".terminal-pane-frame")!.firstElementChild!);
		fireEvent.click(await screen.findByText("Fullscreen terminal"));
		expect(onToggleFullscreen).toHaveBeenCalledOnce();

		const pane = container.querySelector(".terminal-pane-frame") as HTMLElement;
		Object.defineProperty(document, "fullscreenElement", { configurable: true, value: pane });
		rerender(
			<div className="terminal-pane-frame">
				<XtermTerminal isFullscreen onToggleFullscreen={onToggleFullscreen} theme="dark" />
			</div>,
		);
		fireEvent.contextMenu(pane.firstElementChild!);
		const exitFullscreenItem = await screen.findByText("Exit fullscreen");
		expect(pane.contains(exitFullscreenItem.closest<HTMLElement>("[role='menu']"))).toBe(true);
		Object.defineProperty(document, "fullscreenElement", { configurable: true, value: null });
	});


	it.each([true, false])("preserves newer focus after deferred fullscreen completion when visible=%s", async (isVisible) => {
		let complete!: () => void;
		const onToggleFullscreen = vi.fn(() => new Promise<void>((resolve) => { complete = resolve; }));
		const frames: FrameRequestCallback[] = [];
		const frameSpy = vi.spyOn(window, "requestAnimationFrame").mockImplementation((callback) => {
			frames.push(callback);
			return frames.length;
		});
		const { container, rerender } = render(<XtermTerminal onToggleFullscreen={onToggleFullscreen} theme="dark" />);
		const control = document.createElement("button");
		document.body.appendChild(control);
		try {
			fireEvent.contextMenu(container.firstElementChild!);
			fireEvent.click(await screen.findByText("Fullscreen terminal"));
			rerender(<XtermTerminal isVisible={isVisible} onToggleFullscreen={onToggleFullscreen} theme="dark" />);
			control.focus();
			state.lastTerminal!.focus.mockClear();
			frames.length = 0;
			await act(async () => complete());
			expect(control).toHaveFocus();
			act(() => { while (frames.length) frames.shift()!(performance.now()); });
			expect(control).toHaveFocus();
			expect(state.lastTerminal!.focus).not.toHaveBeenCalled();
		} finally {
			control.remove();
			frameSpy.mockRestore();
		}
	});
	it("returns focus to xterm after entering and exiting fullscreen from the menu", async () => {
		const onToggleFullscreen = vi.fn(async () => undefined);
		const frames: FrameRequestCallback[] = [];
		const requestAnimationFrameSpy = vi.spyOn(window, "requestAnimationFrame").mockImplementation((callback) => {
			frames.push(callback);
			return frames.length;
		});
		const { container, rerender } = render(
			<div className="terminal-pane-frame">
				<XtermTerminal onToggleFullscreen={onToggleFullscreen} theme="dark" />
			</div>,
		);
		const trigger = document.createElement("button");
		document.body.appendChild(trigger);
		state.lastTerminal!.focus.mockClear();
		frames.length = 0;

		try {
			fireEvent.contextMenu(container.querySelector(".terminal-pane-frame")!.firstElementChild!);
			fireEvent.click(await screen.findByText("Fullscreen terminal"));
			await act(async () => undefined);

			expect(onToggleFullscreen).toHaveBeenCalledOnce();
			expect(frames).toHaveLength(1);
			act(() => frames.shift()?.(performance.now()));
			expect(frames).toHaveLength(1);
			act(() => frames.shift()?.(performance.now()));
			expect(state.lastTerminal!.focus).toHaveBeenCalledOnce();

			const pane = container.querySelector(".terminal-pane-frame")!;
			Object.defineProperty(document, "fullscreenElement", { configurable: true, value: pane });
			rerender(
				<div className="terminal-pane-frame">
					<XtermTerminal isFullscreen onToggleFullscreen={onToggleFullscreen} theme="dark" />
				</div>,
			);
			frames.length = 0;
			state.lastTerminal!.focus.mockClear();

			fireEvent.contextMenu(pane.querySelector("div")!);
			fireEvent.click(await screen.findByText("Exit fullscreen"));
			await act(async () => undefined);
			expect(onToggleFullscreen).toHaveBeenCalledTimes(2);
			expect(frames).toHaveLength(1);
			act(() => frames.shift()?.(performance.now()));
			act(() => frames.shift()?.(performance.now()));
			expect(state.lastTerminal!.focus).toHaveBeenCalledOnce();
		} finally {
			Object.defineProperty(document, "fullscreenElement", { configurable: true, value: null });
			requestAnimationFrameSpy.mockRestore();
			trigger.remove();
		}
	});

	it("runs context menu copy and select all against the xterm instance", async () => {
		const { container } = render(<XtermTerminal theme="dark" />);
		const host = container.firstElementChild!;
		state.lastTerminal!.selection = "menu copy";

		fireEvent.contextMenu(host);
		fireEvent.click(await screen.findByText("Copy"));
		await waitFor(() => expect(window.tallpine!.clipboard.writeText).toHaveBeenCalledWith("menu copy"));
		expect(state.lastTerminal!.focus).toHaveBeenCalled();

		fireEvent.contextMenu(host);
		fireEvent.click(await screen.findByText("Select All"));
		expect(state.lastTerminal!.selectAll).toHaveBeenCalled();
	});

	it("pastes from the context menu through the terminal paste path", async () => {
		const onInput = vi.fn();
		window.tallpine!.clipboard.readText = vi.fn().mockResolvedValue("menu\npaste");
		const { container } = render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);

		fireEvent.contextMenu(container.firstElementChild!);
		fireEvent.click(await screen.findByText("Paste"));

		await waitFor(() => expect(onInput).toHaveBeenCalledWith("menu\rpaste", "paste"));
		expect(window.tallpine!.clipboard.readText).toHaveBeenCalledTimes(1);
	});

	it("honors bracketed paste mode from the context menu", async () => {
		const onInput = vi.fn();
		window.tallpine!.clipboard.readText = vi.fn().mockResolvedValue("bracketed\npaste");
		const { container } = render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);
		state.lastTerminal!.modes.bracketedPasteMode = true;

		fireEvent.contextMenu(container.firstElementChild!);
		fireEvent.click(await screen.findByText("Paste"));

		await waitFor(() => expect(onInput).toHaveBeenCalledWith("\x1b[200~bracketed\rpaste\x1b[201~", "paste"));
	});

	it("does not copy terminal selections without an explicit copy action", async () => {
		render(<XtermTerminal theme="dark" />);

		state.lastTerminal!.selection = "selected text";
		state.lastTerminal!.selectionListeners.forEach((listener) => listener());
		await new Promise((resolve) => window.setTimeout(resolve, 0));

		expect(window.tallpine!.clipboard.writeText).not.toHaveBeenCalled();
		expect(screen.queryByRole("status")).not.toBeInTheDocument();
	});

	it("copies the selection to the clipboard when the mouse button is released", async () => {
		const { container } = render(<XtermTerminal theme="dark" />);
		const host = container.querySelector(".terminal-xterm-host")!;
		state.lastTerminal!.selection = "released selection";

		fireEvent.pointerDown(host, { button: 0 });
		fireEvent.pointerUp(document, { button: 0 });

		await waitFor(() => expect(window.tallpine!.clipboard.writeText).toHaveBeenCalledWith("released selection"));
		expect(await screen.findByRole("status")).toHaveTextContent("Copied to clipboard");
	});

	it("does not copy on release when the copy-on-select setting is off", async () => {
		const { container } = render(<XtermTerminal theme="dark" />);
		state.lastTerminal!.selection = "keep out of the clipboard";
		act(() => useUiStore.getState().setTerminalCopyOnSelect(false));

		try {
			fireEvent.pointerDown(container.querySelector(".terminal-xterm-host")!, { button: 0 });
			fireEvent.pointerUp(document, { button: 0 });
			await new Promise((resolve) => window.setTimeout(resolve, 0));

			expect(window.tallpine!.clipboard.writeText).not.toHaveBeenCalled();
			expect(screen.queryByRole("status")).not.toBeInTheDocument();
		} finally {
			act(() => useUiStore.getState().setTerminalCopyOnSelect(true));
		}
	});

	it("ignores releases that did not follow a left-button press inside the terminal", async () => {
		const { container } = render(<XtermTerminal theme="dark" />);
		state.lastTerminal!.selection = "should stay out";
		const host = container.querySelector(".terminal-xterm-host")!;

		fireEvent.pointerUp(document, { button: 0 });
		fireEvent.pointerDown(host, { button: 2 });
		fireEvent.pointerUp(document, { button: 2 });
		fireEvent.pointerDown(host, { button: 2 });
		fireEvent.pointerUp(document, { button: 0 });
		await new Promise((resolve) => window.setTimeout(resolve, 0));

		expect(window.tallpine!.clipboard.writeText).not.toHaveBeenCalled();
	});

	it("copies on the left release even when a right-button release happened mid-drag", async () => {
		const { container } = render(<XtermTerminal theme="dark" />);
		const host = container.querySelector(".terminal-xterm-host")!;
		state.lastTerminal!.selection = "kept armed";

		fireEvent.pointerDown(host, { button: 0 });
		fireEvent.pointerUp(document, { button: 2 });
		fireEvent.pointerUp(document, { button: 0 });

		await waitFor(() => expect(window.tallpine!.clipboard.writeText).toHaveBeenCalledWith("kept armed"));
	});

	it.each([
		["pointercancel", () => fireEvent.pointerCancel(document)],
		["window blur", () => window.dispatchEvent(new Event("blur"))],
	])("disarms the pending selection on %s so a later release does not copy", async (_name, cancel) => {
		const { container } = render(<XtermTerminal theme="dark" />);
		state.lastTerminal!.selection = "stale selection";

		fireEvent.pointerDown(container.querySelector(".terminal-xterm-host")!, { button: 0 });
		cancel();
		fireEvent.pointerUp(document, { button: 0 });
		await new Promise((resolve) => window.setTimeout(resolve, 0));

		expect(window.tallpine!.clipboard.writeText).not.toHaveBeenCalled();
		expect(screen.queryByRole("status")).not.toBeInTheDocument();
	});

	it("joins selected TUI rows that fill the whole grid width when copying", () => {
		render(<XtermTerminal theme="dark" />);
		const terminal = state.lastTerminal!;
		terminal.cols = 5;
		terminal.bufferLines = [
			{ isWrapped: false, translateToString: (t) => (t ? "hello" : "hello") },
			{ isWrapped: false, translateToString: (t) => (t ? "wo" : "wo   ") },
			{ isWrapped: false, translateToString: (t) => (t ? "next" : "next") },
		];
		terminal.selectionRange = { start: { x: 0, y: 0 }, end: { x: 3, y: 2 } };
		terminal.selection = "hello\nwo\nnext";

		terminal.keyHandler!({
			key: "c",
			metaKey: true,
			ctrlKey: false,
			shiftKey: false,
			preventDefault: vi.fn(),
			stopPropagation: vi.fn(),
		} as unknown as KeyboardEvent);

		expect(window.tallpine!.clipboard.writeText).toHaveBeenCalledWith("hellowo\nnext");
	});

	it.each([
		["joins a visibly full row despite stale backing padding", "hello   ", "hello\nwo", "hellowo"],
		["keeps a visibly short row separate despite stale backing text", "hi   stale", "hi\nwo", "hi\nwo"],
	])("%s after a terminal resize", (_name, backingText, selection, expected) => {
		render(<XtermTerminal theme="dark" />);
		const terminal = state.lastTerminal!;
		terminal.cols = 5;
		terminal.bufferLines = [
			{
				isWrapped: false,
				translateToString: (trimRight, startColumn = 0, endColumn = backingText.length) => {
					const text = backingText.slice(startColumn, endColumn);
					return trimRight ? text.trimEnd() : text;
				},
			},
			{ isWrapped: false, translateToString: (trimRight) => (trimRight ? "wo" : "wo   ") },
		];
		terminal.selectionRange = { start: { x: 0, y: 0 }, end: { x: 2, y: 1 } };
		terminal.selection = selection;

		terminal.keyHandler!({
			key: "c",
			metaKey: true,
			ctrlKey: false,
			shiftKey: false,
			altKey: false,
			preventDefault: vi.fn(),
			stopPropagation: vi.fn(),
		} as unknown as KeyboardEvent);

		expect(window.tallpine!.clipboard.writeText).toHaveBeenCalledWith(expected);
	});

	it("joins Windows CRLF rows without changing preserved row breaks", () => {
		setNavigatorPlatform("Win32");
		render(<XtermTerminal theme="dark" />);
		const terminal = state.lastTerminal!;
		terminal.cols = 5;
		terminal.bufferLines = [
			{ isWrapped: false, translateToString: (t) => (t ? "hello" : "hello") },
			{ isWrapped: false, translateToString: (t) => (t ? "wo" : "wo   ") },
			{ isWrapped: false, translateToString: (t) => (t ? "next" : "next") },
		];
		terminal.selectionRange = { start: { x: 0, y: 0 }, end: { x: 3, y: 2 } };
		terminal.selection = "hello\r\nwo\r\nnext";

		terminal.keyHandler!({
			key: "c",
			metaKey: false,
			ctrlKey: true,
			shiftKey: false,
			altKey: false,
			preventDefault: vi.fn(),
			stopPropagation: vi.fn(),
		} as unknown as KeyboardEvent);

		expect(window.tallpine!.clipboard.writeText).toHaveBeenCalledWith("hellowo\r\nnext");
	});
});
