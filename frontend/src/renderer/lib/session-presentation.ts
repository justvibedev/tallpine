import {
	attentionZone,
	attentionZoneOrder,
	boardAttentionZoneOrder,
	boardKanbanColumnOrder,
	getAgentActivityView as getPortableAgentActivityView,
	getAttentionZoneView as getPortableAttentionZoneView,
	getAttentionZoneViewForZone as getPortableAttentionZoneViewForZone,
	getKanbanColumnView as getPortableKanbanColumnView,
	getSessionStatusView as getPortableSessionStatusView,
	getSessionTimelinePillView as getPortableSessionTimelinePillView,
	isAgentActivityWorking,
	isSessionIdle,
	isWaitingOnOrchestrator,
	sessionWantsAttention,
	type AgentActivityView,
	type AttentionZone,
	type AttentionZoneView,
	type KanbanColumn,
	type KanbanColumnView,
	type ProductUITranslator,
	type SessionStatusView,
	type SessionTimelinePillStatus,
	type SessionTimelinePillView,
} from "@tallpine/product-ui";
export { isWaitingOnOrchestrator, sessionWantsAttention };
import type { TFunction } from "i18next";
import { appI18n, type MessageKey } from "../i18n";
import type { SessionActivity, SessionStatus } from "../types/workspace";
import { formatUsageReset } from "@tallpine/shared/format/usage-reset";

function translator(t: TFunction): ProductUITranslator {
	return (key, values) => t(key as MessageKey, values);
}

export function getAgentActivityView(
	activity?: SessionActivity | null,
	t: TFunction = appI18n.t,
): AgentActivityView {
	return getPortableAgentActivityView(activity, translator(t));
}

/** The activity label of a session; a session paused on a usage limit says when it resumes. */
export function sessionActivityLabel(
	session: { activity?: SessionActivity | null; pausedUntil?: string },
	t: TFunction,
	locale: string,
): string {
	return session.pausedUntil
		? t("session.waitingForUsageReset", { time: formatUsageReset(session.pausedUntil, locale) })
		: getAgentActivityView(session.activity, t).label;
}

export function getSessionStatusView(
	status: SessionStatus,
	t: TFunction = appI18n.t,
): SessionStatusView {
	return getPortableSessionStatusView(status, translator(t));
}

export function getAttentionZoneView(
	status: SessionStatus,
	t: TFunction = appI18n.t,
): AttentionZoneView {
	return getPortableAttentionZoneView(status, translator(t));
}

export function getAttentionZoneViewForZone(
	zone: AttentionZone,
	t: TFunction = appI18n.t,
): AttentionZoneView {
	return getPortableAttentionZoneViewForZone(zone, translator(t));
}

/**
 * Why an idle-toned, not-working session is quiet: it waits on the owner
 * (review approved its current head and it has not landed, or the board holds
 * it in Ready), or on a timer or background task. Undefined means plain idle or
 * finished. Statuses that already colour the dot (approved, needs-input) never
 * reach this, since callers only ask for idle-toned sessions.
 */
export function getIdleReason(
	session: {
		backgroundTasks?: readonly unknown[];
		kanbanColumn?: string;
		landed?: boolean;
		pausedUntil?: string;
		reviewVerdict?: string;
	},
	now: number = Date.now(),
): "owner" | "timer" | undefined {
	if ((session.reviewVerdict === "approved" && !session.landed) || session.kanbanColumn === "ready") return "owner";
	if ((session.backgroundTasks?.length ?? 0) > 0) return "timer";
	const until = session.pausedUntil ? Date.parse(session.pausedUntil) : Number.NaN;
	if (until > now) return "timer";
	return undefined;
}

export type SessionStatusDotView = {
	className: string;
	breathe: boolean;
	/** The agent sleeps (its processes are stopped) and nothing more urgent claims the slot. */
	sleeping: boolean;
	/** Why a quiet (idle-toned) worker is quiet, when something other than plain idle explains it. */
	idleReason?: "owner" | "timer";
	/** Tooltip for the dot when its tone needs explaining (waiting on the orchestrator). */
	title?: string;
};

