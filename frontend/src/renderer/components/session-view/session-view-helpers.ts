/** Constants and pure helpers for SessionView. */
import { type CSSProperties } from "react";
import { defaultShortcutBindings, shortcutBindingLabel } from "../../../shared/shortcuts";
import { isMacPlatform } from "../../lib/platform";
import { type TerminalTarget } from "../../types/terminal";
import { inspectorIsOpen, useUiStore, type InspectorView } from "../../stores/ui-store";
import { ReviewsResponse, ReviewerSurface, ReviewerTerminalTarget, ReviewerChatTarget } from "./session-view-types";

export const EMPTY_AUXILIARY_TAB_ORDER: string[] = [];
// Centre-file open requests take keys from this process-wide counter, not a
// per-mount one: the display mode remembered for a request (ui-store) outlives a
// SessionView remount, so a new request must never reuse a recorded key.
let lastCenterFileRequestKey = 0;
export function nextCenterFileRequestKey(): number {
	lastCenterFileRequestKey += 1;
	return lastCenterFileRequestKey;
}
// The inspector tab labels respond to the tablist's remaining width. The
// 239px tablist breakpoint plus the 76px pinned-action reserve and 10px leading
// inset gives a 325px inspector breakpoint for the animation lock.
export const INSPECTOR_COMPACT_MAX_PX = 325;
export const TOPBAR_SECONDARY_COMPACT_MAX_PX = 759;
export const isMac = isMacPlatform();
export const noDragStyle = isMac ? ({ WebkitAppRegion: "no-drag" } as CSSProperties) : undefined;
export const newTerminalShortcutLabel = shortcutBindingLabel(defaultShortcutBindings("new-shell-terminal", isMac)[0], isMac);

export function topbarSecondaryLabelMode(width: number): "compact" | "expanded" {
	return width <= TOPBAR_SECONDARY_COMPACT_MAX_PX ? "compact" : "expanded";
}

export function previewRevealKey(previewUrl?: string, previewRevision?: number): string {
	const target = previewUrl?.trim();
	if (!target) return "";
	if (typeof previewRevision === "number") return `revision:${previewRevision}`;
	return `url:${target}`;
}

export const BROWSER_REVEAL_ACTIONS = new Set(["open", "tab-new"]);

/** `shownView` is the tab on screen: an orchestrator derives it, so the stored view can lag behind. */
export function browserIsVisible(sessionId: string, browserPoppedOut: boolean, shownView: InspectorView): boolean {
	if (browserPoppedOut) return true;
	return inspectorIsOpen(useUiStore.getState().inspectorSessions, sessionId) && shownView === "browser";
}

// workingReviewerSurface is the live reviewer to show when the selected one is
// idle but another is working (an agent asked a different reviewer). The
// selected reviewer fields keep meaning the session's selected reviewer.
export function workingReviewerSurface(data?: ReviewsResponse): ReviewerSurface | undefined {
	const active = data?.activeReviewers ?? [];
	if (active.some((surface) => surface.reviewId === data?.reviewerSurface?.reviewId)) return undefined;
	return active[0];
}

export function reviewerTerminalFromReviews(data?: ReviewsResponse, selected?: TerminalTarget): ReviewerTerminalTarget | undefined {
	// Several reviewers can run on one worker at once. The reviewer tab follows
	// whichever live reviewer the user opened (from the inspector), and
	// otherwise shows the selected reviewer, or the working one when it is idle.
	if (selected?.kind === "reviewer") {
		const opened = data?.activeReviewers?.find((surface) => surface.mode !== "chat" && surface.handleId === selected.handleId);
		if (opened?.handleId) return { handleId: opened.handleId, harness: opened.harness || selected.harness };
	}
	const working = workingReviewerSurface(data);
	if (working) return working.mode !== "chat" && working.handleId ? { handleId: working.handleId, harness: working.harness || "codex" } : undefined;
	if (data?.reviewerSurface?.mode === "chat") return undefined;
	const handleId = data?.reviewerHandleId?.trim();
	if (!handleId) return undefined;
	const latest = data?.reviews?.find((review) => review.latestRun)?.latestRun;
	return { handleId, harness: data?.reviewerHarness || latest?.harness || "codex" };
}

export function reviewerChatFromReviews(data?: ReviewsResponse): ReviewerChatTarget | undefined {
	const surface = workingReviewerSurface(data) ?? data?.reviewerSurface;
	if (surface?.mode !== "chat" || !surface.reviewId) return undefined;
	return { reviewId: surface.reviewId, harness: surface.harness || "codex" };
}

