import {
	state,
	setNavigatorPlatform,
	asciiCells,
	testBufferLine,
} from "./XtermTerminal.test.setup";

import {
	act,
	fireEvent,
	render,
	screen,
	waitFor,
} from "@testing-library/react";
import { MAX_DROPPED_FILE_BYTES } from "../../shared/dropped-file";
import {
	beforeEach,
	describe,
	expect,
	it,
	vi,
} from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { TerminalMux } from "../lib/terminal-mux";
import { markTerminalHandleFresh } from "../lib/fresh-terminal-handles";
import { useTerminalSession, type AttachableTerminal } from "../hooks/useTerminalSession";
import { useUiStore } from "../stores/ui-store";
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

	function dropTransfer(entry: { isDirectory: boolean }, files: File[]) {
		return { items: [{ webkitGetAsEntry: () => entry }], files, types: ["Files"] };
	}

	it.each([
		["Linux x86_64", "bc\nhi"],
		["Win32", "bc\r\nhi"],
	])("preserves one row per buffer line for Alt-drag column selections on %s", async (platform, selection) => {
		setNavigatorPlatform(platform);
		const { container } = render(<XtermTerminal theme="dark" />);
		const terminal = state.lastTerminal!;
		terminal.cols = 5;
		terminal.bufferLines = [
			{ isWrapped: false, translateToString: (t) => (t ? "hello" : "hello") },
			{ isWrapped: false, translateToString: (t) => (t ? "thing" : "thing") },
		];
		terminal.selectionRange = { start: { x: 1, y: 0 }, end: { x: 3, y: 1 } };
		terminal.selection = selection;

		const host = container.querySelector(".terminal-xterm-host")!;
		fireEvent.pointerDown(host, { altKey: true, button: 0 });
		fireEvent.pointerUp(document, { altKey: true, button: 0 });

		await waitFor(() => expect(window.tallpine!.clipboard.writeText).toHaveBeenCalledWith(selection));
	});

	it("joins full-width rows of wide characters by cell coverage, not string length", () => {
		render(<XtermTerminal theme="dark" />);
		const terminal = state.lastTerminal!;
		// "你好你" occupies all 6 grid cells (3 wide chars) but is only 3 UTF-16
		// code units long — fullness must not compare string length to cols.
		terminal.cols = 6;
		terminal.bufferLines = [
			{ isWrapped: false, translateToString: (t) => (t ? "你好你" : "你好你") },
			{ isWrapped: false, translateToString: (t) => (t ? "ab" : "ab    ") },
		];
		terminal.selectionRange = { start: { x: 0, y: 0 }, end: { x: 0, y: 1 } };
		terminal.selection = "你好你\nab";

		terminal.keyHandler!({
			key: "c",
			metaKey: true,
			ctrlKey: false,
			shiftKey: false,
			preventDefault: vi.fn(),
			stopPropagation: vi.fn(),
		} as unknown as KeyboardEvent);

		expect(window.tallpine!.clipboard.writeText).toHaveBeenCalledWith("你好你ab");
	});

	it("keeps xterm-wrapped rows merged instead of re-splitting them", () => {
		render(<XtermTerminal theme="dark" />);
		const terminal = state.lastTerminal!;
		terminal.cols = 5;
		terminal.bufferLines = [
			{ isWrapped: false, translateToString: () => "hello" },
			{ isWrapped: true, translateToString: () => "world" },
			{ isWrapped: false, translateToString: () => "tail" },
		];
		terminal.selectionRange = { start: { x: 0, y: 0 }, end: { x: 4, y: 2 } };
		terminal.selection = "helloworld\ntail";

		terminal.keyHandler!({
			key: "c",
			metaKey: true,
			ctrlKey: false,
			shiftKey: false,
			preventDefault: vi.fn(),
			stopPropagation: vi.fn(),
		} as unknown as KeyboardEvent);

		expect(window.tallpine!.clipboard.writeText).toHaveBeenCalledWith("helloworldtail");
	});

	it("shows a copied toast after an explicit copy", async () => {
		render(<XtermTerminal theme="dark" />);
		state.lastTerminal!.selection = "toast selection";
		state.lastTerminal!.keyHandler!({
			key: "c",
			metaKey: true,
			ctrlKey: false,
			shiftKey: false,
			preventDefault: vi.fn(),
			stopPropagation: vi.fn(),
		} as unknown as KeyboardEvent);

		expect(await screen.findByRole("status")).toHaveTextContent("Copied to clipboard");
	});

	it("does not show a copied toast when clipboard write fails", async () => {
		window.tallpine!.clipboard.writeText = vi.fn().mockRejectedValue(new Error("clipboard failed"));
		render(<XtermTerminal theme="dark" />);
		state.lastTerminal!.selection = "failed selection";
		state.lastTerminal!.keyHandler!({
			key: "c",
			metaKey: true,
			ctrlKey: false,
			shiftKey: false,
			preventDefault: vi.fn(),
			stopPropagation: vi.fn(),
		} as unknown as KeyboardEvent);
		await Promise.resolve();

		expect(screen.queryByRole("status")).not.toBeInTheDocument();
	});

	it("hides the copied toast after a short delay", async () => {
		vi.useFakeTimers();
		try {
			render(<XtermTerminal theme="dark" />);
			state.lastTerminal!.selection = "timed selection";
			await act(async () => {
				state.lastTerminal!.keyHandler!({
					key: "c",
					metaKey: true,
					ctrlKey: false,
					shiftKey: false,
					preventDefault: vi.fn(),
					stopPropagation: vi.fn(),
				} as unknown as KeyboardEvent);
				await Promise.resolve();
			});

			expect(screen.getByRole("status")).toHaveTextContent("Copied to clipboard");

			await act(async () => {
				await vi.advanceTimersByTimeAsync(1600);
			});
			expect(screen.queryByRole("status")).not.toBeInTheDocument();
		} finally {
			vi.useRealTimers();
		}
	});

	it("leaves plain Ctrl+C as terminal input on non-Windows even when text is selected", () => {
		render(<XtermTerminal theme="dark" />);
		state.lastTerminal!.selection = "selected text";

		const event = {
			key: "c",
			metaKey: false,
			ctrlKey: true,
			shiftKey: false,
			altKey: false,
			preventDefault: vi.fn(),
			stopPropagation: vi.fn(),
		} as unknown as KeyboardEvent;
		const allowed = state.lastTerminal!.keyHandler!(event);

		expect(allowed).toBe(true);
		expect(event.preventDefault).not.toHaveBeenCalled();
		expect(event.stopPropagation).not.toHaveBeenCalled();
		expect(window.tallpine!.clipboard.writeText).not.toHaveBeenCalled();
	});

	it("copies selected text with plain Ctrl+C on Windows", () => {
		setNavigatorPlatform("Win32");
		render(<XtermTerminal theme="dark" />);
		state.lastTerminal!.selection = "windows copy";

		const event = {
			key: "c",
			metaKey: false,
			ctrlKey: true,
			shiftKey: false,
			altKey: false,
			preventDefault: vi.fn(),
			stopPropagation: vi.fn(),
		} as unknown as KeyboardEvent;
		const allowed = state.lastTerminal!.keyHandler!(event);

		expect(allowed).toBe(false);
		expect(event.preventDefault).toHaveBeenCalled();
		expect(event.stopPropagation).toHaveBeenCalled();
		expect(window.tallpine!.clipboard.writeText).toHaveBeenCalledWith("windows copy");
	});

	it("leaves plain Ctrl+C as terminal input on Windows when nothing is selected", () => {
		setNavigatorPlatform("Win32");
		render(<XtermTerminal theme="dark" />);
		state.lastTerminal!.selection = "";

		const event = {
			key: "c",
			metaKey: false,
			ctrlKey: true,
			shiftKey: false,
			altKey: false,
			preventDefault: vi.fn(),
			stopPropagation: vi.fn(),
		} as unknown as KeyboardEvent;
		const allowed = state.lastTerminal!.keyHandler!(event);

		expect(allowed).toBe(true);
		expect(event.preventDefault).not.toHaveBeenCalled();
		expect(event.stopPropagation).not.toHaveBeenCalled();
		expect(window.tallpine!.clipboard.writeText).not.toHaveBeenCalled();
	});

	it.each(["Linux x86_64", "Win32"])(
		"pastes once from the Electron clipboard on Ctrl+Shift+V for %s",
		async (platform) => {
			setNavigatorPlatform(platform);
			const onInput = vi.fn();
			window.tallpine!.clipboard.readText = vi.fn().mockResolvedValue("hello\nworld");
			const { container } = render(
				<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />,
			);

			const event = {
				key: "v",
				metaKey: false,
				ctrlKey: true,
				shiftKey: true,
				altKey: false,
				preventDefault: vi.fn(),
				stopPropagation: vi.fn(),
			} as unknown as KeyboardEvent;
			const allowed = state.lastTerminal!.keyHandler!(event);
			const pasteEvent = new Event("paste", { bubbles: true, cancelable: true }) as ClipboardEvent;
			Object.defineProperty(pasteEvent, "clipboardData", {
				value: { getData: vi.fn().mockReturnValue("native paste") },
			});
			container.firstElementChild!.dispatchEvent(pasteEvent);
			await Promise.resolve();

			expect(allowed).toBe(false);
			expect(event.preventDefault).toHaveBeenCalled();
			expect(event.stopPropagation).toHaveBeenCalled();
			expect(window.tallpine!.clipboard.readText).toHaveBeenCalledTimes(1);
			expect(pasteEvent.defaultPrevented).toBe(true);
			expect(onInput).toHaveBeenCalledTimes(1);
			expect(onInput).toHaveBeenCalledWith("hello\rworld", "paste");
		},
	);

	it("supports plain Ctrl+V paste on Windows", async () => {
		setNavigatorPlatform("Win32");
		const onInput = vi.fn();
		window.tallpine!.clipboard.readText = vi.fn().mockResolvedValue("windows paste");
		render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);

		const event = {
			key: "v",
			metaKey: false,
			ctrlKey: true,
			shiftKey: false,
			altKey: false,
			preventDefault: vi.fn(),
			stopPropagation: vi.fn(),
		} as unknown as KeyboardEvent;
		const allowed = state.lastTerminal!.keyHandler!(event);
		await Promise.resolve();

		expect(allowed).toBe(false);
		expect(event.preventDefault).toHaveBeenCalled();
		expect(event.stopPropagation).toHaveBeenCalled();
		expect(window.tallpine!.clipboard.readText).toHaveBeenCalled();
		expect(onInput).toHaveBeenCalledWith("windows paste", "paste");
	});

	it("suppresses a queued native paste event after a handled paste shortcut", async () => {
		const onInput = vi.fn();
		window.tallpine!.clipboard.readText = vi.fn().mockResolvedValue("shortcut paste");
		const { container } = render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);

		const event = {
			key: "v",
			metaKey: false,
			ctrlKey: true,
			shiftKey: true,
			altKey: false,
			preventDefault: vi.fn(),
			stopPropagation: vi.fn(),
		} as unknown as KeyboardEvent;
		expect(state.lastTerminal!.keyHandler!(event)).toBe(false);
		await new Promise((resolve) => window.setTimeout(resolve, 0));

		const pasteEvent = new Event("paste", { bubbles: true, cancelable: true }) as ClipboardEvent;
		Object.defineProperty(pasteEvent, "clipboardData", {
			value: { getData: vi.fn().mockReturnValue("native paste") },
		});
		container.firstElementChild!.dispatchEvent(pasteEvent);
		await Promise.resolve();

		expect(pasteEvent.defaultPrevented).toBe(true);
		expect(onInput).toHaveBeenCalledTimes(1);
		expect(onInput).toHaveBeenCalledWith("shortcut paste", "paste");
	});

	it("supports classic Windows terminal copy and paste shortcuts", async () => {
		const onInput = vi.fn();
		window.tallpine!.clipboard.readText = vi.fn().mockResolvedValue("insert paste");
		render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);
		state.lastTerminal!.selection = "insert copy";

		const copyEvent = {
			key: "Insert",
			metaKey: false,
			ctrlKey: true,
			shiftKey: false,
			altKey: false,
			preventDefault: vi.fn(),
			stopPropagation: vi.fn(),
		} as unknown as KeyboardEvent;
		expect(state.lastTerminal!.keyHandler!(copyEvent)).toBe(false);
		expect(window.tallpine!.clipboard.writeText).toHaveBeenCalledWith("insert copy");

		const pasteEvent = {
			key: "Insert",
			metaKey: false,
			ctrlKey: false,
			shiftKey: true,
			altKey: false,
			preventDefault: vi.fn(),
			stopPropagation: vi.fn(),
		} as unknown as KeyboardEvent;
		expect(state.lastTerminal!.keyHandler!(pasteEvent)).toBe(false);
		await Promise.resolve();

		expect(window.tallpine!.clipboard.readText).toHaveBeenCalled();
		expect(onInput).toHaveBeenCalledWith("insert paste", "paste");
	});

	it.each([
		["Option/Alt+Left", { key: "ArrowLeft", altKey: true }, "\x1bb"],
		["Option/Alt+Right", { key: "ArrowRight", altKey: true }, "\x1bf"],
		["Option/Alt+Backspace", { key: "Backspace", altKey: true }, "\x1b\x7f"],
		["Option/Alt+Delete", { key: "Delete", altKey: true }, "\x1bd"],
		["Ctrl+Left", { key: "ArrowLeft", ctrlKey: true }, "\x1b[1;5D"],
		["Ctrl+Right", { key: "ArrowRight", ctrlKey: true }, "\x1b[1;5C"],
		["Ctrl+Backspace", { key: "Backspace", ctrlKey: true }, "\x1b\x7f"],
		["Ctrl+Delete", { key: "Delete", ctrlKey: true }, "\x1bd"],
	])("normalizes %s into terminal input", (_name, init, expected) => {
		const onInput = vi.fn();
		render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);

		const event = {
			metaKey: false,
			ctrlKey: false,
			shiftKey: false,
			altKey: false,
			preventDefault: vi.fn(),
			stopPropagation: vi.fn(),
			...init,
		} as unknown as KeyboardEvent;
		const allowed = state.lastTerminal!.keyHandler!(event);

		expect(allowed).toBe(false);
		expect(event.preventDefault).toHaveBeenCalled();
		expect(event.stopPropagation).toHaveBeenCalled();
		expect(onInput).toHaveBeenCalledWith(expected, "shortcut");
	});

	it.each([
		["Cmd+Left", { key: "ArrowLeft", metaKey: true }, "\x01"],
		["Cmd+Right", { key: "ArrowRight", metaKey: true }, "\x05"],
	])("normalizes macOS %s into beginning/end-of-line input", (_name, init, expected) => {
		setNavigatorPlatform("MacIntel");
		const onInput = vi.fn();
		render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);

		const event = {
			type: "keydown",
			ctrlKey: false,
			shiftKey: false,
			altKey: false,
			preventDefault: vi.fn(),
			stopPropagation: vi.fn(),
			...init,
		} as unknown as KeyboardEvent;
		const allowed = state.lastTerminal!.keyHandler!(event);

		expect(allowed).toBe(false);
		expect(event.preventDefault).toHaveBeenCalled();
		expect(event.stopPropagation).toHaveBeenCalled();
		expect(onInput).toHaveBeenCalledWith(expected, "shortcut");
	});

	it("does not re-fire macOS Cmd+Left on the following keyup", () => {
		setNavigatorPlatform("MacIntel");
		const onInput = vi.fn();
		render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);

		const keyDown = {
			type: "keydown",
			key: "ArrowLeft",
			metaKey: true,
			ctrlKey: false,
			shiftKey: false,
			altKey: false,
			preventDefault: vi.fn(),
			stopPropagation: vi.fn(),
		} as unknown as KeyboardEvent;
		expect(state.lastTerminal!.keyHandler!(keyDown)).toBe(false);
		expect(onInput).toHaveBeenCalledTimes(1);

		const keyUp = { ...keyDown, type: "keyup" } as unknown as KeyboardEvent;
		expect(state.lastTerminal!.keyHandler!(keyUp)).toBe(true);
		expect(onInput).toHaveBeenCalledTimes(1);
	});

	it("leaves Windows Home and End to xterm instead of rewriting them to Ctrl+A/E", () => {
		setNavigatorPlatform("Win32");
		const onInput = vi.fn();
		render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);

		for (const key of ["Home", "End"]) {
			const event = {
				type: "keydown",
				key,
				metaKey: false,
				ctrlKey: false,
				shiftKey: false,
				altKey: false,
				preventDefault: vi.fn(),
				stopPropagation: vi.fn(),
			} as unknown as KeyboardEvent;
			expect(state.lastTerminal!.keyHandler!(event)).toBe(true);
			expect(event.preventDefault).not.toHaveBeenCalled();
			expect(onInput).not.toHaveBeenCalled();
		}
	});

	it("keeps Windows Ctrl+Left and Ctrl+Right as word movement", () => {
		setNavigatorPlatform("Win32");
		const onInput = vi.fn();
		render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);

		const left = {
			type: "keydown",
			key: "ArrowLeft",
			ctrlKey: true,
			metaKey: false,
			shiftKey: false,
			altKey: false,
			preventDefault: vi.fn(),
			stopPropagation: vi.fn(),
		} as unknown as KeyboardEvent;
		expect(state.lastTerminal!.keyHandler!(left)).toBe(false);
		expect(onInput).toHaveBeenCalledWith("\x1b[1;5D", "shortcut");

		const right = { ...left, key: "ArrowRight", preventDefault: vi.fn(), stopPropagation: vi.fn() } as unknown as KeyboardEvent;
		expect(state.lastTerminal!.keyHandler!(right)).toBe(false);
		expect(onInput).toHaveBeenCalledWith("\x1b[1;5C", "shortcut");
	});

	it("does not treat the Windows key plus arrows as macOS line-boundary shortcuts", () => {
		setNavigatorPlatform("Win32");
		const onInput = vi.fn();
		render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);

		const event = {
			type: "keydown",
			key: "ArrowLeft",
			metaKey: true,
			ctrlKey: false,
			shiftKey: false,
			altKey: false,
			preventDefault: vi.fn(),
			stopPropagation: vi.fn(),
		} as unknown as KeyboardEvent;
		expect(state.lastTerminal!.keyHandler!(event)).toBe(true);
		expect(event.preventDefault).not.toHaveBeenCalled();
		expect(onInput).not.toHaveBeenCalled();
	});

	it("does not re-fire a shortcut on the keyup that follows its keydown", () => {
		// xterm.js invokes attachCustomKeyEventHandler on keydown, keyup, AND
		// keypress for the same physical key press. Without gating on event.type,
		// releasing Ctrl+Backspace would emit the escape sequence a second time.
		const onInput = vi.fn();
		render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);

		const keyDown = {
			type: "keydown",
			key: "Backspace",
			ctrlKey: true,
			metaKey: false,
			shiftKey: false,
			altKey: false,
			preventDefault: vi.fn(),
			stopPropagation: vi.fn(),
		} as unknown as KeyboardEvent;
		expect(state.lastTerminal!.keyHandler!(keyDown)).toBe(false);
		expect(onInput).toHaveBeenCalledTimes(1);

		const keyUp = { ...keyDown, type: "keyup" } as unknown as KeyboardEvent;
		expect(state.lastTerminal!.keyHandler!(keyUp)).toBe(true);
		expect(onInput).toHaveBeenCalledTimes(1);
	});

	it("does not re-paste on the keyup that follows a Cmd+V keydown", async () => {
		window.tallpine!.clipboard.readText = vi.fn().mockResolvedValue("pasted once");
		const onInput = vi.fn();
		render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);

		const keyDown = {
			type: "keydown",
			key: "v",
			ctrlKey: false,
			metaKey: true,
			shiftKey: false,
			altKey: false,
			preventDefault: vi.fn(),
			stopPropagation: vi.fn(),
		} as unknown as KeyboardEvent;
		expect(state.lastTerminal!.keyHandler!(keyDown)).toBe(false);
		await Promise.resolve();

		const keyUp = { ...keyDown, type: "keyup" } as unknown as KeyboardEvent;
		expect(state.lastTerminal!.keyHandler!(keyUp)).toBe(true);
		await Promise.resolve();

		expect(window.tallpine!.clipboard.readText).toHaveBeenCalledTimes(1);
		expect(onInput).toHaveBeenCalledTimes(1);
	});

	it("sends the meta-return escape sequence for Shift+Enter and consumes the event", () => {
		const onInput = vi.fn();
		render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);

		const event = {
			type: "keydown",
			key: "Enter",
			metaKey: false,
			ctrlKey: false,
			shiftKey: true,
			altKey: false,
			preventDefault: vi.fn(),
			stopPropagation: vi.fn(),
		} as unknown as KeyboardEvent;
		const allowed = state.lastTerminal!.keyHandler!(event);

		expect(allowed).toBe(false);
		expect(event.preventDefault).toHaveBeenCalled();
		expect(event.stopPropagation).toHaveBeenCalled();
		expect(onInput).toHaveBeenCalledTimes(1);
		expect(onInput).toHaveBeenCalledWith("\x1b\r", "keyboard");
	});

	it("does not re-send the meta-return sequence on the keyup that follows Shift+Enter", () => {
		const onInput = vi.fn();
		render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);

		const keyDown = {
			type: "keydown",
			key: "Enter",
			metaKey: false,
			ctrlKey: false,
			shiftKey: true,
			altKey: false,
			preventDefault: vi.fn(),
			stopPropagation: vi.fn(),
		} as unknown as KeyboardEvent;
		expect(state.lastTerminal!.keyHandler!(keyDown)).toBe(false);
		expect(onInput).toHaveBeenCalledTimes(1);

		const keyUp = { ...keyDown, type: "keyup" } as unknown as KeyboardEvent;
		expect(state.lastTerminal!.keyHandler!(keyUp)).toBe(true);
		expect(onInput).toHaveBeenCalledTimes(1);
	});

	it("leaves plain Enter as normal terminal input rather than intercepting it", () => {
		const onInput = vi.fn();
		render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);

		const event = {
			type: "keydown",
			key: "Enter",
			metaKey: false,
			ctrlKey: false,
			shiftKey: false,
			altKey: false,
			preventDefault: vi.fn(),
			stopPropagation: vi.fn(),
		} as unknown as KeyboardEvent;
		const allowed = state.lastTerminal!.keyHandler!(event);

		expect(allowed).toBe(true);
		expect(event.preventDefault).not.toHaveBeenCalled();
		expect(event.stopPropagation).not.toHaveBeenCalled();
		expect(onInput).not.toHaveBeenCalled();
	});

	it("forwards keyboard input from explicit key events", () => {
		const onInput = vi.fn();
		render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);

		state.lastTerminal!.keyListeners.forEach((listener) => listener({ key: "a" }));

		expect(onInput).toHaveBeenCalledWith("a", "keyboard");
	});

	it("does not forward raw xterm data/control bytes as user input", () => {
		const onInput = vi.fn();
		render(
			<XtermTerminal
				theme="dark"
				supportsCursorColorScheme
				onReady={(terminal) => terminal.onUserInput(onInput)}
			/>,
		);

		// One onData hook exists for OSC 10/11/12 color replies (Cursor theme probes).
		expect(state.lastTerminal!.dataListeners.size).toBe(1);
		state.lastTerminal!.dataListeners.forEach((listener) => listener("\x1b[A"));
		expect(onInput).not.toHaveBeenCalled();

		state.lastTerminal!.dataListeners.forEach((listener) =>
			listener("\x1b]11;rgb:f5f5/f5f5/f4f4\x07"),
		);
		expect(onInput).toHaveBeenCalledWith("\x1b]11;rgb:f5f5/f5f5/f4f4\x07", "protocol");
	});

	it("forwards validated OSC replies for generic terminals without forwarding raw control bytes", () => {
		const onInput = vi.fn();
		render(
			<XtermTerminal
				theme="dark"
				paneScrollsByKeyboard
				onReady={(terminal) => terminal.onUserInput(onInput)}
			/>,
		);

		state.lastTerminal!.dataListeners.forEach((listener) => listener("\x1b[?997;1n"));
		expect(onInput).not.toHaveBeenCalled();
		state.lastTerminal!.dataListeners.forEach((listener) =>
			listener("\x1b]4;196;rgb:ffff/0000/8000\x07"),
		);
		expect(onInput).toHaveBeenCalledWith("\x1b]4;196;rgb:ffff/0000/8000\x07", "protocol");
	});

	it("forwards the cursor-position reply generated for a PTY request", () => {
		const onInput = vi.fn();
		let terminal: AttachableTerminal | undefined;
		render(<XtermTerminal theme="dark" onReady={(ready) => {
			terminal = ready;
			return ready.onUserInput(onInput);
		}} />);

		terminal!.write(new TextEncoder().encode("prompt\x1b"));
		terminal!.write(new TextEncoder().encode("[6n"));

		expect(onInput).toHaveBeenCalledWith("\x1b[7;21R", "protocol");
	});

	it("keeps pending cursor credits when more output is queued before parsing", () => {
		const onInput = vi.fn();
		let terminal: AttachableTerminal | undefined;
		render(<XtermTerminal theme="dark" onReady={(ready) => {
			terminal = ready;
			return ready.onUserInput(onInput);
		}} />);
		vi.spyOn(state.lastTerminal!, "write").mockImplementation(() => {});
		terminal!.write(new TextEncoder().encode("\x1b[6n"));
		terminal!.write(new TextEncoder().encode("tail"));
		state.lastTerminal!.dataListeners.forEach((listener) => listener("\x1b[7;21R"));
		expect(onInput).toHaveBeenCalledWith("\x1b[7;21R", "protocol");
	});

	it("answers Cursor color-scheme output queries", () => {
		const onInput = vi.fn();
		let terminal: AttachableTerminal | undefined;
		render(<XtermTerminal theme="light" supportsCursorColorScheme onReady={(ready) => {
			terminal = ready;
			return ready.onUserInput(onInput);
		}} />);
		onInput.mockClear();
		terminal!.write(new TextEncoder().encode("\x1b[?2031h"));
		expect(onInput).toHaveBeenCalledWith(expect.stringContaining("\x1b[?997;2n"), "protocol");
	});

	it("returns a CPR to a fresh shell PTY through coverInitialReplay", async () => {
		markTerminalHandleFresh("fresh-login");
		let output: (data: Uint8Array) => void = () => {};
		let opened: () => void = () => {};
		const sendInput = vi.fn();
		const mux: TerminalMux = {
			open: vi.fn(), close: vi.fn(), resize: vi.fn(), dispose: vi.fn(), sendInput,
			onData: (_id, listener) => { output = listener; return () => {}; },
			onOpened: (_id, listener) => { opened = listener; return () => {}; },
			onExit: () => () => {}, onError: () => () => {}, onConnectionChange: () => () => {},
		};
		function Shell() {
			const session = useTerminalSession(undefined, { daemonReady: true, coverInitialReplay: true, shellTerminalHandleId: "fresh-login", createMux: () => mux });
			return <XtermTerminal theme="dark" onReady={session.attach} />;
		}
		render(<QueryClientProvider client={new QueryClient()}><Shell /></QueryClientProvider>);
		act(() => {
			opened();
			output(new TextEncoder().encode("prompt\x1b"));
			output(new TextEncoder().encode("[6n"));
		});
		await waitFor(() => expect(sendInput).toHaveBeenCalledWith("fresh-login", "\x1b[7;21R"));
	});

	it("stops treating covered output as live after a fresh handle reconnects", async () => {
		markTerminalHandleFresh("fresh-then-reconnected");
		const outputs: Array<(data: Uint8Array) => void> = [];
		const opened: Array<() => void> = [];
		const connectionChanges: Array<(state: "open" | "closed") => void> = [];
		const sendInputs = [vi.fn(), vi.fn()];
		const createMux = vi.fn((): TerminalMux => {
			const index = outputs.length;
			return {
				open: vi.fn(), close: vi.fn(), resize: vi.fn(), dispose: vi.fn(), sendInput: sendInputs[index],
				onData: (_id, listener) => { outputs[index] = listener; return () => {}; },
				onOpened: (_id, listener) => { opened[index] = listener; return () => {}; },
				onExit: () => () => {}, onError: () => () => {},
				onConnectionChange: (listener) => { connectionChanges[index] = listener; return () => {}; },
			};
		});
		function Shell() {
			const session = useTerminalSession(undefined, { daemonReady: true, coverInitialReplay: true, shellTerminalHandleId: "fresh-then-reconnected", createMux });
			return <XtermTerminal theme="dark" onReady={session.attach} />;
		}
		render(<QueryClientProvider client={new QueryClient()}><Shell /></QueryClientProvider>);
		act(() => {
			opened[0]();
			connectionChanges[0]("closed");
		});
		await waitFor(() => expect(createMux).toHaveBeenCalledTimes(2), { timeout: 2_000 });
		const xtermWrite = vi.spyOn(state.lastTerminal!, "write");
		act(() => {
			opened[1]();
			outputs[1](new TextEncoder().encode("history\x1b[6n"));
		});
		await waitFor(() => expect(xtermWrite).toHaveBeenCalled());
		expect(sendInputs[1]).not.toHaveBeenCalled();
	});

	it.each(["remount", "existing"])("discards historical CPRs on %s pane mount", async (scenario) => {
		const outputs: Array<(data: Uint8Array) => void> = [];
		const opened: Array<() => void> = [];
		const sendInputs = [vi.fn(), vi.fn()];
		const createMux = vi.fn((): TerminalMux => {
			const index = outputs.length;
			return {
				open: vi.fn(), close: vi.fn(), resize: vi.fn(), dispose: vi.fn(), sendInput: sendInputs[index],
				onData: (_id, listener) => { outputs[index] = listener; return () => {}; },
				onOpened: (_id, listener) => { opened[index] = listener; return () => {}; },
				onExit: () => () => {}, onError: () => () => {},
				onConnectionChange: () => () => {},
			};
		});
		function Shell() {
			const session = useTerminalSession(undefined, { daemonReady: true, coverInitialReplay: true, shellTerminalHandleId: "mounted-existing", createMux });
			return <XtermTerminal theme="dark" onReady={session.attach} />;
		}
		if (scenario === "remount") {
			markTerminalHandleFresh("mounted-existing");
			const first = render(<QueryClientProvider client={new QueryClient()}><Shell /></QueryClientProvider>);
			act(() => opened[0]());
			first.unmount();
		}
		render(<QueryClientProvider client={new QueryClient()}><Shell /></QueryClientProvider>);
		const index = outputs.length - 1;
		const xtermWrite = vi.spyOn(state.lastTerminal!, "write");
		act(() => {
			opened[index]();
			outputs[index](new TextEncoder().encode("history\x1b[6n"));
		});
		await waitFor(() => expect(xtermWrite).toHaveBeenCalled());
		expect(sendInputs[index]).not.toHaveBeenCalled();
	});

	it("does not forward colour replies xterm makes while parsing a replayed scrollback", () => {
		const onInput = vi.fn();
		let terminal: AttachableTerminal | undefined;
		render(<XtermTerminal theme="dark" onReady={(handle) => { terminal = handle; handle.onUserInput(onInput); }} />);
		// The replay is still being parsed: its write has not called back yet.
		vi.spyOn(state.lastTerminal!, "write").mockImplementation(() => {});
		terminal!.write(new TextEncoder().encode("old output\x1b]11;?\x07"), undefined, "replay");
		state.lastTerminal!.dataListeners.forEach((listener) => listener("\x1b]11;rgb:f5f5/f5f5/f4f4\x07"));
		expect(onInput).not.toHaveBeenCalled();
	});

	it("does not forward an unsolicited cursor-position-shaped input", () => {
		const onInput = vi.fn();
		render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);

		state.lastTerminal!.dataListeners.forEach((listener) => listener("\x1b[7;21R"));

		expect(onInput).not.toHaveBeenCalled();
	});

	it("updates protocol handling when a retained terminal becomes a Cursor terminal", () => {
		const onInput = vi.fn();
		const { rerender } = render(
			<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />,
		);
		onInput.mockClear();
		rerender(
			<XtermTerminal
				theme="dark"
				supportsCursorColorScheme
				onReady={(terminal) => terminal.onUserInput(onInput)}
			/>,
		);
		onInput.mockClear();
		state.lastTerminal!.dataListeners.forEach((listener) =>
			listener("\x1b]11;rgb:f5f5/f5f5/f4f4\x07"),
		);

		expect(onInput).toHaveBeenCalledWith("\x1b]11;rgb:f5f5/f5f5/f4f4\x07", "protocol");
	});

	it("reports live light and dark changes to mode 2031 subscribers", () => {
		const onInput = vi.fn();
		const view = render(
			<XtermTerminal theme="light" onReady={(terminal) => terminal.onUserInput(onInput)} />,
		);
		const setMode = state.lastTerminal!.csiHandlers.find(
			({ id }) => id.prefix === "?" && id.final === "h",
		);
		const resetMode = state.lastTerminal!.csiHandlers.find(
			({ id }) => id.prefix === "?" && id.final === "l",
		);

		expect(setMode?.callback([2031])).toBe(true);
		view.rerender(
			<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />,
		);
		expect(onInput).toHaveBeenLastCalledWith("\x1b[?997;1n", "protocol");

		view.rerender(
			<XtermTerminal theme="light" onReady={(terminal) => terminal.onUserInput(onInput)} />,
		);
		expect(onInput).toHaveBeenLastCalledWith("\x1b[?997;2n", "protocol");

		expect(resetMode?.callback([2031])).toBe(true);
		onInput.mockClear();
		view.rerender(
			<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />,
		);
		expect(onInput).not.toHaveBeenCalled();
	});

	it("reports named palette changes to mode 2031 subscribers", () => {
		const onInput = vi.fn();
		useUiStore.setState({ themeStyle: "orchestrate" });

		try {
			render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);
			const setMode = state.lastTerminal!.csiHandlers.find(
				({ id }) => id.prefix === "?" && id.final === "h",
			);

			expect(setMode?.callback([2031])).toBe(true);
			onInput.mockClear();
			act(() => useUiStore.getState().setThemeStyle("github"));

			expect(onInput).toHaveBeenLastCalledWith("\x1b[?997;1n", "protocol");
		} finally {
			act(() => useUiStore.setState({ themeStyle: "orchestrate" }));
		}
	});

	it("leaves unrelated private modes for xterm to handle", () => {
		render(<XtermTerminal theme="light" />);
		const setMode = state.lastTerminal!.csiHandlers.find(
			({ id }) => id.prefix === "?" && id.final === "h",
		);

		expect(setMode?.callback([25])).toBe(false);
	});

	it("enables color-scheme updates from mixed private mode sets without consuming them", () => {
		const onInput = vi.fn();
		const view = render(
			<XtermTerminal theme="light" onReady={(terminal) => terminal.onUserInput(onInput)} />,
		);
		const setMode = state.lastTerminal!.csiHandlers.find(
			({ id }) => id.prefix === "?" && id.final === "h",
		);

		expect(setMode?.callback([2031, 25])).toBe(false);
		view.rerender(
			<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />,
		);
		expect(onInput).toHaveBeenLastCalledWith("\x1b[?997;1n", "protocol");
	});

	it("disables color-scheme updates from mixed private mode resets without consuming them", () => {
		const onInput = vi.fn();
		const view = render(
			<XtermTerminal theme="light" onReady={(terminal) => terminal.onUserInput(onInput)} />,
		);
		const setMode = state.lastTerminal!.csiHandlers.find(
			({ id }) => id.prefix === "?" && id.final === "h",
		);
		const resetMode = state.lastTerminal!.csiHandlers.find(
			({ id }) => id.prefix === "?" && id.final === "l",
		);

		expect(setMode?.callback([2031])).toBe(true);
		expect(resetMode?.callback([2031, 25])).toBe(false);
		view.rerender(
			<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />,
		);
		expect(onInput).not.toHaveBeenCalled();
	});

	it("answers mixed color-scheme capability queries without consuming them", () => {
		const onInput = vi.fn();
		render(<XtermTerminal theme="light" onReady={(terminal) => terminal.onUserInput(onInput)} />);
		const capabilityQuery = state.lastTerminal!.csiHandlers.find(
			({ id }) => id.prefix === "?" && id.intermediates === "$" && id.final === "p",
		);

		expect(capabilityQuery?.callback([2031, 25])).toBe(false);
		expect(onInput).toHaveBeenCalledWith("\x1b[?2031;2$y", "protocol");
	});

	it("answers mixed color-scheme mode queries without consuming them", () => {
		const onInput = vi.fn();
		render(<XtermTerminal theme="light" onReady={(terminal) => terminal.onUserInput(onInput)} />);
		const modeQuery = state.lastTerminal!.csiHandlers.find(
			({ id }) => id.prefix === "?" && id.final === "n",
		);

		expect(modeQuery?.callback([996, 25])).toBe(false);
		expect(onInput).toHaveBeenCalledWith("\x1b[?997;2n", "protocol");
	});

	it("answers color-scheme capability and current-mode queries", () => {
		const onInput = vi.fn();
		render(<XtermTerminal theme="light" onReady={(terminal) => terminal.onUserInput(onInput)} />);
		const capabilityQuery = state.lastTerminal!.csiHandlers.find(
			({ id }) => id.prefix === "?" && id.intermediates === "$" && id.final === "p",
		);
		const modeQuery = state.lastTerminal!.csiHandlers.find(
			({ id }) => id.prefix === "?" && id.final === "n",
		);

		expect(capabilityQuery?.callback([2031])).toBe(true);
		expect(modeQuery?.callback([996])).toBe(true);
		expect(onInput.mock.calls).toEqual([
			["\x1b[?2031;2$y", "protocol"],
			["\x1b[?997;2n", "protocol"],
		]);
	});

	it("translates wheel motion into SGR wheel reports for zellij scrollback", () => {
		const onInput = vi.fn();
		render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);
		// rowHeight = fontSize(12) * lineHeight(1.35) = 16.2px; -50px => 3 lines up.
		const suppressed = state.lastTerminal!.wheelHandler!({ clientX: 0, clientY: 0, deltaY: -50 } as WheelEvent);

		expect(suppressed).toBe(false);
		expect(onInput).toHaveBeenCalledWith("\x1b[<64;1;1M\x1b[<64;1;1M\x1b[<64;1;1M", "wheel");
	});

	it.each([
		["transcript body", 180, 70, 5, 2],
		["pinned header", 180, 50, 5, 1],
		["outside top-left", 90, 40, 1, 1],
		["outside bottom-right", 2000, 2000, 80, 24],
	])("reports actual one-based wheel coordinates over %s", (_name, clientX, clientY, column, row) => {
		const onInput = vi.fn();
		render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);
		// Offset grid with 20px cells: models a moved and CSS-scaled terminal.
		vi.spyOn(state.lastTerminal!.screenElement, "getBoundingClientRect").mockReturnValue({
			left: 100, top: 50, width: 1600, height: 480,
		} as DOMRect);
		for (const [deltaY, button] of [[-1, 64], [1, 65]]) {
			state.lastTerminal!.wheelHandler!({ clientX, clientY, deltaY, deltaMode: 1 } as WheelEvent);
			expect(onInput).toHaveBeenLastCalledWith(`\x1b[<${button};${column};${row}M`, "wheel");
		}
	});

	it("does not invent mouse coordinates for an unmeasured terminal", () => {
		const onInput = vi.fn();
		render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);
		vi.spyOn(state.lastTerminal!.screenElement, "getBoundingClientRect").mockReturnValue({ width: 0, height: 0 } as DOMRect);
		state.lastTerminal!.wheelHandler!({ clientX: 100, clientY: 100, deltaY: -1, deltaMode: 1 } as WheelEvent);
		expect(onInput).not.toHaveBeenCalled();
	});

	it("handles line- and page-mode wheels (Linux/Windows mice), not just pixel deltas", () => {
		const onInput = vi.fn();
		render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);

		// DOM_DELTA_LINE: deltaY is already in lines, so one notch up => one report.
		expect(state.lastTerminal!.wheelHandler!({ clientX: 0, clientY: 0, deltaY: -1, deltaMode: 1 } as WheelEvent)).toBe(false);
		expect(onInput).toHaveBeenLastCalledWith("\x1b[<64;1;1M", "wheel");

		// DOM_DELTA_PAGE: one page down => rows (24) line reports down.
		onInput.mockClear();
		expect(state.lastTerminal!.wheelHandler!({ clientX: 0, clientY: 0, deltaY: 1, deltaMode: 2 } as WheelEvent)).toBe(false);
		expect(onInput).toHaveBeenLastCalledWith("\x1b[<65;1;1M".repeat(24), "wheel");
	});

	it("scrolls down on positive wheel delta and leaves zoom (ctrl/meta) wheel alone", () => {
		const onInput = vi.fn();
		render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);

		expect(state.lastTerminal!.wheelHandler!({ clientX: 0, clientY: 0, deltaY: 20 } as WheelEvent)).toBe(false);
		expect(onInput).toHaveBeenCalledWith("\x1b[<65;1;1M", "wheel");

		onInput.mockClear();
		expect(state.lastTerminal!.wheelHandler!({ clientX: 0, clientY: 0, deltaY: -50, ctrlKey: true } as WheelEvent)).toBe(false);
		expect(onInput).not.toHaveBeenCalled();
	});

	it("scrolls xterm's own viewport for normal-buffer panes with mouse tracking off (codex, plain shell)", () => {
		const onInput = vi.fn();
		render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);
		state.lastTerminal!.modes.mouseTrackingMode = "none";
		state.lastTerminal!.buffer.active.type = "normal";

		// rowHeight = 16.2px; -50px => 3 lines up. The pane never sees these bytes;
		// we scroll the terminal's retained scrollback locally instead.
		expect(state.lastTerminal!.wheelHandler!({ clientX: 0, clientY: 0, deltaY: -50 } as WheelEvent)).toBe(false);
		expect(state.lastTerminal!.scrollLines).toHaveBeenLastCalledWith(-3);
		expect(onInput).not.toHaveBeenCalled();

		expect(state.lastTerminal!.wheelHandler!({ clientX: 0, clientY: 0, deltaY: 20 } as WheelEvent)).toBe(false);
		expect(state.lastTerminal!.scrollLines).toHaveBeenLastCalledWith(1);
		expect(onInput).not.toHaveBeenCalled();
	});

	it("falls back to PageUp/PageDown for alt-buffer panes with mouse tracking off", () => {
		const onInput = vi.fn();
		render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);
		state.lastTerminal!.modes.mouseTrackingMode = "none";
		// Alt buffer: no local scrollback to move, and no keyboard-scroll hint, so a
		// page key per notch is the best fallback.
		state.lastTerminal!.buffer.active.type = "alternate";

		expect(state.lastTerminal!.wheelHandler!({ clientX: 0, clientY: 0, deltaY: -50 } as WheelEvent)).toBe(false);
		expect(onInput).toHaveBeenLastCalledWith("\x1b[5~", "wheel");
		expect(state.lastTerminal!.scrollLines).not.toHaveBeenCalled();

		expect(state.lastTerminal!.wheelHandler!({ clientX: 0, clientY: 0, deltaY: 20 } as WheelEvent)).toBe(false);
		expect(onInput).toHaveBeenLastCalledWith("\x1b[6~", "wheel");
	});

	it("sends SGR reports on Windows when the pane tracks the mouse (conpty delivers them to the app)", () => {
		setNavigatorPlatform("Win32");
		const onInput = vi.fn();
		render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);
		// A mouse-tracking pane gets SGR reports on every platform; on Windows conpty
		// forwards them straight to the app. Keyboard-scroll panes (opencode) opt out
		// via the paneScrollsByKeyboard hint, tested separately.
		state.lastTerminal!.modes.mouseTrackingMode = "any";

		expect(state.lastTerminal!.wheelHandler!({ clientX: 0, clientY: 0, deltaY: -50 } as WheelEvent)).toBe(false);
		expect(onInput).toHaveBeenLastCalledWith("\x1b[<64;1;1M".repeat(3), "wheel");
	});

	it("sends PageUp/PageDown for keyboard-scroll panes even under a mux (opencode on macOS/Linux)", () => {
		const onInput = vi.fn();
		render(<XtermTerminal theme="dark" paneScrollsByKeyboard onReady={(terminal) => terminal.onUserInput(onInput)} />);
		// Linux (beforeEach) + mouse tracking on: without the paneScrollsByKeyboard
		// hint this would send SGR reports; the hint forces page keys.
		state.lastTerminal!.modes.mouseTrackingMode = "any";

		expect(state.lastTerminal!.wheelHandler!({ clientX: 0, clientY: 0, deltaY: -50 } as WheelEvent)).toBe(false);
		expect(onInput).toHaveBeenLastCalledWith("\x1b[5~", "wheel");
	});

	it("routes web links to the Tallpine browser and does not open the system browser", () => {
		const open = vi.spyOn(window, "open").mockReturnValue(null);
		const onLinkOpen = vi.fn();
		render(<XtermTerminal onLinkOpen={onLinkOpen} theme="dark" />);

		// A left-click on an http(s) link is reported to the parent (which shows it
		// in the Tallpine Browser panel); it must NOT spawn a system-browser window.
		expect(state.linkHandler).toBeTypeOf("function");
		state.linkHandler!({} as MouseEvent, "https://example.com");

		expect(onLinkOpen).toHaveBeenCalledWith("https://example.com");
		expect(open).not.toHaveBeenCalled();
		open.mockRestore();
	});

	it("routes OSC 8 web links to the Tallpine browser without a system-browser window", () => {
		const open = vi.spyOn(window, "open").mockReturnValue(null);
		const onLinkOpen = vi.fn();
		render(<XtermTerminal onLinkOpen={onLinkOpen} theme="dark" />);
		const oscLinkHandler = state.lastTerminal!.options.linkHandler as {
			activate: (event: MouseEvent, uri: string) => void;
		};

		oscLinkHandler.activate({} as MouseEvent, "http://localhost:3000");

		expect(onLinkOpen).toHaveBeenCalledWith("http://localhost:3000");
		expect(open).not.toHaveBeenCalled();
		open.mockRestore();
	});

	it.each(["plain", "OSC 8"])("activates %s session links inside Tallpine", (kind) => {
		const onSessionLinkOpen = vi.fn();
		render(<XtermTerminal onSessionLinkOpen={onSessionLinkOpen} theme="dark" />);
		state.lastTerminal!.modes.mouseTrackingMode = "none";
		const osc = state.lastTerminal!.options.linkHandler as {
			activate: (event: MouseEvent, uri: string) => void;
		};
		if (kind === "OSC 8") {
			osc.activate({} as MouseEvent, "tallpine://sessions/project/session");
		} else {
			state.lastTerminal!.buffer.active.getLine = () =>
				testBufferLine(asciiCells("tallpine://sessions/project/session"));
			state.sessionLinkProvider!.provideLinks(1, (links) =>
				links![0]!.activate({} as MouseEvent),
			);
		}
		expect(onSessionLinkOpen).toHaveBeenCalledWith("tallpine://sessions/project/session");
	});

	it("opens a session link on a plain click while an application captures mouse input", () => {
		const onSessionLinkOpen = vi.fn();
		render(<XtermTerminal onSessionLinkOpen={onSessionLinkOpen} theme="dark" />);
		state.lastTerminal!.modes.mouseTrackingMode = "any";
		const handler = (state.lastTerminal!.options.linkHandler as {
			activate: (event: MouseEvent, uri: string) => void;
		}).activate;
		handler({ ctrlKey: false, metaKey: false } as MouseEvent, "tallpine://sessions/project/session");
		expect(onSessionLinkOpen).toHaveBeenCalledWith("tallpine://sessions/project/session");
	});

	it("opens a tallpine:// session link in the app instead of handing it to the OS", () => {
		const open = vi.spyOn(window, "open").mockReturnValue(null);
		const onSessionLinkOpen = vi.fn();
		render(<XtermTerminal onSessionLinkOpen={onSessionLinkOpen} theme="dark" />);
		const handler = (state.lastTerminal!.options.linkHandler as {
			activate: (event: MouseEvent, uri: string) => void;
		}).activate;
		handler({ ctrlKey: false, metaKey: false } as MouseEvent, "tallpine://sessions/project/session");
		expect(onSessionLinkOpen).toHaveBeenCalledWith("tallpine://sessions/project/session");
		expect(open).not.toHaveBeenCalled();
	});

	it("routes malformed Tallpine OSC links to in-app feedback without external dispatch", () => {
		const open = vi.spyOn(window, "open").mockReturnValue(null);
		const onSessionLinkOpen = vi.fn();
		render(<XtermTerminal onSessionLinkOpen={onSessionLinkOpen} theme="dark" />);
		state.lastTerminal!.modes.mouseTrackingMode = "none";
		const handler = (state.lastTerminal!.options.linkHandler as {
			activate: (event: MouseEvent, uri: string) => void;
		}).activate;
		handler({} as MouseEvent, "tallpine://sessions/project/session/kill");
		expect(onSessionLinkOpen).toHaveBeenCalledWith("tallpine://sessions/project/session/kill");
		expect(open).not.toHaveBeenCalled();
		open.mockRestore();
	});

	it("detects a session URL wrapped across terminal rows without consuming punctuation", () => {
		const onSessionLinkOpen = vi.fn();
		render(<XtermTerminal onSessionLinkOpen={onSessionLinkOpen} theme="dark" />);
		state.lastTerminal!.modes.mouseTrackingMode = "none";
		state.lastTerminal!.cols = 16;
		const rows = ["tallpine://sessions/pr", "oject/session)."];
		state.lastTerminal!.buffer.active.length = rows.length;
		state.lastTerminal!.buffer.active.getLine = (line) =>
			line >= rows.length
				? undefined
				: testBufferLine(asciiCells(rows[line]!), line === 1);
		state.sessionLinkProvider!.provideLinks(2, (links) => {
			expect(links?.[0]?.text).toBe("tallpine://sessions/project/session");
			expect(links?.[0]?.range).toEqual({
				start: { x: 1, y: 1 },
				end: { x: 13, y: 2 },
			});
			links?.[0]?.activate({} as MouseEvent);
		});
		expect(onSessionLinkOpen).toHaveBeenCalledWith("tallpine://sessions/project/session");
	});

	it.each([
		{
			name: "wide",
			prefix: [{ chars: "中", width: 2 }, { chars: "", width: 0 }, { chars: " ", width: 1 }],
			expected: { start: { x: 4, y: 1 }, end: { x: 26, y: 1 } },
		},
		{
			name: "combining",
			prefix: [{ chars: "e\u0301", width: 1 }, { chars: " ", width: 1 }],
			expected: { start: { x: 3, y: 1 }, end: { x: 25, y: 1 } },
		},
	])("maps a session link after $name characters to terminal cells", ({ prefix, expected }) => {
		render(<XtermTerminal onSessionLinkOpen={vi.fn()} theme="dark" />);
		const line = testBufferLine([
			...prefix,
			...asciiCells("tallpine://sessions/p/s"),
			{ chars: "", width: 1 },
		]);
		state.lastTerminal!.buffer.active.length = 1;
		state.lastTerminal!.buffer.active.getLine = (row) => (row === 0 ? line : undefined);

		state.sessionLinkProvider!.provideLinks(1, (links) => {
			expect(links?.[0]?.range).toEqual(expected);
		});
	});

	it("maps a wrapped session link with a wide character prefix to terminal cells", () => {
		render(<XtermTerminal onSessionLinkOpen={vi.fn()} theme="dark" />);
		state.lastTerminal!.cols = 10;
		const rows = [
			testBufferLine([
				{ chars: "中", width: 2 },
				{ chars: "", width: 0 },
				{ chars: " ", width: 1 },
				...asciiCells("tallpine://se"),
			]),
			testBufferLine(asciiCells("ssions/p/s"), true),
		];
		state.lastTerminal!.buffer.active.length = rows.length;
		state.lastTerminal!.buffer.active.getLine = (row) => rows[row];

		state.sessionLinkProvider!.provideLinks(2, (links) => {
			expect(links?.[0]?.range).toEqual({
				start: { x: 4, y: 1 },
				end: { x: 10, y: 2 },
			});
		});
	});

	it.each([
		["plain", "Option/Alt", { altKey: true }],
		["OSC 8", "Option/Alt", { altKey: true }],
		["plain", "Command", { metaKey: true }],
		["OSC 8", "Command", { metaKey: true }],
	])("opens %s web links in the system browser on %s+Click", (kind, _modifier, event) => {
		const openExternal = vi.fn().mockResolvedValue(undefined);
		window.tallpine!.app.openExternal = openExternal;
		const onLinkOpen = vi.fn();
		render(<XtermTerminal onLinkOpen={onLinkOpen} theme="dark" />);
		const oscHandler = state.lastTerminal!.options.linkHandler as { activate: (event: MouseEvent, uri: string) => void };
		const handler = kind === "plain" ? state.linkHandler! : oscHandler.activate;
		handler(event as unknown as MouseEvent, "https://example.com");
		expect(openExternal).toHaveBeenCalledWith("https://example.com");
		expect(onLinkOpen).not.toHaveBeenCalled();
	});

	it("opens non-web links (mailto:) in the system browser, not the Tallpine browser", () => {
		const open = vi.spyOn(window, "open").mockReturnValue(null);
		const onLinkOpen = vi.fn();
		render(<XtermTerminal onLinkOpen={onLinkOpen} theme="dark" />);

		expect(state.linkHandler).toBeTypeOf("function");
		state.linkHandler!({} as MouseEvent, "mailto:dev@example.com");

		expect(open).toHaveBeenCalledWith("mailto:dev@example.com", "_blank", "noopener");
		expect(onLinkOpen).not.toHaveBeenCalled();
		open.mockRestore();
	});

	it("forces plain drag selection without raw xterm data forwarding", () => {
		render(<XtermTerminal theme="dark" />);

		expect(state.lastTerminal!.options.macOptionClickForcesSelection).toBe(true);
		expect(state.lastTerminal!._core._selectionService.enable).toHaveBeenCalled();
		expect(state.lastTerminal!._core.element.classList.remove).toHaveBeenCalledWith("enable-mouse-events");
		expect(state.lastTerminal!._core._selectionService.shouldForceSelection({} as MouseEvent)).toBe(true);
	});

	it("never reads or sends a dropped file over the size cap", async () => {
		const saveDroppedFile = vi.fn();
		window.tallpine!.terminal.saveDroppedFile = saveDroppedFile;
		const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
		const { container } = render(<XtermTerminal theme="dark" />);
		const file = new File(["x"], "huge.bin");
		Object.defineProperty(file, "size", { value: MAX_DROPPED_FILE_BYTES + 1 });
		const arrayBuffer = vi.spyOn(file, "arrayBuffer");

		fireEvent.drop(container.firstElementChild!, { dataTransfer: dropTransfer({ isDirectory: false }, [file]) });
		await waitFor(() => expect(warn).toHaveBeenCalled());
		expect(arrayBuffer).not.toHaveBeenCalled();
		expect(saveDroppedFile).not.toHaveBeenCalled();
		warn.mockRestore();
	});

	it("attaches a real dropped file's saved path, and still lets the drop bubble to the window", async () => {
		const saveDroppedFile = vi.fn().mockResolvedValue("/tmp/tallpine-dropped/notes.txt");
		window.tallpine!.terminal.saveDroppedFile = saveDroppedFile;
		const onInput = vi.fn();
		const { container } = render(
			<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />,
		);
		const host = container.firstElementChild!;
		const bubbled = vi.fn();
		window.addEventListener("drop", bubbled);
		const file = new File(["hello"], "notes.txt", { type: "text/plain" });

		fireEvent.drop(host, { dataTransfer: dropTransfer({ isDirectory: false }, [file]) });
		await waitFor(() => expect(saveDroppedFile).toHaveBeenCalledWith({ name: "notes.txt", bytes: expect.any(Uint8Array) }));
		await waitFor(() => expect(onInput).toHaveBeenCalledWith("/tmp/tallpine-dropped/notes.txt ", "paste"));

		window.removeEventListener("drop", bubbled);
		// Regression: no stopPropagation here — _shell.tsx's window-level listener
		// still needs this drop to reset the drag-depth counter its preceding
		// dragenter bumped, or the next folder drag inherits a stale depth and
		// never shows the overlay.
		expect(bubbled).toHaveBeenCalledTimes(1);
	});

	// Regression: a dropped folder is the app-wide "open as project" gesture
	// (_shell.tsx's window-level drop handler), not a file to attach — dropping
	// one over an active terminal pane must not be swallowed as a file-attach.
	it("lets a dropped folder bubble to the window instead of attaching it as a file", () => {
		const saveDroppedFile = vi.fn();
		window.tallpine!.terminal.saveDroppedFile = saveDroppedFile;
		const { container } = render(<XtermTerminal theme="dark" />);
		const host = container.firstElementChild!;
		const bubbled = vi.fn();
		window.addEventListener("drop", bubbled);
		const folder = new File([], "my-project");

		fireEvent.drop(host, { dataTransfer: dropTransfer({ isDirectory: true }, [folder]) });

		window.removeEventListener("drop", bubbled);
		expect(bubbled).toHaveBeenCalledTimes(1);
		expect(saveDroppedFile).not.toHaveBeenCalled();
	});

	it("does not extend a drag selection into a neighboring pane or keep auto-scrolling", () => {
		render(<XtermTerminal theme="dark" />);
		const selectionService = state.lastTerminal!._core._selectionService;
		const listener = selectionService._mouseMoveListener!;

		// Simulate xterm's timer having been armed by a previous drag below the
		// terminal before the pointer crosses horizontally into the sidebar.
		selectionService._dragScrollAmount = 1;
		listener(new MouseEvent("mousemove", { clientX: 801 }));
		expect(state.mouseMoveListener).not.toHaveBeenCalled();
		expect(selectionService._dragScrollAmount).toBe(0);

		listener(new MouseEvent("mousemove", { clientX: 800 }));
		expect(state.mouseMoveListener).toHaveBeenCalledTimes(1);
	});
});
