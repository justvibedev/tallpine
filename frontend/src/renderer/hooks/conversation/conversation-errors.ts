/** Classifies steer and edit failures that definitively did not reach the agent. */

import { apiErrorCode, apiErrorMessage } from "../../lib/api-client";
import type { ChatEditOutcome, ChatSteerOutcome } from "../../types/conversation";

/**
 * What to tell the user about a refused steer.
 *
 * The daemon's own message is preferred for the retryable case because only it knows
 * which kind of turn refused — a compaction and a review read differently, and
 * "cannot be steered" alone leaves the user with nothing to do next.
 */
export function steerRefusal(error: unknown): string | undefined {
	const code = apiErrorCode(error);
	if (!code) return undefined;
	switch (code) {
		case "CHAT_NO_ACTIVE_TURN":
			return "The turn finished before this landed. Send it as a message instead.";
		case "CHAT_INTERFACE_TRANSITION":
			return "The session is switching interfaces. This guidance was not delivered; send it after the switch finishes.";
		case "CHAT_TURN_NOT_STEERABLE":
			return `${apiErrorMessage(error)} Try again once it finishes.`;
		case "CHAT_STEER_UNSUPPORTED":
			// Answered by hiding the control, so there is nothing to say.
			return undefined;
		case "CHAT_STEER_TEXT_REQUIRED":
			return "Type something to steer with.";
		default:
			return apiErrorMessage(error);
	}
}

export const DEFINITIVE_STEER_NON_ACCEPTANCE_CODES = new Set([
	"CHAT_NO_ACTIVE_TURN",
	"CHAT_INTERFACE_TRANSITION",
	"CHAT_TURN_NOT_STEERABLE",
	"CHAT_STEER_UNSUPPORTED",
	"CHAT_STEER_TEXT_REQUIRED",
	"CHAT_UNSUPPORTED_STEER_CONTENT",
	"CHAT_PROVIDER_REFUSED",
	"SESSION_MODE_MISMATCH",
	"SESSION_NOT_FOUND",
	"CHAT_CONTROLLER_NOT_READY",
	"CHAT_AUTH_REQUIRED",
]);

export function steerNonAcceptance(error: unknown): ChatSteerOutcome | undefined {
	const code = apiErrorCode(error);
	if (!code || !DEFINITIVE_STEER_NON_ACCEPTANCE_CODES.has(code)) return undefined;
	return {
		status: "not-accepted",
		reason: steerRefusal(error) ?? apiErrorMessage(error),
	};
}

export const DEFINITIVE_EDIT_NON_ACCEPTANCE_CODES = new Set([
	"CHAT_EDIT_REJECTED",
	"CHAT_EDIT_UNSUPPORTED",
	"CHAT_EDIT_BUSY",
	"CHAT_EDIT_TURN_INVALID",
	"CHAT_PROVIDER_REFUSED",
	"SESSION_MODE_MISMATCH",
	"SESSION_NOT_FOUND",
	"CHAT_CONTROLLER_NOT_READY",
	"CHAT_AUTH_REQUIRED",
]);

export function editNonAcceptance(error: unknown): ChatEditOutcome | undefined {
	const code = apiErrorCode(error);
	if (!code || !DEFINITIVE_EDIT_NON_ACCEPTANCE_CODES.has(code)) return undefined;
	return { status: "not-accepted", reason: apiErrorMessage(error) };
}

