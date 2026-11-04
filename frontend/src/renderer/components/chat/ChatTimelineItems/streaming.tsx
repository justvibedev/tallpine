import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { type ConversationMessage } from "../../../types/conversation";

/**
 * Each update reveals the share of the backlog that decays over STREAM_SETTLE_MS, so
 * bursts of provider text spread across the gap before the next one instead of landing
 * at once. Updates are capped at ~30 Hz: every one re-parses the message's markdown.
 */
const STREAM_SETTLE_MS = 140;

const STREAM_MIN_FRAME_MS = 32;

const STREAM_MAX_FRAME_DELTA_MS = 100;

// No frame for this long means the tab was occluded or throttled: nobody watched the
// animation, so show the current text instead of replaying it.
const STREAM_STALL_MS = 1000;

const STREAM_GRAPHEME_SEGMENTER = new Intl.Segmenter(undefined, { granularity: "grapheme" });

function streamGraphemes(text: string): string[] {
	return Array.from(STREAM_GRAPHEME_SEGMENTER.segment(text), ({ segment }) => segment);
}

function reconciledStreamPrefix(visibleText: string, targetGraphemes: string[]) {
	let boundary = 0;
	let count = 0;
	for (const grapheme of targetGraphemes) {
		const nextBoundary = boundary + grapheme.length;
		if (nextBoundary > visibleText.length) break;
		boundary = nextBoundary;
		count++;
	}
	return { text: visibleText.slice(0, boundary), count };
}

export function useSmoothStreamingText(message: ConversationMessage): string {
	// A snapshot can first reach the renderer after the provider has already emitted
	// text. Keep that first durable burst visible; only later deltas need smoothing.
	const [visibleText, setVisibleText] = useState(() => message.text);
	const visibleRef = useRef(visibleText);
	const targetRef = useRef(message.text);
	const targetGraphemes = useMemo(
		() => message.streaming ? streamGraphemes(message.text) : [],
		[message.text, message.streaming],
	);
	const visibleGraphemeCountRef = useRef(targetGraphemes.length);
	const targetGraphemesRef = useRef(targetGraphemes);
	const messageIdRef = useRef(message.id);
	const frameRef = useRef<number | undefined>(undefined);
	const lastFrameAtRef = useRef<number | undefined>(undefined);
	const [reducedMotion, setReducedMotion] = useState(
		() => typeof window !== "undefined" && window.matchMedia("(prefers-reduced-motion: reduce)").matches,
	);

	useEffect(() => {
		const mediaQuery = window.matchMedia("(prefers-reduced-motion: reduce)");
		const update = () => setReducedMotion(mediaQuery.matches);
		mediaQuery.addEventListener("change", update);
		return () => mediaQuery.removeEventListener("change", update);
	}, []);

	const cancelDrain = useCallback(() => {
		if (frameRef.current !== undefined) {
			window.cancelAnimationFrame(frameRef.current);
			frameRef.current = undefined;
		}
		lastFrameAtRef.current = undefined;
	}, []);

	const scheduleDrain = useCallback(() => {
		if (frameRef.current !== undefined) return;
		const scheduledAt = performance.now();

		const tick = (now: number) => {
			frameRef.current = undefined;
			const target = targetGraphemesRef.current;
			const currentCount = visibleGraphemeCountRef.current;
			const backlog = target.length - currentCount;
			if (backlog <= 0) {
				lastFrameAtRef.current = undefined;
				return;
			}

			if (document.hidden || now - (lastFrameAtRef.current ?? scheduledAt) > STREAM_STALL_MS) {
				visibleRef.current = targetRef.current;
				visibleGraphemeCountRef.current = target.length;
				setVisibleText(targetRef.current);
				cancelDrain();
				return;
			}

			const previousFrameAt = lastFrameAtRef.current;
			if (previousFrameAt !== undefined && now - previousFrameAt < STREAM_MIN_FRAME_MS) {
				frameRef.current = window.requestAnimationFrame(tick);
				return;
			}
			lastFrameAtRef.current = now;
			const elapsedMs = Math.min(STREAM_MAX_FRAME_DELTA_MS, previousFrameAt === undefined ? STREAM_MIN_FRAME_MS : now - previousFrameAt);
			const count = Math.ceil(backlog * (1 - Math.exp(-elapsedMs / STREAM_SETTLE_MS)));
			const nextCount = Math.min(target.length, currentCount + count);
			const next = visibleRef.current + target.slice(currentCount, nextCount).join("");
			visibleRef.current = next;
			visibleGraphemeCountRef.current = nextCount;
			setVisibleText(next);
			if (nextCount < target.length) frameRef.current = window.requestAnimationFrame(tick);
			else lastFrameAtRef.current = undefined;
		};

		frameRef.current = window.requestAnimationFrame(tick);
	}, [cancelDrain]);

	useEffect(() => {
		if (message.id !== messageIdRef.current) {
			cancelDrain();
			messageIdRef.current = message.id;
			targetRef.current = message.text;
			targetGraphemesRef.current = targetGraphemes;
			const initial = message.text;
			visibleRef.current = initial;
			visibleGraphemeCountRef.current = targetGraphemes.length;
			setVisibleText(initial);
			return;
		}

		targetRef.current = message.text;
		targetGraphemesRef.current = targetGraphemes;
		if (!message.streaming || reducedMotion) {
			cancelDrain();
			visibleRef.current = message.text;
			visibleGraphemeCountRef.current = targetGraphemes.length;
			setVisibleText(message.text);
			return;
		}
		// A provider correction or rollback can replace the current prefix. In that
		// case the durable snapshot is authoritative and should be shown immediately.
		if (!message.text.startsWith(visibleRef.current)) {
			cancelDrain();
			visibleRef.current = message.text;
			visibleGraphemeCountRef.current = targetGraphemes.length;
			setVisibleText(message.text);
			return;
		}
		// A later combining mark or ZWJ can merge the last visible grapheme into a
		// different target grapheme. Reconcile that trailing fragment before using
		// the old grapheme count, otherwise the drain can skip the merged suffix.
		const reconciled = reconciledStreamPrefix(visibleRef.current, targetGraphemesRef.current);
		visibleGraphemeCountRef.current = reconciled.count;
		if (reconciled.text !== visibleRef.current) {
			visibleRef.current = reconciled.text;
			setVisibleText(reconciled.text);
		}
		if (visibleGraphemeCountRef.current < targetGraphemesRef.current.length) scheduleDrain();
	}, [cancelDrain, message.id, message.text, message.streaming, reducedMotion, scheduleDrain, targetGraphemes]);

	useEffect(
		() => cancelDrain,
		[cancelDrain],
	);

	return visibleText;
}
