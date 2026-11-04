import { useStableSet } from "../../hooks/useStable";
/**
 * The Chat surface for a session whose persisted mode is `chat`.
 *
 * Rendered from a durable snapshot the daemon serves. The renderer stays thin:
 * items arrive already ordered by sequence and are never re-sorted here, turn
 * state comes from the daemon rather than being inferred from the last message,
 * and a decision is a typed action carrying the provider's request id.
 *
 * State this surface must be able to show, because each one happens: empty,
 * streaming, waiting on an approval, a command still running with no completion,
 * a turn the user stopped, a controller that died mid-turn, and history the user
 * has scrolled away from.
 */

import { useMemo } from "react";
import { retrySourceTurnIds } from "./timeline-groups";
import { can, type ConversationSnapshot } from "../../types/conversation";

/**
 * Which turns can be edited, branched from or retried.
 */
export function useTurnEligibility({ snapshot }: {
	snapshot: ConversationSnapshot;
}) {
	const canForkHistoricalContext = can(snapshot, "fork");

	const canReconstructHistoricalContext =
		can(snapshot, "prompt_replay") && can(snapshot, "embedded_context");

	const branchPoints = useMemo(
		() => new Map((snapshot.branchPoints ?? []).map((point) => [point.turnId, point])),
		[snapshot.branchPoints],
	);

	const firstEditableHumanPromptTurnId = useMemo(
		() =>
			snapshot.items.find(
				(item) =>
					item.kind === "message" &&
					item.role === "user" &&
					item.origin === "human" &&
					item.editAvailable &&
					item.turnId,
			)?.turnId,
		[snapshot.items],
	);

	const { editableTurns: editableTurnsNow, reconstructedTurns } = useMemo(() => {
		const editable = new Set<string>();
		const reconstructed = new Set<string>();
		if (snapshot.controller.state !== "ready") {
			return {
				editableTurns: editable,
				reconstructedTurns: reconstructed,
			};
		}
		const accepted = new Set(
			snapshot.turns
				.filter(
					(turn) =>
						turn.state === "completed" || turn.state === "interrupted" || turn.state === "failed",
				)
				.map((turn) => turn.id),
		);
		for (const item of snapshot.items) {
			if (
				item.kind !== "message" ||
				item.role !== "user" ||
				item.origin !== "human" ||
				!item.turnId
			) {
				continue;
			}
			const firstPromptInBinding =
				!snapshot.hasMoreBefore && item.turnId === firstEditableHumanPromptTurnId;
			const canNativeFork =
				canForkHistoricalContext &&
				(snapshot.nativeForkAvailableAfterSequence ?? 0) > 0 &&
				item.sequence > (snapshot.nativeForkAvailableAfterSequence ?? 0);
			if (
				item.editAvailable &&
				accepted.has(item.turnId) &&
				(firstPromptInBinding || canNativeFork || canReconstructHistoricalContext)
			) {
				editable.add(item.turnId);
				if (!firstPromptInBinding && !canNativeFork) reconstructed.add(item.turnId);
			}
		}
		return { editableTurns: editable, reconstructedTurns: reconstructed };
	}, [
		canForkHistoricalContext,
		canReconstructHistoricalContext,
		firstEditableHumanPromptTurnId,
		snapshot.controller.state,
		snapshot.hasMoreBefore,
		snapshot.items,
		snapshot.nativeForkAvailableAfterSequence,
		snapshot.turns,
	]);

	const consumedRetrySources = useMemo(() => retrySourceTurnIds(snapshot), [snapshot]);

	const retryableTurns = useMemo(() => {
		// A steer can split one logical turn into several visual groups. Its prompt
		// still makes the final failed group retryable, even when it is in an earlier one.
		const humanPromptTurnIds = new Set(snapshot.items.flatMap((item) =>
			item.kind === "message" && item.role === "user" && item.origin === "human" && item.turnId
				? [item.turnId]
				: [],
		));
		return new Map(
			snapshot.turns
				.filter(
					(turn) =>
						turn.state === "failed" &&
						Boolean(turn.providerTurnId) &&
						!consumedRetrySources.has(turn.id) &&
						(humanPromptTurnIds.has(turn.id) || Boolean(turn.usageLimit && turn.hasHumanPrompt)),
				)
				.map((turn) => [turn.id, turn]),
		);
	}, [snapshot.items, snapshot.turns, consumedRetrySources]);

	const editableTurns = useStableSet(editableTurnsNow);

	return { branchPoints, reconstructedTurns, retryableTurns, editableTurns };
}
