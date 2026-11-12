/** Maps the daemon conversation wire shape onto the view model. */

import { type InfiniteData } from "@tanstack/react-query";
import type { AutoCompactChoice } from "../../lib/auto-compact";
import type { ActivityKind, ApprovalMode, ActivityStatus, ConversationActivity, ConversationItem, ConversationMessage, ConversationSnapshot, ControllerState, DecisionOption, DiffStatus, McpServer, MessageOrigin, MessageRole, PlanStep, PlanStepStatus, SessionMode, ThreadStatus, TurnState } from "../../types/conversation";

/**
 * Merge messages and activities into one ordered timeline.
 *
 * They are separate tables because they have different write patterns, but the
 * reader sees one sequence — which is why sequence is conversation-scoped rather
 * than per-table.
 */
export function toSnapshot(wire: WireSnapshot): ConversationSnapshot {
	const items: ConversationItem[] = [
		...(wire.messages ?? []).map(toMessage),
		...(wire.activities ?? []).map(toActivity),
	].sort((a, b) => a.sequence - b.sequence);

	return {
		continueTurnId: wire.continueTurnId,
		conversationId: wire.conversationId,
		sessionId: wire.sessionId,
		harness: wire.harness ?? "",
		mode: wire.mode as SessionMode,
		controller: { state: wire.controller as ControllerState },
		latestSequence: wire.latestSequence,
		oldestSequence: wire.oldestSequence ?? wire.latestSequence + 1,
		hasMoreBefore: wire.hasMoreBefore ?? false,
		nativeForkAvailableAfterSequence: wire.nativeForkAvailableAfterSequence ?? 0,
		settings: {
			model: wire.settings?.model || undefined,
			reasoningEffort: wire.settings?.reasoningEffort || undefined,
			approvalMode: (wire.settings?.approvalMode as ApprovalMode | undefined) || undefined,
			fastMode: wire.settings?.fastMode || undefined,
			planMode: wire.settings?.planMode || undefined,
			autoCompactWindow: (wire.settings?.autoCompactWindow || undefined) as AutoCompactChoice | undefined,
			autoCompactPending: wire.settings?.autoCompactPending || undefined,
			autoCompactProjectTokens: wire.settings?.autoCompactProjectTokens || undefined,
			contextCapacity: wire.settings?.contextCapacity || undefined,
			compactThreshold: wire.settings?.compactThreshold || undefined,
			modelLabel: wire.settings?.modelLabel || undefined,
		},
		// Absent means the provider has not reported, which the meter renders as
		// nothing rather than as an empty bar.
		usage: wire.usage ? { ...wire.usage } : undefined,
		rateLimits: wire.rateLimits ? { ...wire.rateLimits } : undefined,
		compactedAt: wire.compactedAt ?? undefined,
		title: wire.title || undefined,
		// What actually answered, as opposed to what was asked for. Kept separate from
		// `settings` all the way through, because collapsing them would lose exactly the
		// fact worth showing.
		modelReroute: wire.modelReroute
			? {
					fromModel: wire.modelReroute.fromModel || undefined,
					toModel: wire.modelReroute.toModel,
					reason: wire.modelReroute.reason || undefined,
					providerTurnId: wire.modelReroute.providerTurnId || undefined,
					at: wire.modelReroute.at,
				}
			: undefined,
		account: wire.account
			? {
					authMode: wire.account.authMode || undefined,
					planLabel: wire.account.planLabel || undefined,
					authenticationState: wire.account.authenticationState,
						authVerifiedAt: wire.account.authVerifiedAt ?? undefined,
						lastAuthFailureAt: wire.account.lastAuthFailureAt ?? undefined,
						lastAuthFailureReason: wire.account.lastAuthFailureReason || undefined,
						authFailureId: wire.account.authFailureId || undefined,
						reauthRequiredAt: wire.account.reauthRequiredAt ?? undefined,
					reauthReason: wire.account.reauthReason || undefined,
				}
			: undefined,
		// The provider's lifecycle view, kept apart from `controller` on purpose: they
		// answer different questions and routinely disagree.
		threadState: wire.threadState
			? {
					status: (wire.threadState.status as ThreadStatus | undefined) || undefined,
					waitingOn: wire.threadState.waitingOn?.length ? wire.threadState.waitingOn : undefined,
					archivedAt: wire.threadState.archivedAt ?? undefined,
					closedAt: wire.threadState.closedAt ?? undefined,
					compaction: wire.threadState.compaction
						? {
								status: wire.threadState.compaction.status,
								trigger: (wire.threadState.compaction.trigger as "auto" | "manual" | undefined) || undefined,
								preTokens: wire.threadState.compaction.preTokens || undefined,
								postTokens: wire.threadState.compaction.postTokens || undefined,
								at: wire.threadState.compaction.at,
							}
						: undefined,
				}
			: undefined,
		mcpServers: wire.mcpServers?.length
			? wire.mcpServers.map(
					(server): McpServer => ({
						name: server.name,
						status: server.status as McpServer["status"],
						error: server.error || undefined,
						failureReason: server.failureReason || undefined,
					}),
				)
			: undefined,
		capabilities: wire.capabilities?.length ? wire.capabilities : undefined,
		activeBranchId: wire.activeBranchId || undefined,
		branchedFromEarlierMessage: wire.branchedFromEarlierMessage ?? undefined,
		branchMaterialization: wire.branchMaterialization
			? {
					strategy: wire.branchMaterialization.strategy,
					replayTruncated: wire.branchMaterialization.replayTruncated,
				}
			: undefined,
		branchPoints: (wire.branchPoints ?? []).map((point) => ({
			turnId: point.turnId,
			position: point.position,
			total: point.total,
			previousBranchId: point.previousBranchId || undefined,
			nextBranchId: point.nextBranchId || undefined,
		})),
		turns: (wire.turns ?? []).map((turn) => ({
			id: turn.id,
			state: turn.state as TurnState,
			providerTurnId: turn.providerTurnId,
			retryOfTurnId: turn.retryOfTurnId,
			hasRetryAttempt: turn.hasRetryAttempt,
			hasHumanPrompt: turn.hasHumanPrompt,
			errorMessage: turn.errorMessage,
			usageLimit: turn.usageLimit ? {
				resetsAt: turn.usageLimit.resetsAt ?? undefined,
				autoResume: turn.usageLimit.autoResume,
				resumeError: turn.usageLimit.resumeError,
			} : undefined,
			transientError: turn.transientError,
			autoRetry: turn.autoRetry
				? { attempt: turn.autoRetry.attempt, maxAttempts: turn.autoRetry.maxAttempts, nextAt: turn.autoRetry.nextAt, offline: turn.autoRetry.offline }
				: undefined,
			requestedAt: turn.requestedAt,
			startedAt: turn.startedAt ?? undefined,
			completedAt: turn.completedAt ?? undefined,
			// Left undefined when the daemon reported none, so the surface can tell
			// "this turn changed nothing" from "this agent does not report diffs".
			diff: turn.diff
				? {
						files: (turn.diff.files ?? []).map((file) => ({
							path: file.path,
							additions: file.additions,
							deletions: file.deletions,
							status: file.status as DiffStatus,
							oldPath: file.oldPath || undefined,
						})),
						truncated: turn.diff.truncated,
					}
				: undefined,
			// Current state of the turn rather than history: the provider re-sends the
			// whole plan on every revision and the daemon overwrites this, which is why
			// the surface reads it here instead of walking the timeline for plan rows.
			plan: turn.plan
				? {
						explanation: turn.plan.explanation || undefined,
						steps: (turn.plan.steps ?? []).map(
							(step): PlanStep => ({
								text: step.text,
								status: step.status as PlanStepStatus,
							}),
						),
						spansTurns: turn.plan.spansTurns || undefined,
					}
				: undefined,
			rolledBack: turn.rolledBack ?? undefined,
		})),
		items,
	};
}
import { WireSnapshot, WireMessage, WireActivity } from "./conversation-types";

