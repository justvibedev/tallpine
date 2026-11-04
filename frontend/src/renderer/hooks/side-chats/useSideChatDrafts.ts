/* Side-chat draft text, references, attachments and persistence, split out of chat/IndependentSideChats.tsx (B298, P56). */

import { useCallback, useEffect, useRef, useState, type MutableRefObject } from "react";
import { useTranslation } from "react-i18next";
import { parseSideChatDraft, type SideChatDraft } from "../../components/chat/sideChatDraft";
import { readSideChatDraft, saveSideChatDraft } from "../../components/chat/sideChatDraftWriter";
import { apiErrorMessage } from "../../lib/api-client";
import { tallpineBridge } from "../../lib/bridge";
import type { ChatDraftExcerptReference } from "../../lib/chat-drafts";
import { sideFilePayload } from "../../lib/side-chats-api";
import { withDraftAttachments, withDraftReference, withDraftText } from "../../lib/side-chats-model";

export function useSideChatDrafts({
	sessionId,
	sessionRef,
	closedSidesRef,
	activeId,
	activeIdRef,
	stageAttachments,
	setError,
}: {
	sessionId: string;
	sessionRef: MutableRefObject<string>;
	closedSidesRef: MutableRefObject<Set<string>>;
	activeId: string | undefined;
	activeIdRef: MutableRefObject<string | undefined>;
	stageAttachments: (items: { mimeType: string; data: string }[]) => Promise<string[]>;
	setError: (message: string | undefined) => void;
}) {
	const { t } = useTranslation();
	const [drafts, setDrafts] = useState<Record<string, SideChatDraft>>({});
	const draftsRef = useRef(drafts);
	draftsRef.current = drafts;
	const [attachments, setAttachments] = useState<Record<string, { path: string; file: File }[]>>({});
	const [focusKey, setFocusKey] = useState(0);
	useEffect(() => {
		if (!activeId || drafts[activeId] !== undefined) return;
		void readSideChatDraft(sessionId, activeId)
			.then(({ data }) => {
				if (sessionRef.current !== sessionId) return;
				if (data)
					setDrafts((current) => ({
						...current,
						[activeId]: current[activeId] ?? parseSideChatDraft(data.contentJson),
					}));
			})
			.catch((error) => {
				if (sessionRef.current === sessionId) setError(String(error));
			});
	}, [sessionId, activeId, drafts]);

	const updateDraft = useCallback((sideId: string, text: string) => {
		setDrafts((current) => withDraftText(current, sideId, text));
	}, []);
	const replaceDraft = useCallback(
		async (sideId: string, draft: SideChatDraft) => {
			const next = {
				...draft,
				pendingDelivery: draftsRef.current[sideId]?.pendingDelivery,
				lastHandoffDelivery: draftsRef.current[sideId]?.lastHandoffDelivery,
			};
			// Apply the edit before awaiting I/O so its response cannot overwrite a newer receipt.
			draftsRef.current = { ...draftsRef.current, [sideId]: next };
			setDrafts((current) => ({ ...current, [sideId]: next }));
			setAttachments((current) => ({ ...current, [sideId]: [] }));
			setFocusKey((key) => key + 1);
			const { error: requestError } = await saveSideChatDraft(sessionId, sideId, JSON.stringify(next));
			if (requestError) throw requestError;
			await tallpineBridge.sideChats.capture();
		},
		[sessionId],
	);

	const addReference = useCallback((sideId: string, excerpt: ChatDraftExcerptReference) => {
		setDrafts((current) => {
			const result = withDraftReference(current, sideId, excerpt);
			if (result.outcome === "limit") queueMicrotask(() => setError(t("sideChat.referenceLimit")));
			return result.drafts;
		});
		setFocusKey((key) => key + 1);
	}, [t]);
	const attachFiles = useCallback(
		async (sideId: string, files: File[]) => {
			if (files.length === 0) return;
			try {
				const payloads = await Promise.all(files.map(sideFilePayload));
				const paths = await stageAttachments(payloads);
				if (paths.length !== files.length) throw new Error("Could not stage every attached file.");
				setDrafts((current) =>
					withDraftAttachments(
						current,
						sideId,
						files.map((file, index) => ({
							id: crypto.randomUUID(),
							path: paths[index],
							name: file.name,
							mimeType: file.type || "application/octet-stream",
							bytes: file.size,
						})),
					),
				);
				setAttachments((current) => ({
					...current,
					[sideId]: [...(current[sideId] ?? []), ...files.map((file, index) => ({ file, path: paths[index] }))],
				}));
			} catch (cause) {
				setError(apiErrorMessage(cause));
			}
		},
		[stageAttachments],
	);

	useEffect(() => {
		if (!activeId || drafts[activeId] === undefined) return;
		const draft = drafts[activeId];
		const save = () => {
			if (closedSidesRef.current.has(activeId)) return;
			const currentDraft = draftsRef.current[activeId] ?? draft;
			void saveSideChatDraft(sessionId, activeId, JSON.stringify(currentDraft))
				.then(({ error: requestError }) => {
					if (requestError) throw requestError;
					return tallpineBridge.sideChats.capture();
				})
				.catch((cause) => {
					if (sessionRef.current === sessionId && !closedSidesRef.current.has(activeId))
						setError(apiErrorMessage(cause));
				});
		};
		const timer = window.setTimeout(() => {
			save();
		}, 350);
		return () => {
			window.clearTimeout(timer);
			if (
				!closedSidesRef.current.has(activeId) &&
				(activeIdRef.current !== activeId || sessionRef.current !== sessionId)
			)
				save();
		};
	}, [sessionId, activeId, drafts]);

	return {
		drafts,
		setDrafts,
		draftsRef,
		attachments,
		setAttachments,
		focusKey,
		setFocusKey,
		updateDraft,
		replaceDraft,
		addReference,
		attachFiles,
	};
}
