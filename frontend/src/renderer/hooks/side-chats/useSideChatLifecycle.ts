/* Side-chat creation and close, split out of useSideChats (B303). */

import { useCallback } from "react";
import type { Dispatch, MutableRefObject, SetStateAction } from "react";
import { PRODUCT_NAME } from "../../../shared/brand";
import type { SideChatDraft } from "../../components/chat/sideChatDraft";
import { apiErrorMessage } from "../../lib/api-client";
import { tallpineBridge } from "../../lib/bridge";
import type { ChatDraftExcerptReference } from "../../lib/chat-drafts";
import { createSideChat, deleteSideChat } from "../../lib/side-chats-api";
import { nextActiveAfterClose, omitKey, upsertSide, type Side, type Snapshot } from "../../lib/side-chats-model";


type Setter<T> = Dispatch<SetStateAction<T>>;
type Attachments = Record<string, { path: string; file: File }[]>;

export function useSideChatLifecycle({
	sessionId, enabled, canCreate, sessionRef, closedSidesRef, setError, setPending,
	sides, setSides, activeId, setActiveId, setVisible, setFocusKey, setSnapshot, setDrafts, setAttachments, addReference,
}: {
	sessionId: string;
	enabled: boolean;
	canCreate: boolean;
	sessionRef: MutableRefObject<string>;
	closedSidesRef: MutableRefObject<Set<string>>;
	setError: Setter<string | undefined>;
	setPending: Setter<boolean>;
	sides: Side[];
	setSides: Setter<Side[]>;
	activeId: string | undefined;
	setActiveId: Setter<string | undefined>;
	setVisible: Setter<boolean>;
	setFocusKey: Setter<number>;
	setSnapshot: Setter<Snapshot | undefined>;
	setDrafts: Setter<Record<string, SideChatDraft>>;
	setAttachments: Setter<Attachments>;
	addReference: (sideId: string, excerpt: ChatDraftExcerptReference) => void;
}) {
	const create = useCallback(
		async (excerpt?: ChatDraftExcerptReference, forceNew = false, idempotencyKey: string = crypto.randomUUID()) => {
			if (!enabled) throw new Error(`Side chats are currently available only for local ${PRODUCT_NAME} sessions.`);
			if (!canCreate) throw new Error("The current provider does not support resumable side chats.");
			setPending(true);
			setError(undefined);
			try {
				const { data, error: requestError } = await createSideChat(sessionId, {
					idempotencyKey,
					forceNew,
					reference: excerpt
						? {
								conversationId: excerpt.conversationId,
								messageId: excerpt.messageId,
								revision: excerpt.revision,
								text: excerpt.text,
							}
						: undefined,
				});
				if (requestError) throw requestError;
				if (sessionRef.current !== sessionId || closedSidesRef.current.has(data.side.id))
					throw new Error("Side chat is no longer active.");
				await tallpineBridge.sideChats.capture().catch(() => undefined);
				setSides((current) => upsertSide(current, data.side));
				setActiveId(data.side.id);
				setVisible(true);
				setFocusKey((key) => key + 1);
				if (excerpt) addReference(data.side.id, excerpt);
				return { id: data.side.id };
			} catch (cause) {
				setError(apiErrorMessage(cause));
				throw cause;
			} finally {
				setPending(false);
			}
		},
		[sessionId, enabled, canCreate],
	);

	const close = useCallback(
		async (sideId: string) => {
			// Record close intent in Electron RAM before the request can race a daemon restart.
			try {
				await tallpineBridge.sideChats.capture(sideId);
			} catch (cause) {
				await tallpineBridge.sideChats.capture(sideId, true).catch(() => undefined);
				throw cause;
			}
			const { error: requestError } = await deleteSideChat(sessionId, sideId)
				.catch(async (cause) => {
					await tallpineBridge.sideChats.capture(sideId, true).catch(() => undefined);
					throw cause;
				});
			if (requestError) {
				await tallpineBridge.sideChats.capture(sideId, true).catch(() => undefined);
				throw new Error(apiErrorMessage(requestError));
			}
			if (sessionRef.current !== sessionId) return;
			closedSidesRef.current.add(sideId);
			await tallpineBridge.sideChats.capture().catch(() => undefined);
			if (sessionRef.current !== sessionId) return;
			setDrafts((current) => omitKey(current, sideId));
			setAttachments((current) => omitKey(current, sideId));
			const remaining = sides.filter((side) => side.id !== sideId);
			setSides(remaining);
			if (activeId === sideId) {
				setActiveId(nextActiveAfterClose(sides, sideId));
				setSnapshot(undefined);
			}
		},
		[sessionId, activeId, sides],
	);

	return { create, close };
}