export function applyQueuedTurnOrder(
	snapshot: ConversationSnapshot,
	fifoTurnIds: readonly string[],
): ConversationSnapshot {
	const queuedTurns = snapshot.turns.filter((turn) => turn.state === "queued");
	if (queuedTurns.length !== fifoTurnIds.length) return snapshot;

	const queuedById = new Map(queuedTurns.map((turn) => [turn.id, turn]));
	const requestedAts = [...queuedTurns]
		.sort((left, right) => left.requestedAt.localeCompare(right.requestedAt))
		.map((turn) => turn.requestedAt);
	const reorderedQueued = fifoTurnIds.flatMap((turnId, index) => {
		const turn = queuedById.get(turnId);
		return turn
			? [{ ...turn, requestedAt: requestedAts[index] ?? turn.requestedAt }]
			: [];
	});
	if (reorderedQueued.length !== queuedTurns.length) return snapshot;

	const queuedIds = new Set(fifoTurnIds);
	const otherTurns = snapshot.turns.filter((turn) => !queuedIds.has(turn.id));
	return {
		...snapshot,
		turns: [...otherTurns, ...reorderedQueued].sort((left, right) =>
			left.requestedAt.localeCompare(right.requestedAt),
		),
	};
}

export function applyQueuedTurnOrderToPages(
	data: InfiniteData<ConversationSnapshot>,
	fifoTurnIds: readonly string[],
): InfiniteData<ConversationSnapshot> {
	if (data.pages.length === 0) return data;
	return {
		...data,
		pages: data.pages.map((page, index) =>
			index === 0 ? applyQueuedTurnOrder(page, fifoTurnIds) : page,
		),
	};
}

