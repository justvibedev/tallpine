import { leaseWebglRenderer } from "../../lib/terminal-webgl-pool";
import type { XtermEnv, XtermState } from "./xterm-types";
import { installSetup } from "./xterm-setup";
import { DevXtermHost } from "./xterm-types";
import { forceSelectionMode, confineDragSelectionToTerminalWidth, MAC_TERMINAL_SCROLLBAR_IDLE_MS } from "./xterm-scroll";

export function installScrollbar(env: XtermEnv, st: XtermState, setupOut: NonNullable<ReturnType<typeof installSetup>>) {
	const {
		callbacksRef,
		scrollbarThumbRef,
		scrollbarTrackRef,
		translucent,
		webglLeaseRef,
	} = env;
	const {
		host,
		term,
	} = setupOut;
	let visibleContentReported = false;
	const reportVisibleContent = () => {
		if (visibleContentReported || !callbacksRef.current.onVisibleContent) return;
		const buffer = term.buffer.active;
		for (let row = buffer.viewportY; row < buffer.viewportY + term.rows; row++) {
			if (!buffer.getLine(row)?.translateToString(true).trim()) continue;
			visibleContentReported = true;
			callbacksRef.current.onVisibleContent();
			break;
		}
	};
	const visibleContentRender = term.onRender(reportVisibleContent);
	reportVisibleContent();
	// Browser integration tests need to wait on xterm's buffer state, not
	// infer it from a hidden viewport element whose scrollTop can lag.
	// Vite removes this development-only seam from packaged builds.
	if (import.meta.env.DEV) {
		(host as DevXtermHost).__tallpineXtermForTest = term;
	}
	// WebGL forces alpha to 1 for default-background cells carrying flags
	// such as dim, leaving opaque rectangles on a transparent canvas. The
	// DOM renderer respects their alpha and preserves the same buffer.
	const webglLease = leaseWebglRenderer(term, () => callbacksRef.current.isVisible !== false, !translucent);
	webglLeaseRef.current = webglLease;
	webglLease.activate();
	term.options.macOptionClickForcesSelection = true;
	forceSelectionMode(term);
	confineDragSelectionToTerminalWidth(term);

	// xterm's viewport scrollbar follows macOS's system auto-hide preference
	// even when its WebKit pseudo-elements are styled. Keep the native viewport
	// hidden and mirror its normal-buffer geometry into a small app-owned thumb.
	// Like a native macOS overlay scrollbar, it appears while scrolling or
	// dragging and fades after the interaction goes idle.
	const scrollbarTrack = scrollbarTrackRef.current;
	const scrollbarThumb = scrollbarThumbRef.current;
	st.scrollbarFrame = null;
	st.scrollbarHideTimer = null;
	let scrollbarDrag: { pointerId: number; startLine: number; startY: number } | null = null;
	let scrollbarLastActive = 0;
	const hideScrollbarWhenIdle = () => {
		st.scrollbarHideTimer = null;
		if (!scrollbarTrack || scrollbarDrag) return;
		const idleFor = Date.now() - scrollbarLastActive;
		if (idleFor < MAC_TERMINAL_SCROLLBAR_IDLE_MS) {
			st.scrollbarHideTimer = window.setTimeout(hideScrollbarWhenIdle, MAC_TERMINAL_SCROLLBAR_IDLE_MS - idleFor);
			return;
		}
		scrollbarTrack.dataset.active = "false";
	};
	// Called once per frame while scrolling (streaming output fires a scroll
	// per line feed): keep it to a timestamp. The single hide timer re-arms
	// itself while activity continues instead of being cleared and re-created.
	const revealScrollbar = () => {
		if (!scrollbarTrack || scrollbarTrack.dataset.scrollable !== "true") return;
		scrollbarLastActive = Date.now();
		if (scrollbarTrack.dataset.active !== "true") scrollbarTrack.dataset.active = "true";
		if (scrollbarDrag || st.scrollbarHideTimer !== null) return;
		st.scrollbarHideTimer = window.setTimeout(hideScrollbarWhenIdle, MAC_TERMINAL_SCROLLBAR_IDLE_MS);
	};
	let revealAfterUpdate = false;
	const updateScrollbar = () => {
		st.scrollbarFrame = null;
		const reveal = revealAfterUpdate;
		revealAfterUpdate = false;
		if (!scrollbarTrack || !scrollbarThumb) return;
		const buffer = term.buffer.active;
		const maxScrollLine = buffer.type === "normal" ? buffer.baseY : 0;
		const trackHeight = scrollbarTrack.clientHeight;
		if (maxScrollLine <= 0 || trackHeight <= 0) {
			scrollbarTrack.dataset.scrollable = "false";
			scrollbarTrack.dataset.active = "false";
			return;
		}
		const totalLines = maxScrollLine + term.rows;
		const thumbHeight = Math.max(24, (trackHeight * term.rows) / totalLines);
		const travel = Math.max(0, trackHeight - thumbHeight);
		const thumbTop = travel * (buffer.viewportY / maxScrollLine);
		scrollbarThumb.style.height = `${thumbHeight}px`;
		scrollbarThumb.style.transform = `translateY(${thumbTop}px)`;
		scrollbarTrack.dataset.scrollable = "true";
		if (reveal) revealScrollbar();
	};
	const scheduleScrollbarUpdate = () => {
		if (!scrollbarTrack || st.scrollbarFrame !== null) return;
		st.scrollbarFrame = requestAnimationFrame(updateScrollbar);
	};
	const scrollPositionChange = scrollbarTrack
		? term.onScroll(() => {
			revealAfterUpdate = true;
			scheduleScrollbarUpdate();
		})
		: null;
	const scrollbarResize = scrollbarTrack ? term.onResize(scheduleScrollbarUpdate) : null;
	const scrollToPointer = (clientY: number) => {
		if (!scrollbarTrack || !scrollbarThumb) return;
		const maxScrollLine = term.buffer.active.type === "normal" ? term.buffer.active.baseY : 0;
		const trackRect = scrollbarTrack.getBoundingClientRect();
		const travel = trackRect.height - scrollbarThumb.getBoundingClientRect().height;
		if (maxScrollLine <= 0 || travel <= 0) return;
		const thumbTop = Math.min(travel, Math.max(0, clientY - trackRect.top - scrollbarThumb.offsetHeight / 2));
		term.scrollToLine(Math.round((thumbTop / travel) * maxScrollLine));
	};
	const scrollbarPointerDown = (event: PointerEvent) => {
		if (!scrollbarTrack || !scrollbarThumb || scrollbarTrack.dataset.scrollable !== "true") return;
		event.preventDefault();
		scrollbarTrack.setPointerCapture(event.pointerId);
		if (event.target !== scrollbarThumb) scrollToPointer(event.clientY);
		scrollbarDrag = {
			pointerId: event.pointerId,
			startLine: term.buffer.active.viewportY,
			startY: event.clientY,
		};
		revealScrollbar();
	};
	const scrollbarPointerMove = (event: PointerEvent) => {
		if (!scrollbarDrag || scrollbarDrag.pointerId !== event.pointerId || !scrollbarTrack || !scrollbarThumb) return;
		const maxScrollLine = term.buffer.active.type === "normal" ? term.buffer.active.baseY : 0;
		const travel = scrollbarTrack.clientHeight - scrollbarThumb.offsetHeight;
		if (maxScrollLine <= 0 || travel <= 0) return;
		const lineDelta = ((event.clientY - scrollbarDrag.startY) / travel) * maxScrollLine;
		term.scrollToLine(Math.round(scrollbarDrag.startLine + lineDelta));
	};
	const scrollbarPointerUp = (event: PointerEvent) => {
		if (!scrollbarDrag || scrollbarDrag.pointerId !== event.pointerId) return;
		scrollbarDrag = null;
		if (scrollbarTrack?.hasPointerCapture(event.pointerId)) scrollbarTrack.releasePointerCapture(event.pointerId);
		revealScrollbar();
	};
	scrollbarTrack?.addEventListener("pointerdown", scrollbarPointerDown);
	scrollbarTrack?.addEventListener("pointermove", scrollbarPointerMove);
	scrollbarTrack?.addEventListener("pointerup", scrollbarPointerUp);
	scrollbarTrack?.addEventListener("pointercancel", scrollbarPointerUp);
	scheduleScrollbarUpdate();

	return {
		scheduleScrollbarUpdate,
		scrollPositionChange,
		scrollbarPointerDown,
		scrollbarPointerMove,
		scrollbarPointerUp,
		scrollbarResize,
		scrollbarTrack,
		visibleContentRender,
		webglLease,
	};
}
