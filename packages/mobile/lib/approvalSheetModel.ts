import { pendingApproval, type ConversationSnapshot, type DecisionOption } from "./chat/types";

export type ApprovalSheetModel = {
	requestId: string;
	/** What will actually run, verbatim: the raw command when the daemon sent one. */
	command: string;
	cwd?: string;
	reason?: string;
	decisions: DecisionOption[];
};

/**
 * What the approve/deny sheet shows for a session's open permission prompt, or
 * null when there is nothing the sheet can answer (no prompt, no request id, or
 * no decisions) - the caller then opens the session instead.
 */
export function approvalSheetModel(snapshot: ConversationSnapshot | undefined): ApprovalSheetModel | null {
	if (!snapshot) return null;
	const approval = pendingApproval(snapshot);
	const decisions = approval?.decisions ?? [];
	if (!approval || !approval.requestId || decisions.length === 0) return null;
	const command = (approval.detail?.rawCommand || approval.detail?.command || approval.summary || "").trim();
	if (!command) return null;
	return {
		requestId: approval.requestId,
		command,
		cwd: approval.detail?.cwd || undefined,
		reason: approval.detail?.reason?.trim() || undefined,
		decisions,
	};
}

/** Where a needs-input tap goes: the approval sheet, which falls back to the session itself. */
export function approvalSheetHref(target: string): string | null {
	const match = /^\/session\/([^/?]+)\?hostId=([^&]+)$/.exec(target);
	return match ? `/sheets/approval?sessionId=${match[1]}&hostId=${match[2]}` : null;
}

/**
 * Whether the sheet should give up and open the session. A machine that has not
 * hydrated yet (a cold-start push tap lands before its config exists) is still
 * loading, not empty: redirecting then would skip the sheet on the very path it
 * exists for. Only a finished lookup with nothing to answer, a host that never
 * showed up, or a machine that finished loading without a config redirects.
 */
export function approvalSheetShouldOpenSession(state: {
	/** undefined while the prompt is still being fetched, null when there is none. */
	model: ApprovalSheetModel | null | undefined;
	hasConfig: boolean;
	/** The machine is in the store and still loading. */
	hostLoading: boolean;
	/** The machine is in the store at all. */
	hostKnown: boolean;
	/** The wait for the machine to appear has run out. */
	waitedOut: boolean;
}): boolean {
	if (state.model === null) return true;
	if (state.hasConfig) return false;
	if (state.hostKnown) return !state.hostLoading;
	return state.waitedOut;
}