/** Merge the newest live page with any older pages loaded on demand. */
export function mergeConversationPages(pages: ConversationSnapshot[]): ConversationSnapshot | undefined {
	const live = pages[0];
	if (!live) return undefined;
	if (pages.length === 1) return live;

	const items = new Map<string, ConversationItem>();
	const turns = new Map<string, ConversationSnapshot["turns"][number]>();
	// Walk oldest to newest so the live page wins when a row changed after an older
	// page was fetched (for example, a streamed message settling).
	for (const page of [...pages].reverse()) {
		for (const item of page.items) items.set(`${item.kind}:${item.id}`, item);
		for (const turn of page.turns) turns.set(turn.id, turn);
	}

	const oldest = pages[pages.length - 1] ?? live;
	return {
		...live,
		oldestSequence: oldest.oldestSequence,
		hasMoreBefore: oldest.hasMoreBefore,
		items: [...items.values()].sort((a, b) => a.sequence - b.sequence),
		turns: [...turns.values()].sort((a, b) => a.requestedAt.localeCompare(b.requestedAt)),
	};
}

export function toMessage(wire: WireMessage): ConversationMessage {
	return {
		kind: "message",
		id: wire.id,
		turnId: wire.turnId,
		sequence: wire.sequence,
		revision: wire.revision,
		role: wire.role as MessageRole,
		origin: wire.origin as MessageOrigin,
		text: wire.text,
		content: (wire.content ?? []).map((item) => ({
			type: item.type,
			mimeType: item.mimeType || undefined,
			uri: item.uri || undefined,
			name: item.name || undefined,
			text: item.text || undefined,
			sourceConversationId: item.sourceConversationId || undefined,
			sourceMessageId: item.sourceMessageId || undefined,
			sourceRevision: item.sourceRevision ?? undefined,
		})),
		editAvailable: wire.editAvailable ?? undefined,
		continuation: wire.continuation || undefined,
		streaming: wire.streaming,
		senderSessionId: wire.senderSessionId,
		senderProjectId: wire.senderProjectId,
		senderDisplayName: wire.senderDisplayName,
		clientMessageId: wire.clientMessageId,
		createdAt: wire.createdAt,
	};
}

export function toActivity(wire: WireActivity): ConversationActivity {
	const detail = (wire.detail ?? {}) as Record<string, unknown>;
	return {
		kind: "activity",
		id: wire.id,
		turnId: wire.turnId,
		sequence: wire.sequence,
		revision: wire.revision,
		activityKind: wire.activityKind as ActivityKind,
		status: wire.status as ActivityStatus,
		summary: wire.summary,
		requestId: wire.requestId,
		providerItemId: wire.providerItemId,
		// The provider's own offered decisions, carried through the detail payload.
		// Rendering from this is what keeps the card from drawing a button the
		// provider will reject.
		decisions: readDecisions(detail),
		inputResponse: wire.activityKind === "user_input" ? readInputResponse(detail) : undefined,
		detail: detail as ConversationActivity["detail"],
		createdAt: wire.createdAt,
	};
}

export function readInputResponse(detail: Record<string, unknown>): ConversationActivity["inputResponse"] {
	const action = detail.action;
	if (action !== "accept" && action !== "decline" && action !== "cancel") return undefined;
	const content = detail.content;
	return {
		action,
		content: isInputContent(content) ? content : undefined,
	};
}

export function isInputContent(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function readDecisions(detail: Record<string, unknown>): DecisionOption[] | undefined {
	const raw = detail.decisions;
	if (!Array.isArray(raw)) return undefined;
	const options: DecisionOption[] = [];
	for (const entry of raw) {
		if (entry && typeof entry === "object" && "id" in entry) {
			const option = entry as {
				id?: unknown;
				label?: unknown;
				kind?: unknown;
			};
			if (typeof option.id === "string" && option.id !== "") {
				const kind = isDecisionKind(option.kind) ? option.kind : undefined;
				options.push({
					id: option.id,
					label: typeof option.label === "string" && option.label ? option.label : option.id,
					kind,
				});
			}
		}
	}
	return options.length > 0 ? options : undefined;
}

export function isDecisionKind(value: unknown): value is NonNullable<DecisionOption["kind"]> {
	return (
		value === "allow_once" ||
		value === "allow_always" ||
		value === "reject_once" ||
		value === "reject_always"
	);
}
