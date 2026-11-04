/** Wire aliases, send input and mutation input types for the conversation hooks. */

import type { components } from "../../../api/schema";
import { apiErrorMessage } from "../../lib/api-client";

export type WireSnapshot = components["schemas"]["ConversationSnapshotResponse"];
export type WireMessage = components["schemas"]["ConversationMessageResponse"];
export type WireActivity = components["schemas"]["ConversationActivityResponse"];
export type WireImageContent = components["schemas"]["ConversationImageContentRequest"];
export type WireResourceContent = components["schemas"]["ConversationResourceContentRequest"];
export type WireExcerptReference = components["schemas"]["ConversationExcerptReferenceRequest"];

export interface ConversationSendInput {
	text: string;
	attachments?: WireImageContent[];
	resources?: WireResourceContent[];
	excerpts?: WireExcerptReference[];
	/** Caller-owned durable idempotency key used for crash-safe retries. */
	clientMessageId?: string;
	/** The user's one-click "continue" after stopping a turn. */
	continuation?: boolean;
	/** Switches the agent first; the new agent answers this message as its first turn. */
	providerSwitch?: Omit<components["schemas"]["ProviderSwitchRequest"], "idempotencyKey" | "message">;
	/** What the transcript's "Switched" line shows while the switch runs. Never sent. */
	switchDisplay?: ConversationSwitchDisplay;
}

/** The two sides of a switch as the transcript names them, known before the daemon answers. */
export interface ConversationSwitchDisplay {
	sourceHarness: string;
	sourceModel?: string;
	targetModel?: string;
	targetEffort?: string;
}

/** A send whose agent switch was refused or failed, so the message never left. */
export class ProviderSwitchFailed extends Error {
	constructor(readonly reason: unknown) {
		super(apiErrorMessage(reason));
	}
}

/**
 * A remote daemon's gateway answers 502–504 while the daemon may still finish the switch.
 * The daemon's own refusal (a PROVIDER_SWITCH_ code, 503 for "not installed") is definite.
 */
export const switchOutcomeUnknown = (status: number, error: unknown) =>
	(status === 502 || status === 503 || status === 504) &&
	!String((error as { code?: unknown } | undefined)?.code ?? "").startsWith("PROVIDER_SWITCH_");

export interface ConversationSendMutationInput {
	targetSessionId: string;
	clientMessageId: string;
	input: ConversationSendInput;
}

export interface ConversationSessionMutationInput {
	targetSessionId: string;
}

export interface ConversationRetryMutationInput extends ConversationSessionMutationInput {
	requestId: string;
	sourceTurnId: string;
}

export interface ConversationEditMutationInput extends ConversationRetryMutationInput {
	text: string;
}
