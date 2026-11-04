import { MAX_DROPPED_FILE_BYTES } from "../../../shared/dropped-file";
import type { AttachableTerminal } from "../../hooks/useTerminalSession";
import { tallpineBridge } from "../../lib/bridge";
import { TERMINAL_FONT_SIZE_DEFAULT } from "../../lib/design-tokens";
import { cursorColorSchemeReplyForOutput } from "../../lib/cursor-color-scheme";
import type { XtermEnv, XtermState } from "./xterm-types";
import { installSetup } from "./xterm-setup";
import { installScrollbar } from "./xterm-scrollbar";
import { installInput } from "./xterm-input";
import { installFit } from "./xterm-fit";
import { pageKeyReport, SGR_WHEEL_UP, SGR_WHEEL_DOWN, sgrWheelReport } from "./xterm-scroll";
import { canAutoFocusTerminal } from "./xterm-shortcuts";

export function installActivation(env: XtermEnv, st: XtermState, setupOut: NonNullable<ReturnType<typeof installSetup>>, scrollbarOut: ReturnType<typeof installScrollbar>, inputOut: ReturnType<typeof installInput>, fitOut: ReturnType<typeof installFit>) {
	const {
		announcedCursorSchemeRef,
		callbacksRef,
		gridMeasuredRef,
		hostRef,
	} = env;
	const {
		host,
		shell,
		term,
	} = setupOut;
	const {
		scheduleScrollbarUpdate,
		webglLease,
	} = scrollbarOut;
	const {
		clearSuppressNativePaste,
		emitUserInput,
		focusTerminal,
		notifyCursorScheme,
		pasteText,
		userInputListeners,
	} = inputOut;
	const {
		cursorPositionForwarder,
		didParseResizeOutput,
		scheduleStableFit,
	} = fitOut;
	let wheelAccumPx = 0;
	term.attachCustomWheelEventHandler((event) => {
		if (event.ctrlKey || event.metaKey) return false;
		let lines: number;
		if (event.deltaMode === 1 /* DOM_DELTA_LINE */) {
			lines = Math.trunc(event.deltaY) || Math.sign(event.deltaY);
		} else if (event.deltaMode === 2 /* DOM_DELTA_PAGE */) {
			lines = (Math.trunc(event.deltaY) || Math.sign(event.deltaY)) * term.rows;
		} else {
			const rowHeight = (term.options.fontSize ?? TERMINAL_FONT_SIZE_DEFAULT) * (term.options.lineHeight ?? 1);
			wheelAccumPx += event.deltaY;
			lines = Math.trunc(wheelAccumPx / rowHeight);
			wheelAccumPx -= lines * rowHeight;
		}
		if (lines === 0) return false;
		// A full-screen TUI that keeps its own transcript and scrolls it only by
		// keyboard (opencode) ignores wheel/mouse reports on every platform; route
		// its wheel to page keys. Kept first so opencode is unaffected by the
		// buffer-aware paths below.
		if (callbacksRef.current.paneScrollsByKeyboard) {
			emitUserInput(pageKeyReport(lines), "wheel");
			return false;
		}
		// A normal-buffer pane with mouse tracking off (codex, a plain shell)
		// prints its transcript and relies on the terminal's own scrollback — the
		// way it scrolls in a raw terminal. Scroll xterm's viewport locally; the
		// pane never sees these bytes. Requires scrollback > 0 (see Terminal opts).
		if (term.modes.mouseTrackingMode === "none" && term.buffer.active.type === "normal") {
			term.scrollLines(lines);
			return false;
		}
		// Mouse tracking on: the pane (tmux/zellij copy-mode, or any app that
		// tracks the mouse) acts on SGR wheel reports. On Windows conpty this
		// reaches the app directly; under a mux it drives copy-mode.
		if (term.modes.mouseTrackingMode !== "none") {
			const button = lines < 0 ? SGR_WHEEL_UP : SGR_WHEEL_DOWN;
			const screen = term.screenElement?.getBoundingClientRect();
			if (!screen || screen.width <= 0 || screen.height <= 0
				|| !Number.isFinite(event.clientX) || !Number.isFinite(event.clientY)) return false;
			// Measure the grid itself, excluding host padding and the scrollbar.
			// Dividing its rendered size also accounts for CSS scaling and zoom.
			const column = Math.max(1, Math.min(term.cols,
				Math.floor((event.clientX - screen.left) * term.cols / screen.width) + 1));
			const row = Math.max(1, Math.min(term.rows,
				Math.floor((event.clientY - screen.top) * term.rows / screen.height) + 1));
			emitUserInput(sgrWheelReport(button, Math.abs(lines), column, row), "wheel");
			return false;
		}
		// Alt-buffer pane with mouse tracking off and no keyboard-scroll hint:
		// no scrollback to move locally, so fall back to page keys.
		emitUserInput(pageKeyReport(lines), "wheel");
		return false;
	});
	const pasteInput = (event: ClipboardEvent) => {
		event.preventDefault();
		event.stopPropagation();
		if (st.suppressNextNativePaste) {
			clearSuppressNativePaste();
			return;
		}
		const text = event.clipboardData?.getData("text/plain") ?? "";
		pasteText(text);
	};
	const compositionInput = (event: CompositionEvent) => {
		emitUserInput(event.data, "composition");
	};
	shell.addEventListener("paste", pasteInput, true);
	shell.addEventListener("compositionend", compositionInput, true);

	// A file dropped on the pane inserts its path, mirroring a native terminal
	// so an agent (e.g. Claude Code) attaches it. The sandboxed renderer cannot
	// read a dropped file's original path on macOS, so the bytes are stashed to
	// a temp file by the main process and that path is inserted instead.
	const isFileDrag = (event: DragEvent) => Array.from(event.dataTransfer?.types ?? []).includes("Files");
	const dragOverInput = (event: DragEvent) => {
		if (!isFileDrag(event)) return;
		event.preventDefault();
		if (event.dataTransfer) event.dataTransfer.dropEffect = "copy";
	};
	// A dropped folder is the app-wide "open as project" gesture (see
	// _shell.tsx's window-level drop handler), not a file to attach. Let it
	// bubble untouched — swallowing it here (preventDefault/stopPropagation)
	// would silently absorb the drop into this file-attach flow instead.
	const isDirectoryDrag = (event: DragEvent) =>
		event.dataTransfer?.items?.[0]?.webkitGetAsEntry?.()?.isDirectory ?? false;
	const dropInput = (event: DragEvent) => {
		if (isDirectoryDrag(event)) return;
		const files = Array.from(event.dataTransfer?.files ?? []);
		if (files.length === 0) return;
		event.preventDefault();
		// Deliberately no stopPropagation: _shell.tsx's window-level listener
		// still needs this drop to reset its drag-depth counter (bumped by the
		// dragenter that already bubbled past this host, unseen by this
		// handler), or the next folder drag inherits a stale nonzero depth and
		// never shows the overlay.
		void (async () => {
			const paths: string[] = [];
			for (const file of files) {
				// Skip before reading: arrayBuffer() would allocate the whole file.
				if (file.size > MAX_DROPPED_FILE_BYTES) {
					console.warn(`Dropped file ${file.name} is too large to attach`);
					continue;
				}
				try {
					const bytes = new Uint8Array(await file.arrayBuffer());
					const saved = await tallpineBridge.terminal.saveDroppedFile({ name: file.name, bytes });
					if (saved) paths.push(saved);
				} catch (error) {
					console.warn("Unable to attach dropped file", error);
				}
			}
			if (paths.length === 0) return;
			pasteText(`${paths.map((p) => (/\s/.test(p) ? `'${p}'` : p)).join(" ")} `);
		})();
	};
	shell.addEventListener("dragover", dragOverInput);
	shell.addEventListener("drop", dropInput);

	const showLatestOutput = () => {
		term.scrollToBottom();
		// Hidden output can leave the offscreen DOM scrollbar stale even
		// after xterm's logical viewport moves. Synchronize it before either
		// the first-load cover or retained-cache container is revealed.
		const viewport = host.querySelector<HTMLElement>(".xterm-viewport");
		if (!viewport) return;
		viewport.scrollTop = Math.max(0, viewport.scrollHeight - viewport.clientHeight);
		scheduleScrollbarUpdate();
	};

	st.cancelActivationPreparation = null;
	const prepareForActivation = (): Promise<void> => {
		st.cancelActivationPreparation?.();
		// Before the hidden paint frames, so the revealed frame is drawn by WebGL.
		webglLease.activate();
		return new Promise((resolve) => {
			let firstFrame: number | null = null;
			let paintFrame: number | null = null;
			let finished = false;
			const finish = () => {
				if (finished) return;
				finished = true;
				if (firstFrame !== null) cancelAnimationFrame(firstFrame);
				if (paintFrame !== null) cancelAnimationFrame(paintFrame);
				if (st.cancelActivationPreparation === finish) st.cancelActivationPreparation = null;
				resolve();
			};
			st.cancelActivationPreparation = finish;

			const finishAcrossPaintFrames = () => {
				if (finished) return;
				showLatestOutput();
				firstFrame = requestAnimationFrame(() => {
					firstFrame = null;
					// Reconcile after the settled fit, then remain hidden through a
					// second frame so Chromium composites the final viewport.
					showLatestOutput();
					paintFrame = requestAnimationFrame(() => {
						paintFrame = null;
						finish();
					});
				});
			};
			// The container is in its real slot but remains hidden. Wait for its
			// dimensions to settle (including fullscreen/sidebar transitions), fit
			// once, and avoid the old unconditional full-grid refresh.
			scheduleStableFit(true, finishAcrossPaintFrames);
		});
	};

	// Live cols/rows getters: the owner reads the current grid at attach time,
	// not a snapshot taken at ready time (the first fit may not have run yet).
	const handle: AttachableTerminal = {
		get cols() {
			return term.cols;
		},
		get rows() {
			return term.rows;
		},
		get hasMeasuredGrid() {
			return gridMeasuredRef.current;
		},
		// Forward xterm's write callback: it fires once THIS chunk has been
		// parsed into the buffer, which is what lets the attachment reveal the
		// pane at the replay's settled scroll position (issue #3160).
		write: (data, done, source = "live") => {
			let hasEsc = false;
			for (let i = 0; i < data.length; i++) {
				if (data[i] === 0x1b) {
					hasEsc = true;
					break;
				}
			}
			if (source === "replay") {
				// Existing panes replay historical DSRs through xterm. Clear any old
				// correlation credits so their generated CPRs cannot reach the live PTY.
				cursorPositionForwarder.dispose();
			}
			if (hasEsc || cursorPositionForwarder.hasPartialRequest()) {
				// A DSR can be split immediately after ESC. Decode an ESC-free chunk
				// only while a request prefix from the preceding chunk is incomplete.
				const chunk = new TextDecoder().decode(data);
				if (source === "live") cursorPositionForwarder.observeOutput(chunk);
				if (hasEsc) {
					const reply = callbacksRef.current.supportsCursorColorScheme
						? cursorColorSchemeReplyForOutput(chunk, callbacksRef.current.theme)
						: null;
					if (reply) {
						announcedCursorSchemeRef.current = null;
						notifyCursorScheme(callbacksRef.current.theme, true, true);
					}
				}
			}
			const outputResizeGeneration = st.resizeGeneration;
			if (source === "replay") st.pendingReplayWrites++;
			term.write(data, () => {
				if (source === "replay") st.pendingReplayWrites--;
				else didParseResizeOutput(outputResizeGeneration);
				scheduleScrollbarUpdate();
				done?.();
			});
		},
		writeln: (line) => {
			const outputResizeGeneration = st.resizeGeneration;
			term.writeln(line, () => {
				didParseResizeOutput(outputResizeGeneration);
				scheduleScrollbarUpdate();
			});
		},
		showLatestOutput,
		prepareForActivation,
		requestActivationFocus: () => {
			// Parked terminals were deliberately blurred on switch-away and the
			// autofocus effect's guarded attempt can be cancelled or refused by
			// the momentary focus holder (issue #6140). The cache asks again on
			// every re-activation; the guard below keeps this from stealing
			// focus from dialogs or other legitimately focused controls.
			window.setTimeout(() => {
				const host = hostRef.current;
				if (
					!host ||
					callbacksRef.current.isVisible === false ||
					callbacksRef.current.focusRequested === false ||
					!canAutoFocusTerminal(host)
				) return;
				focusTerminal();
			}, 0);
		},
		notifyCursorColorScheme: () => {
			if (callbacksRef.current.supportsCursorColorScheme) {
				notifyCursorScheme(callbacksRef.current.theme, false, true);
			}
		},
		sendUserInput: (data, source = "shortcut") => emitUserInput(data, source),
		onUserInput: (listener) => {
			userInputListeners.add(listener);
			return { dispose: () => userInputListeners.delete(listener) };
		},
		onResize: (listener) => term.onResize(listener),
	};
	callbacksRef.current.onReady?.(handle);

	return {
		compositionInput,
		dragOverInput,
		dropInput,
		pasteInput,
	};
}
