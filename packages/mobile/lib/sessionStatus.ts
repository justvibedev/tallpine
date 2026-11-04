// Derived session status, split out of api.ts so pure view-model modules can
// use it — api.ts chains through config.ts into AsyncStorage and react-native,
// so nothing importing a value from it is unit-testable.
//
// Re-exported from api.ts, so existing call sites are unchanged.
import type { DashboardSession } from "./api";
import { toSessionStatus, type SessionStatus } from "@tallpine/product-ui/session-models";
import {
	attentionZone,
	phoneAttentionBucket,
	sessionWantsAttention,
} from "@tallpine/product-ui/session-presentation";
import type { AttentionLevel } from "./theme";

// Same normalisation as desktop: a missing or unknown wire status is "unknown" (an action).
function statusOf(s: DashboardSession): SessionStatus {
	return s.isTerminated === true ? "terminated" : toSessionStatus(s.status ?? undefined);
}

// One rule: the desktop's attentionZone, with the phone's respond/review split
// as a thin mapping over it (product-ui phoneAttentionBucket).
export function attentionOf(s: DashboardSession): AttentionLevel {
	const model = { status: statusOf(s), needsInputAudience: s.needsInputAudience };
	return phoneAttentionBucket(attentionZone(model), model);
}

/** Same sessions the desktop badge counts: merge-ready or needing the human. */
export function wantsAttention(s: DashboardSession): boolean {
	return sessionWantsAttention({
		status: statusOf(s),
		needsInputAudience: s.needsInputAudience,
		isTerminated: s.isTerminated,
	});
}

// Matches desktop's `session.displayName ?? session.issueId ?? session.id`
// (useWorkspaceQuery.ts). `issueId` is the rung mobile was missing: the daemon
// stores the task name there — it is what desktop's New task dialog sends as its
// Title — so without it a named session still rendered as its own id, and the
// same session read differently in the two apps.
//
// issueTitle/userPrompt/summary are hardcoded null by mapSession today; they
// stay for forward compatibility, between the two rungs that actually fire.
//
// The candidates are filtered on trimmed content rather than truthiness. A
// displayName of " " is truthy, so a `||` chain stopped there and rendered a
// card with no title at all — and the id fallback, the one value that is always
// present, was never reached. Whitespace is not a name.
export function sessionTitle(s: DashboardSession): string {
	const candidates = [s.displayName, s.issueId, s.issueTitle, s.userPrompt, s.summary];
	for (const c of candidates) {
		const trimmed = c?.trim();
		if (trimmed) return trimmed;
	}
	// `id` is server-assigned and never blank, so this terminates. Trimmed anyway
	// so the return type's promise ("a non-empty string") holds unconditionally.
	return s.id.trim() || s.id;
}
