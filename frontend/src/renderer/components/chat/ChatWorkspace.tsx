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

import { useCallback, useLayoutEffect, useMemo, useState, type CSSProperties } from "react";
import { activateChatDraftScope, chatDraftScopeKey, type ChatDraftScope } from "../../lib/chat-drafts";
import { purgeFileAttachmentsForSession } from "../../hooks/useFileAttachments";
import { ChatWorkspaceProps } from "./ChatWorkspaceProps";


import { CHAT_FONT_SIZE_DEFAULT } from "./chat-workspace-constants";

export { CONTINUE_STOPPED_TURN_PROMPT, continuableTurnId } from "./ChatWorkspaceProps";
export type { ChatRetryControl, ChatWorkspaceProps } from "./ChatWorkspaceProps";
export { promptSpacerHeight, promptTopInset } from "./timeline-groups";
export { Timeline } from "./Timeline";
import { ChatWorkspaceContent } from "./ChatWorkspaceContent";















type ChatWorkspaceActivation =
	| { key: string; state: "active" }
	| { key: string; state: "failed"; reason: "obsolete" | "storage" };


/**
 * Do not mount any renderer draft owner until the daemon incarnation has
 * authoritatively claimed its storage scope. The activation transition itself
 * owns cleanup of the predecessor; callbacks from that obsolete surface can
 * then only fail closed against the successor lease. If storage alone fails,
 * Chat still mounts, but with an in-memory composer that owns no saved drafts.
 */
export function ChatWorkspace(props: ChatWorkspaceProps) {
	const { snapshot, session, draftOwner, uiSessionId = draftOwner?.sessionId ?? snapshot.sessionId } = props;
	const draftScope = useMemo<ChatDraftScope>(
		() => draftOwner ?? ({
			sessionId: uiSessionId,
			// Live surfaces carry the daemon-created session timestamp. Snapshot-only
			// fixtures retain the legacy logical scope for deterministic previews.
			incarnation: session?.createdAt ?? uiSessionId,
		}),
		[draftOwner, session?.createdAt, uiSessionId],
	);
	const scopeKey = chatDraftScopeKey(draftScope);
	const [activation, setActivation] = useState<ChatWorkspaceActivation>();
	const [activationAttempt, setActivationAttempt] = useState(0);
	const retryDraftPersistence = useCallback(() => setActivationAttempt((attempt) => attempt + 1), []);

	useLayoutEffect(() => {
		// Snapshot-only previews have no daemon session incarnation to arbitrate.
		// Their legacy logical scope remains isolated to fixture/demo surfaces.
		if (!session?.createdAt && !draftOwner) {
			setActivation({ key: scopeKey, state: "active" });
			return;
		}
		const result = activateChatDraftScope(draftScope);
		if (!result.ok) {
			setActivation({ key: scopeKey, state: "failed", reason: result.reason });
			return;
		}
		if (result.replaced) purgeFileAttachmentsForSession(draftScope.sessionId);
		setActivation({ key: scopeKey, state: "active" });
	}, [activationAttempt, draftOwner, draftScope, scopeKey, session?.createdAt]);

	const obsolete = activation?.key === scopeKey && activation.state === "failed" && activation.reason === "obsolete";
	if (activation?.key !== scopeKey || obsolete) {
		return (
			<section
				aria-label="Chat"
				className="cursor-chat-surface flex h-full min-h-0 flex-col items-center justify-center px-6 [font-size:var(--chat-font-size)]"
				data-session-mode={snapshot.mode}
				style={{ "--chat-font-size": `${CHAT_FONT_SIZE_DEFAULT}px` } as CSSProperties}
			>
				{obsolete ? (
					<div className="max-w-lg rounded-lg border border-border bg-card p-4">
						<p className="text-xs text-foreground" role="alert">
							This Chat view belongs to an older session incarnation. Reopen the current session to continue.
						</p>
					</div>
				) : null}
			</section>
		);
	}

	const draftPersistenceAvailable = activation.state === "active";
	return (
		<ChatWorkspaceContent
			key={scopeKey}
			{...props}
			draftScope={draftScope}
			draftPersistenceAvailable={draftPersistenceAvailable}
			onRetryDraftPersistence={draftPersistenceAvailable ? undefined : retryDraftPersistence}
		/>
	);
}




/* -------------------------------------------------------------------------- */
/* identity                                                                    */
/* -------------------------------------------------------------------------- */





