// The Action zone gathers everything that wants someone's eyes, but the dot
// keeps DESIGN.md's colour vocabulary so that only a failure or a wait on the
// human reads red: review feedback is the cool in-review teal, a session whose hooks
// never reported is neutral, and failed CI is the failure tone. Everything else
// in the zone (needs_input) keeps the needs-you tone.
const actionDotTones: Partial<Record<SessionStatus, string>> = {
	ci_failed: "bg-status-exited",
	changes_requested: "bg-status-in-review",
	commented: "bg-status-in-review",
	no_signal: "bg-status-unknown",
	unknown: "bg-status-unknown",
};

// The session dot carries two independent signals. Colour comes from the board
// section represented by the SCM state, which survives a running agent —
// `status` is activity-first, so it collapses to `working` the moment an agent
// wakes and would otherwise take every pull request tone with it. Merged keeps
// its split-section tone instead of sharing Ready to merge's tone.
//
// Motion stays on raw agent activity. A no-PR idle session is the exception to
// the preserved section colour: when its agent starts working it blinks blue.
export function getSessionStatusDotView(
	session: {
		activity?: SessionActivity | null;
		displayStatus?: string;
		backgroundTasks?: readonly unknown[];
		hibernatedAt?: string;
		kanbanColumn?: string;
		landed?: boolean;
		pausedUntil?: string;
		reviewVerdict?: string;
		needsInputAudience?: "orchestrator" | "human";
		scmStatus?: SessionStatus;
		status: SessionStatus;
	},
	t: TFunction = appI18n.t,
): SessionStatusDotView {
	const working = isAgentActivityWorking(session.activity);
	const closedWithoutMerge = session.displayStatus === "Closed without merge";
	const sectionStatus: SessionStatus =
		closedWithoutMerge ? "exited" : (session.scmStatus ?? session.status);
	const toneStatus = sectionStatus === "idle" && working ? "working" : sectionStatus;
	if (isWaitingOnOrchestrator(session)) {
		return {
			className: getSessionStatusView("idle", t).dotClassName,
			breathe: false,
			sleeping: false,
			title: t("chat.workerWaitingOnOrchestratorStatus"),
		};
	}
	const className =
		closedWithoutMerge
			? getSessionStatusView("exited", t).dotClassName
			: toneStatus === "exited"
				? "bg-passive"
				: toneStatus === "idle" || toneStatus === "merged"
					? getSessionStatusView(toneStatus, t).dotClassName
					: (actionDotTones[toneStatus] ?? getAttentionZoneView(toneStatus, t).dotClassName);

	const quiet = !working && toneStatus === "idle";
	const idleReason = quiet ? getIdleReason(session) : undefined;
	return {
		className,
		breathe: working,
		idleReason,
		sleeping: quiet && Boolean(session.hibernatedAt) && !idleReason,
	};
}

export function getKanbanColumnView(
	column: KanbanColumn,
	t: TFunction = appI18n.t,
): KanbanColumnView {
	return getPortableKanbanColumnView(column, translator(t));
}

export function getSessionTimelinePillView(
	status: SessionTimelinePillStatus,
	t: TFunction = appI18n.t,
): SessionTimelinePillView {
	return getPortableSessionTimelinePillView(status, translator(t));
}

/** Live labels for the current locale (getters re-resolve on each access). */
export const attentionZoneLabel: Record<AttentionZone, string> = {
	get merge() {
		return getAttentionZoneViewForZone("merge").label;
	},
	get action() {
		return getAttentionZoneViewForZone("action").label;
	},
	get pending() {
		return getAttentionZoneViewForZone("pending").label;
	},
	get working() {
		return getAttentionZoneViewForZone("working").label;
	},
	get done() {
		return getAttentionZoneViewForZone("done").label;
	},
};

export {
	attentionZone,
	attentionZoneOrder,
	boardAttentionZoneOrder,
	boardKanbanColumnOrder,
	isAgentActivityWorking,
	isSessionIdle,
};
export type {
	AgentActivityView,
	AttentionZone,
	AttentionZoneView,
	KanbanColumnView,
	SessionStatusView,
	SessionTimelinePillStatus,
	SessionTimelinePillView,
};
