import { createCursorPositionReportForwarder, createOscColorReportForwarder } from "../../lib/osc-color-report";
import type { XtermEnv, XtermState } from "./xterm-types";
import { installSetup } from "./xterm-setup";
import { installInput } from "./xterm-input";
import { MAC_TERMINAL_SCROLLBAR_WIDTH } from "./xterm-scroll";

export function installFit(env: XtermEnv, st: XtermState, setupOut: NonNullable<ReturnType<typeof installSetup>>, inputOut: ReturnType<typeof installInput>) {
	const {
		callbacksRef,
		fitRef,
		gridMeasuredRef,
	} = env;
	const {
		fit,
		host,
		term,
	} = setupOut;
	const {
		emitUserInput,
	} = inputOut;
	const proposeGrid = () => (host.clientWidth > 0 && host.clientHeight > 0 ? fit.proposeDimensions() : undefined);
	st.pendingReplayWrites = 0;
	let usesSynchronizedOutput = false;
	st.resizeGeneration = 0;
	let coveredResizeGeneration = 0;
	let parsedResizeGeneration = -1;
	let waitingForResizeOutput = false;
	const fitTerminal = () => {
		// Parked terminals keep their last measured box and continue parsing
		// output, but must not refit or emit PTY resizes while hidden.
		if (callbacksRef.current.isVisible === false) return;
		try {
			const grid = proposeGrid();
			if (grid) resizeGrid(grid.cols, grid.rows);
		} catch {
			// Container momentarily has no size (hidden/unmounting) — a later
			// trigger retries.
		}
	};
	// Retained activation waits for a stable box behind its cover. Visible
	// pane resizes use observer geometry below and do not wait for this timer.
	const FIT_QUIET_MS = 120;
	const FIT_CAP_MS = 500;
	st.fitQuietTimer = null;
	st.fitCapTimer = null;
	let fitAllowsHidden = false;
	st.disposed = false;
	const fitSettledListeners = new Set<() => void>();
	const flushScheduledFit = () => {
		if (st.disposed) return;
		if (st.fitQuietTimer !== null) {
			clearTimeout(st.fitQuietTimer);
			st.fitQuietTimer = null;
		}
		if (st.fitCapTimer !== null) {
			clearTimeout(st.fitCapTimer);
			st.fitCapTimer = null;
		}
		if (fitAllowsHidden || callbacksRef.current.isVisible !== false) {
			try {
				const grid = proposeGrid();
				if (grid) resizeGrid(grid.cols, grid.rows, fitAllowsHidden);
			} catch {
				// The next observer/window event retries if the host is transiently
				// unmeasurable (for example while entering fullscreen).
			}
		}
		fitAllowsHidden = false;
		for (const listener of [...fitSettledListeners]) listener();
		fitSettledListeners.clear();
	};
	const scheduleStableFit = (allowHidden = false, onSettled?: () => void) => {
		if (st.disposed) return;
		if (!allowHidden && callbacksRef.current.isVisible === false) return;
		fitAllowsHidden ||= allowHidden;
		if (onSettled) fitSettledListeners.add(onSettled);
		if (st.fitQuietTimer !== null) clearTimeout(st.fitQuietTimer);
		st.fitQuietTimer = setTimeout(flushScheduledFit, FIT_QUIET_MS);
		if (st.fitCapTimer === null) st.fitCapTimer = setTimeout(flushScheduledFit, FIT_CAP_MS);
	};
	// While activation preparation is pending, observer/window events must keep
	// extending the same quiet window even though the container is intentionally
	// hidden behind the cover. A normally parked terminal still ignores them.
	const scheduleVisibleFit = () => scheduleStableFit(fitAllowsHidden);
	fitRef.current = scheduleVisibleFit;
	// Window/DPR changes and unmeasured startup cells still need FitAddon.
	// Coalesce those recovery reads into one frame; measured pane resizes
	// take the observer path below instead.
	st.liveFitFrame = null;
	const scheduleLiveFit = () => {
		if (st.disposed || callbacksRef.current.isVisible === false || st.liveFitFrame !== null) return;
		st.liveFitFrame = requestAnimationFrame(() => {
			st.liveFitFrame = null;
			fitTerminal();
		});
	};

	const raf = requestAnimationFrame(fitTerminal);
	// 50/250ms catch the common settle; 600/1200ms are a session-bounded
	// backstop. By 600ms the WebGL atlas and font metrics are unambiguously
	// warm, so even if the convergence loop below detached at a briefly-stable
	// wrong measurement, this re-measures the real cell box and corrects,
	// firing the PTY resize that makes the pane repaint cleanly (clearing
	// any ghost frame). fit() is idempotent: a no-op when the grid is already
	// right, so a correct terminal never reflows.
	const settleTimers = [50, 250, 600, 1200].map((ms) => window.setTimeout(scheduleVisibleFit, ms));
	if (document.fonts?.ready) {
		void document.fonts.ready.then(() => scheduleStableFit());
	}
	// Read static xterm padding once. Live fits use the observer's content box
	// and xterm's public cell metrics, avoiding FitAddon's computed-style reads
	// and the extra frame between panel layout and terminal resize.
	const terminalStyle = term.element ? getComputedStyle(term.element) : undefined;
	const paddingX = (parseFloat(terminalStyle?.paddingLeft ?? "0") || 0)
		+ (parseFloat(terminalStyle?.paddingRight ?? "0") || 0);
	const paddingY = (parseFloat(terminalStyle?.paddingTop ?? "0") || 0)
		+ (parseFloat(terminalStyle?.paddingBottom ?? "0") || 0);
	// Cover cleared/reflowed resize frames until the TUI's next output paints.
	// Copy in onRender before WebGL discards its drawing buffer.
	const resizeCover = document.createElement("canvas");
	resizeCover.setAttribute("aria-hidden", "true");
	Object.assign(resizeCover.style, {
		position: "absolute", pointerEvents: "none", zIndex: "1",
		backgroundColor: "var(--color-bg-terminal-opaque)",
	});
	let hasResizeFrame = false;
	const cancelResizeCover = () => {
		if (!waitingForResizeOutput) return;
		waitingForResizeOutput = false;
		resizeCover.remove();
		term.refresh(0, term.rows - 1);
	};
	host.addEventListener("wheel", cancelResizeCover, { passive: true });
	host.addEventListener("keydown", cancelResizeCover);
	host.addEventListener("pointerdown", cancelResizeCover);
	const paintedResizeFrame = term.onRender(() => {
		if (st.disposed || term.modes.synchronizedOutputMode) return;
		if (waitingForResizeOutput && parsedResizeGeneration < coveredResizeGeneration) return;
		waitingForResizeOutput = false;
		resizeCover.remove();
		hasResizeFrame = false;
		if (callbacksRef.current.isVisible === false || !usesSynchronizedOutput) {
			resizeCover.width = 0;
			resizeCover.height = 0;
			return;
		}
		const screen = host.querySelector<HTMLElement>(".xterm-screen");
		// The first canvas is the transparent link layer, not terminal text.
		const source = screen?.querySelector<HTMLCanvasElement>(":scope > canvas:not(.xterm-link-layer)");
		if (!screen || !source || source.width === 0 || source.height === 0) return;
		const context = resizeCover.getContext("2d");
		if (!context) return;
		if (resizeCover.width !== source.width) resizeCover.width = source.width;
		if (resizeCover.height !== source.height) resizeCover.height = source.height;
		context.clearRect(0, 0, resizeCover.width, resizeCover.height);
		context.drawImage(source, 0, 0);
		resizeCover.style.width = source.style.width;
		resizeCover.style.height = source.style.height;
		resizeCover.style.left = `${screen.offsetLeft}px`;
		resizeCover.style.top = `${screen.offsetTop}px`;
		hasResizeFrame = true;
	});
	// Commit before paint; onResize immediately forwards the grid to the PTY.
	const resizeGrid = (cols: number, rows: number, allowHidden = false) => {
		if (st.disposed || (!allowHidden && callbacksRef.current.isVisible === false)) return;
		const firstMeasurement = !gridMeasuredRef.current;
		gridMeasuredRef.current = true;
		if (cols !== term.cols || rows !== term.rows) {
			const buffer = term.buffer.active;
			const wasAtBottom = buffer.type === "normal" && buffer.viewportY === buffer.baseY;
			st.resizeGeneration++;
			if (hasResizeFrame && usesSynchronizedOutput && !waitingForResizeOutput) {
				// Advancing this on each movement would starve redraws during a drag.
				coveredResizeGeneration = st.resizeGeneration;
				waitingForResizeOutput = true;
				host.appendChild(resizeCover);
			}
			term.resize(cols, rows);
			if (wasAtBottom) term.scrollToBottom();
		}
		// A terminal attached before it could measure claimed no size. Publish
		// its first measured grid even when it equals xterm's default, which
		// fires no onResize.
		if (firstMeasurement && callbacksRef.current.isVisible !== false) {
			callbacksRef.current.onVisibleSize?.(term.cols, term.rows);
		}
	};
	const synchronizedFrames = term.parser.registerCsiHandler({ prefix: "?", final: "h" }, (params) => {
		if (params.includes(2026)) usesSynchronizedOutput = true;
		return false;
	});
	const didParseResizeOutput = (generation: number) => {
		if (!waitingForResizeOutput || generation < coveredResizeGeneration) return;
		parsedResizeGeneration = Math.max(parsedResizeGeneration, generation);
		// Request a paint after parsing. xterm still honors synchronized output.
		term.refresh(0, term.rows - 1);
	};
	// Never send capability replies for replayed queries to the live agent.
	const synchronizedCapability = term.parser.registerCsiHandler(
		{ prefix: "?", intermediates: "$", final: "p" },
		(params) => {
			if (params[0] !== 2026) return false;
			if (st.pendingReplayWrites === 0) {
				emitUserInput(`\x1b[?2026;${term.modes.synchronizedOutputMode ? 1 : 2}$y`, "protocol");
			}
			return true;
		},
	);
	let wasDragging = document.body.classList.contains("is-resizing-x");
	const dragStateObserver = new MutationObserver(() => {
		const dragging = document.body.classList.contains("is-resizing-x");
		if (wasDragging && !dragging && callbacksRef.current.isVisible !== false) {
			// Pointerup can precede the final ResizeObserver delivery.
			const grid = proposeGrid();
			if (grid) resizeGrid(grid.cols, grid.rows);
			term.refresh(0, term.rows - 1);
		}
		wasDragging = dragging;
	});
	dragStateObserver.observe(document.body, { attributes: true, attributeFilter: ["class"] });
	const observer = new ResizeObserver((entries) => {
		if (st.disposed) return;
		if (callbacksRef.current.isVisible === false) {
			waitingForResizeOutput = false;
			hasResizeFrame = false;
			resizeCover.remove();
			// Activation preparation may fit behind its cover; parked panes stay
			// inert. Preserve the quiet window and its completion listeners.
			scheduleVisibleFit();
			return;
		}
		const box = entries.find((entry) => entry.target === host)?.contentRect;
		const cell = term.dimensions?.css.cell;
		if (!box || !cell || !Number.isFinite(cell.width) || !Number.isFinite(cell.height)
			|| cell.width <= 0 || cell.height <= 0) {
			scheduleLiveFit();
			return;
		}
		if (box.width <= 0 || box.height <= 0) {
			return;
		}
		const gutter = term.options.scrollback === 0 || term.options.scrollbar?.showScrollbar === false
			? 0 : (term.options.scrollbar?.width ?? MAC_TERMINAL_SCROLLBAR_WIDTH);
		const cols = Math.max(2, Math.floor((Math.floor(box.width) - paddingX - gutter) / cell.width));
		const rows = Math.max(1, Math.floor((Math.floor(box.height) - paddingY) / cell.height));
		resizeGrid(cols, rows);
	});
	observer.observe(host);

	// Recovery re-fit that does NOT depend on the host box changing size.
	//
	// FitAddon derives the grid by dividing the pane box by the renderer's
	// measured cell box. That box is measured asynchronously: the WebGL
	// renderer loads after open() and the monospace font's real metrics
	// resolve a frame or more later, so the early fits above can divide by a
	// not-yet-final cell box, mis-count cols/rows, and clip the grid inside the
	// pane. The fixed settle window (rAF, timeouts, fonts.ready) may all run
	// before the cell box is final, and the ResizeObserver never fires to
	// correct it because the host's pixel box is a stable height:100%, so a
	// wrong grid would otherwise freeze for the whole session.
	//
	// onRender fires on every renderer repaint, including the repaint after
	// the metrics settle. Each fire re-proposes dimensions from the *current*
	// measured cell box. Crucially we never re-fit straight off a single
	// frame's proposal: the WebGL atlas warm-up can emit a one-frame transient
	// cell box (e.g. a doubled box on a HiDPI display) that halves the grid,
	// and committing it would lock the terminal at half size and detach (the
	// #313 ghost). So a differing proposal must REPEAT identically across two
	// consecutive renders — proving the measurement settled — before we apply
	// it. proposeDimensions returns undefined until the cell box is non-zero,
	// so a fit is never accepted from an unmeasured cell. Once the proposal
	// holds at the live grid for a few frames (or a hard re-fit cap is hit) the
	// listener detaches, so steady-state content renders cost nothing.
	const STABLE_FRAMES_TARGET = 3;
	const MAX_REFITS = 20;
	let stableFrames = 0;
	let refits = 0;
	let pending: { cols: number; rows: number } | null = null;
	const stabilizer = term.onRender(() => {
		const proposed = proposeGrid();
		if (!proposed || !proposed.cols || !proposed.rows) return;
		if (proposed.cols !== term.cols || proposed.rows !== term.rows) {
			stableFrames = 0;
			// Only act once the same differing proposal repeats — a single-frame
			// transient never gets committed, it just updates `pending`.
			if (pending && pending.cols === proposed.cols && pending.rows === proposed.rows) {
				pending = null;
				if (refits++ >= MAX_REFITS) {
					stabilizer.dispose();
					return;
				}
				fitTerminal();
				return;
			}
			pending = { cols: proposed.cols, rows: proposed.rows };
			return;
		}
		pending = null;
		if (++stableFrames >= STABLE_FRAMES_TARGET) stabilizer.dispose();
	});

	// OS window resize and monitor/DPR changes also alter the true cell box
	// without touching the host's height:100% box, so the ResizeObserver above
	// misses them. Listen on window directly as a session-long recovery path.
	window.addEventListener("resize", scheduleLiveFit);

	// Do not replace this with term.onData. xterm's raw data stream can include
	// terminal-generated control responses during attach/repaint; forwarding
	// those bytes through the mux writes dirty input into the real Codex PTY and
	// corrupts the TUI. Keyboard is the only safe generic text path here; paste,
	// composition, shortcuts, and wheel reports are emitted explicitly below.
	// Forward validated OSC 4/10/11/12 color replies and cursor-position
	// reports here; mode 2026 queries are answered by the live parser hook above.
	// Interactive prompts such as `gh auth login` use DSR to ask
	// xterm for the cursor position and block until the corresponding CPR reaches
	// the PTY. Other onData bytes must not reach the PTY or agent TUIs break.
	// Retained terminals can change providers without remounting. Keep the
	// listener mounted for every provider so standard color replies continue to
	// reach the PTY after a provider change.
	const oscColorForwarder = createOscColorReportForwarder((report) => {
		emitUserInput(report, "protocol");
	});
	const cursorPositionForwarder = createCursorPositionReportForwarder((report) => {
		emitUserInput(report, "protocol");
	});
	const protocolInput = term.onData((data) => {
		// xterm answers old colour queries in a replayed scrollback, and the agent, no longer
		// waiting, takes the answer as typed text ("11;rgb:…" in Droid's prompt).
		if (st.pendingReplayWrites === 0) oscColorForwarder.push(data);
		cursorPositionForwarder.push(data);
	});
	const keyInput = term.onKey(({ key }) => emitUserInput(key, "keyboard"));

	// Translate wheel motion into SGR wheel reports for the pane (see
	// sgrWheelReport), one report per scrolled line. WheelEvent.deltaMode
	// varies by platform/device: trackpads and normalized wheels report
	// pixels (mode 0, the macOS case), while many Linux/Windows mouse wheels
	// report whole lines (mode 1) or pages (mode 2). Mirror xterm's native
	// getLinesScrolled across all three so scroll works everywhere; pixel
	// deltas accumulate so a full cell-height emits one line. Returning false
	// suppresses xterm's arrow-key wheel fallback. Ctrl/Cmd wheel is the
	// font-size zoom (CenterPane), so leave it for that handler.
	return {
		cancelResizeCover,
		cursorPositionForwarder,
		didParseResizeOutput,
		dragStateObserver,
		fitSettledListeners,
		keyInput,
		observer,
		oscColorForwarder,
		paintedResizeFrame,
		protocolInput,
		raf,
		resizeCover,
		scheduleLiveFit,
		scheduleStableFit,
		settleTimers,
		stabilizer,
		synchronizedCapability,
		synchronizedFrames,
	};
}
