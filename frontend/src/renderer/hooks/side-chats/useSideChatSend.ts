/* Side-chat message delivery with durable receipts, split out of useSideChats (B303). */

import { useCallback } from "react";
import type { Dispatch, MutableRefObject, SetStateAction } from "react";
import { emptySideChatDraft, parseSideChatDraft, type SideChatDraft } from "../../components/chat/sideChatDraft";
import { readSideChatDraft, saveSideChatDraft } from "../../components/chat/sideChatDraftWriter";
import { apiErrorMessage } from "../../lib/api-client";
import { tallpineBridge } from "../../lib/bridge";
import type { ChatDraftExcerptReference } from "../../lib/chat-drafts";
import { postSideMessage, sideFilePayload } from "../../lib/side-chats-api";
import { parseSideCommand, settleDeliveredDraft, withoutPendingDelivery } from "../../lib/side-chats-model";

type Setter<T> = Dispatch<SetStateAction<T>>;
type Attachments = Record<string, { path: string; file: File }[]>;

export function useSideChatSend({
	sessionId, nativeImages, stageAttachments, sessionRef, closedSidesRef, sendingRef, setSendingRevision,
	setError, create, updateDraft, draftsRef, setDrafts, attachments, setAttachments,
}: {
	sessionId: string;
	nativeImages: boolean;
	stageAttachments: (items: { mimeType: string; data: string }[]) => Promise<string[]>;
	sessionRef: MutableRefObject<string>;
	closedSidesRef: MutableRefObject<Set<string>>;
	sendingRef: MutableRefObject<Set<string>>;
	setSendingRevision: Setter<number>;
	setError: Setter<string | undefined>;
	create: (excerpt?: ChatDraftExcerptReference, forceNew?: boolean, idempotencyKey?: string) => Promise<{ id: string }>;
	updateDraft: (sideId: string, text: string) => void;
	draftsRef: MutableRefObject<Record<string, SideChatDraft>>;
	setDrafts: Setter<Record<string, SideChatDraft>>;
	attachments: Attachments;
	setAttachments: Setter<Attachments>;
}) {
	const send = useCallback(
		async (
			sideId: string,
			text: string,
			extraAttachments: { mimeType: string; data: string }[] = [],
			overrideReferences?: ChatDraftExcerptReference[],
			propagateError = false,
			handoffClientMessageId?: string,
		): Promise<void> => {
			const btw = parseSideCommand(text);
			if (btw) {
				const created = await create(undefined, true);
				const question = btw.question;
				if (question || extraAttachments.length || overrideReferences?.length)
					await send(created.id, question, extraAttachments, overrideReferences, true);
				updateDraft(sideId, "");
				return;
			}

			if (sendingRef.current.has(sideId)) {
				if (propagateError) throw new Error("Side chat delivery is still in progress. Try again after it finishes.");
				return;
			}
			sendingRef.current.add(sideId);
			setSendingRevision((version) => version + 1);
			setError(undefined);
			let receipt = draftsRef.current[sideId]?.pendingDelivery;
			try {
				if (!Object.hasOwn(draftsRef.current, sideId)) {
					const { data, error } = await readSideChatDraft(sessionId, sideId);
					if (error) throw error;
					const loaded = parseSideChatDraft(data?.contentJson ?? "");
					const current = draftsRef.current[sideId] ?? loaded;
					draftsRef.current = { ...draftsRef.current, [sideId]: current };
					receipt = current.pendingDelivery;
				}
				const draft = draftsRef.current[sideId] ?? emptySideChatDraft();
				if (!receipt && handoffClientMessageId && draft.lastHandoffDelivery?.clientMessageId === handoffClientMessageId)
					receipt = draft.lastHandoffDelivery;
				if (receipt && handoffClientMessageId && receipt.clientMessageId !== handoffClientMessageId)
					throw new Error("This side chat has a different pending delivery.");
				const files = attachments[sideId] ?? [];
				if (!receipt) {
					const imagePayloads = [
						...extraAttachments,
						...(await Promise.all(
							files
								.filter(({ file }) => /^image\/(png|jpeg|gif|webp)$/i.test(file.type))
								.map(({ file }) => sideFilePayload(file)),
						)),
					];
					const paths = extraAttachments.length ? await stageAttachments(extraAttachments) : [];
					const allPaths = [...draft.attachments.map((file) => file.path), ...paths];

					const clientMessageId = handoffClientMessageId ?? crypto.randomUUID();
					receipt = {
						clientMessageId,
						submitted: {
							text: draft.text,
							attachments: draft.attachments,
							references: draft.references,
						},
						request: {
							text,
							resources: allPaths.map((path) => ({
								uri: path,
								name: path.split(/[\\/]/).at(-1) || "Attachment",
							})),
							clientMessageId,
							attachments: nativeImages ? imagePayloads : [],
							references: (overrideReferences ?? draft.references).map((ref) => ({
								conversationId: ref.conversationId,
								messageId: ref.messageId,
								revision: ref.revision,
								text: ref.text,
							})),
						},
					};
				}
				const frozen = receipt;
				const pending = {
					...(draftsRef.current[sideId] ?? draft),
					pendingDelivery: frozen,
				};
				draftsRef.current = { ...draftsRef.current, [sideId]: pending };
				setDrafts((current) => ({
					...current,
					[sideId]: { ...(current[sideId] ?? draft), pendingDelivery: frozen },
				}));
				// Persist the receipt before dispatch so reconnect can retry the exact request.
				const saved = await saveSideChatDraft(sessionId, sideId, JSON.stringify(pending));
				if (saved.error) throw saved.error;
				await tallpineBridge.sideChats.capture();
				if (sessionRef.current !== sessionId || closedSidesRef.current.has(sideId)) return;
				const { error: requestError, response } = await postSideMessage(sessionId, sideId, frozen.request);
				if (requestError) {
					// Server errors can occur after acceptance; keep their receipt too.
					if (response && response.status < 500)
						setDrafts((current) => withoutPendingDelivery(current, sideId, frozen));
					throw requestError;
				}
				await tallpineBridge.sideChats.capture().catch(() => undefined);
				if (sessionRef.current !== sessionId || closedSidesRef.current.has(sideId)) return;
				const d = draftsRef.current[sideId];
				if (d?.pendingDelivery === frozen) {
					const next = settleDeliveredDraft(d, frozen, handoffClientMessageId);
					draftsRef.current = { ...draftsRef.current, [sideId]: next };
					setDrafts((current) => ({ ...current, [sideId]: next }));
					const saved = await saveSideChatDraft(sessionId, sideId, JSON.stringify(next));
					if (saved.error) throw saved.error;
					await tallpineBridge.sideChats.capture().catch(() => undefined);
				}

				setAttachments((current) => (current[sideId] === files ? { ...current, [sideId]: [] } : current));
			} catch (cause) {
				if (sessionRef.current === sessionId && !closedSidesRef.current.has(sideId)) setError(apiErrorMessage(cause));
				if (propagateError) throw cause;
			} finally {
				sendingRef.current.delete(sideId);
				setSendingRevision((version) => version + 1);
			}
		},
		[sessionId, updateDraft, attachments, stageAttachments, nativeImages, create],
	);

	return send;
}
