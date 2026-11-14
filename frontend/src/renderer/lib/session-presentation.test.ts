import { describe, expect, it } from "vitest";
import {
	attentionZone,
	getAgentActivityView,
	getAttentionZoneView,
	getSessionStatusDotView,
	getSessionStatusView,
	getSessionTimelinePillView,
	isAgentActivityWorking,
	isSessionIdle,
} from "./session-presentation";
import { sessionNeedsAttention, type WorkspaceSession } from "../types/workspace";

function sessionWith(overrides: Partial<WorkspaceSession>): WorkspaceSession {
	return {
		id: "sess-1",
		workspaceId: "ws-1",
		workspaceName: "my-app",
		title: "fix-bug",
		provider: "claude-code",
		branch: "feat/x",
		status: "working",
		updatedAt: "2026-01-01T00:00:00Z",
		prs: [],
		...overrides,
	};
}

const openPr: WorkspaceSession["prs"][number] = {
	number: 7,
	url: "https://github.com/acme/app/pull/7",
	state: "open",
	ci: "unknown",
	review: "none",
	mergeability: "unknown",
	reviewComments: false,
	updatedAt: "2026-01-01T00:00:00Z",
};

describe("session presentation", () => {
	it.each([
		["active", "Working", true, "bg-status-working animate-status-pulse"],
		["idle", "Idle", false, "bg-status-idle"],
		["waiting_input", "Input Needed", false, "bg-status-needs-you-dot"],
		["blocked", "Awaiting Decision", false, "bg-status-needs-you-dot"],
		["exited", "Exited", false, "bg-status-exited"],
		["unknown", "Unknown", false, "bg-status-unknown"],
	] as const)("maps %s agent activity to %s", (state, label, breathe, indicatorClassName) => {
		expect(getAgentActivityView({ state, lastActivityAt: "" })).toMatchObject({
			label,
			breathe,
			indicatorClassName,
		});
	});

	it("uses raw agent activity, not session status, for working indicators", () => {
		expect(isAgentActivityWorking({ state: "active", lastActivityAt: "" })).toBe(true);
		expect(isAgentActivityWorking({ state: "idle", lastActivityAt: "" })).toBe(false);
		expect(isAgentActivityWorking(undefined)).toBe(false);
	});

	it.each([
		["working", "Working"],
		["idle", "Idle"],
		["needs_input", "Input needed"],
		["no_signal", "No signal"],
		["ci_failed", "CI failed"],
		["changes_requested", "Changes requested"],
		["review_pending", "Review pending"],
		["draft", "Draft PR"],
		["pr_open", "PR open"],
		["approved", "Approved"],
		["mergeable", "Ready"],
		["merged", "Merged"],
		["exited", "Exited"],
		["terminated", "Terminated"],
		["unknown", "Unknown status"],
	] as const)("maps %s session status to %s", (status, label) => {
		expect(getSessionStatusView(status).label).toBe(label);
	});

	it("uses distinct session-card tones for idle, no signal, and PR waiting states", () => {
		expect(getSessionStatusView("idle").className).toBe("text-status-idle");
		expect(getSessionStatusView("no_signal").className).toBe("text-status-unknown");
		expect(getSessionStatusView("draft").className).toBe("text-status-in-review");
		expect(getSessionStatusView("pr_open").className).toBe("text-status-in-review");
		expect(getSessionStatusView("review_pending").className).toBe("text-status-in-review");
		expect(getSessionStatusView("exited").className).toBe("text-status-exited");
	});

	it.each([
		["approved", "merge", "Ready to merge"],
		["mergeable", "merge", "Ready to merge"],
		["needs_input", "action", "Needs you"],
		["exited", "action", "Needs you"],
		["no_signal", "action", "Needs you"],
		["ci_failed", "action", "Needs you"],
		["changes_requested", "action", "Needs you"],
		["unknown", "action", "Needs you"],
		["review_pending", "pending", "In review"],
		["pr_open", "pending", "In review"],
		["draft", "pending", "In review"],
		["working", "working", "Working"],
		["idle", "working", "Working"],
		["merged", "merge", "Ready to merge"],
		["terminated", "done", "Terminated"],
	] as const)("maps %s to the %s attention zone", (status, zone, label) => {
		expect(attentionZone(sessionWith({ status }))).toBe(zone);
		expect(getAttentionZoneView(status)).toMatchObject({ zone, label });
	});

	it.each([
		["idle", "bg-status-idle"],
		["working", "bg-status-working"],
		["needs_input", "bg-status-needs-you-dot"],
		["exited", "bg-passive"],
		["no_signal", "bg-status-unknown"],
		["ci_failed", "bg-status-exited"],
		["changes_requested", "bg-status-in-review"],
		["commented", "bg-status-in-review"],
		["unknown", "bg-status-unknown"],
		["draft", "bg-status-in-review"],
		["pr_open", "bg-status-in-review"],
		["review_pending", "bg-status-in-review"],
		["approved", "bg-status-ready"],
		["mergeable", "bg-status-ready"],
		["merged", "bg-status-merged"],
		["terminated", "bg-status-terminated"],
	] as const)("paints the %s session dot with its board-section tone", (status, dotClassName) => {
		expect(getSessionStatusDotView(sessionWith({ status }))).toMatchObject({ className: dotClassName });
	});

	it("paints a worker waiting on the live orchestrator neutral, without pulse, with an explaining tooltip", () => {
		const dot = getSessionStatusDotView(sessionWith({ status: "needs_input", needsInputAudience: "orchestrator" }));
		expect(dot).toMatchObject({ className: "bg-status-idle", breathe: false, sleeping: false, title: "Waiting on orchestrator" });
	});

	it("keeps the needs-you tone for human-directed and unknown-audience waits", () => {
		for (const needsInputAudience of ["human", undefined] as const) {
			const dot = getSessionStatusDotView(sessionWith({ status: "needs_input", needsInputAudience }));
			expect(dot).toMatchObject({ className: "bg-status-needs-you-dot" });
			expect(dot.title).toBeUndefined();
		}
	});

	it("does not count an orchestrator-addressed wait as needing attention", () => {
		expect(sessionNeedsAttention(sessionWith({ status: "needs_input", needsInputAudience: "orchestrator" }))).toBe(false);
		expect(sessionNeedsAttention(sessionWith({ status: "needs_input", needsInputAudience: "human" }))).toBe(true);
	});

	it("shows a sleeping agent in the idle slot only when nothing more urgent claims it", () => {
		const hibernatedAt = "2026-10-25T20:00:00Z";
		expect(getSessionStatusDotView(sessionWith({ status: "idle", hibernatedAt }))).toMatchObject({ sleeping: true });
		// A PR state keeps its tone; a working agent is not asleep.
		expect(getSessionStatusDotView(sessionWith({ status: "idle", scmStatus: "ci_failed", hibernatedAt }))).toMatchObject({ sleeping: false });
		expect(
			getSessionStatusDotView(sessionWith({ status: "working", hibernatedAt, activity: { state: "active", lastActivityAt: "" } })),
		).toMatchObject({ sleeping: false });
		expect(getSessionStatusDotView(sessionWith({ status: "idle" }))).toMatchObject({ sleeping: false });
	});

	it("names why a quiet worker is idle: owner, timer, or plain idle", () => {
		const hibernatedAt = "2026-10-25T20:00:00Z";
		// Approved local stack (no PR) waiting to land: owner, not the moon.
		expect(getSessionStatusDotView(sessionWith({ status: "idle", kanbanColumn: "ready", hibernatedAt }))).toMatchObject({ idleReason: "owner", sleeping: false });
		expect(getSessionStatusDotView(sessionWith({ status: "idle", backgroundTasks: [{ id: "t", name: "wait" }], hibernatedAt }))).toMatchObject({ idleReason: "timer", sleeping: false });
		expect(getSessionStatusDotView(sessionWith({ status: "idle", pausedUntil: "2999-01-01T00:00:00Z" }))).toMatchObject({ idleReason: "timer" });
		expect(getSessionStatusDotView(sessionWith({ status: "idle", pausedUntil: "2000-01-01T00:00:00Z" })).idleReason).toBeUndefined();
		expect(getSessionStatusDotView(sessionWith({ status: "idle", hibernatedAt }))).toMatchObject({ idleReason: undefined, sleeping: true });
		// Approved branch (local stack, no PR) that has not landed: waiting on the owner.
		expect(getSessionStatusDotView(sessionWith({ status: "idle", reviewVerdict: "approved", hibernatedAt }))).toMatchObject({ idleReason: "owner", sleeping: false });
		// Once landed, or when the review asked for changes, it is not the owner's move.
		expect(getSessionStatusDotView(sessionWith({ status: "idle", reviewVerdict: "approved", landed: true, hibernatedAt })).idleReason).toBeUndefined();
		expect(getSessionStatusDotView(sessionWith({ status: "idle", reviewVerdict: "changes_requested", hibernatedAt })).idleReason).toBeUndefined();
		// Working agents never carry an idle reason.
		expect(getSessionStatusDotView(sessionWith({ status: "working", kanbanColumn: "ready", activity: { state: "active", lastActivityAt: "" } })).idleReason).toBeUndefined();
	});

	it("prefers SCM state over runtime status for the dot tone", () => {
		// A running agent drives status to `working`, which would erase every PR
		// tone in the sidebar. SCM state wins so the row keeps saying "merged".
		const merged = sessionWith({
			status: "working",
			scmStatus: "merged",
			activity: { state: "active", lastActivityAt: "" },
		});

		expect(getSessionStatusDotView(merged)).toEqual({ className: "bg-status-merged", breathe: true, sleeping: false });
	});

	it("keeps board-section color while raw working activity starts the motion", () => {
		const scmStatus = "mergeable" as const;

		expect(
			getSessionStatusDotView(sessionWith({ status: "idle", scmStatus, activity: { state: "idle", lastActivityAt: "" } })),
		).toEqual({ className: "bg-status-ready", breathe: false, sleeping: false });
		expect(
			getSessionStatusDotView(
				sessionWith({ status: "working", scmStatus, activity: { state: "active", lastActivityAt: "" } }),
			),
		).toEqual({ className: "bg-status-ready", breathe: true, sleeping: false });
	});

	it("uses a blinking blue dot when an idle-section session starts working", () => {
		expect(
			getSessionStatusDotView(
				sessionWith({ status: "idle", activity: { state: "active", lastActivityAt: "" } }),
			),
		).toEqual({ className: "bg-status-working", breathe: true, sleeping: false });
	});

	it("paints a closed-without-merge session red", () => {
		expect(
			getSessionStatusDotView(
				sessionWith({ status: "idle", displayStatus: "Closed without merge" }),
			),
		).toEqual({ className: "bg-status-exited", breathe: false, sleeping: false });
	});

	it("keeps activity indicator color independent from PR and CI presentation", () => {
		const active = getAgentActivityView({ state: "active", lastActivityAt: "" });
		const idle = getAgentActivityView({ state: "idle", lastActivityAt: "" });

		expect(active.indicatorClassName).toBe("bg-status-working animate-status-pulse");
		expect(idle.indicatorClassName).toBe("bg-status-idle");
	});

	it("uses a muted accent treatment for In Review instead of idle gray", () => {
		expect(getAttentionZoneView("review_pending")).toMatchObject({
			dot: "var(--color-status-in-review)",
			titleClassName: "text-status-in-review",
			dotClassName: "bg-status-in-review",
		});
	});

	it("classifies only backend-derived idle sessions for the work lane", () => {
		expect(isSessionIdle(sessionWith({ status: "idle" }))).toBe(true);
		expect(
			isSessionIdle(
				sessionWith({
					status: "idle",
					activity: { state: "active", lastActivityAt: "" },
					prs: [openPr],
				}),
			),
		).toBe(true);
		expect(
			isSessionIdle(
				sessionWith({
					status: "working",
					activity: { state: "idle", lastActivityAt: "" },
					prs: [openPr],
				}),
			),
		).toBe(false);
		expect(
			isSessionIdle(
				sessionWith({
					status: "working",
					activity: { state: "active", lastActivityAt: "" },
				}),
			),
		).toBe(false);
		expect(isSessionIdle(sessionWith({ status: "working" }))).toBe(false);
	});

	it.each([
		["no_signal", "No Signal", "var(--color-status-unknown)"],
		["ci_failed", "CI Failed", "var(--color-status-exited)"],
		["changes_requested", "Changes Requested", "var(--color-status-needs-you)"],
	] as const)("centralizes the %s timeline pill", (status, label, tone) => {
		expect(getSessionTimelinePillView(status)).toMatchObject({ label, tone, breathe: false });
	});
});
