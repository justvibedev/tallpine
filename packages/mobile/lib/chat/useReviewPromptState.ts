import { useCallback, useEffect } from "react";
import AsyncStorage from "@react-native-async-storage/async-storage";
import { machineIdentity } from "../config";
import { usePRSummaries } from "../usePRSummaries";
import { sessionPRReadyForReview } from "../reviewView";
import { MobileChatSession } from "./ChatSessionScreen";
import { ServerConfig } from "../config";

export function useReviewPromptState({
	session,
	config,
	currentHostId,
	setLoadedReviewPromptStateKey,
	setCollapsedReviewPRKey,
	loadedReviewPromptStateKey,
	collapsedReviewPRKey,
}: {
	session: MobileChatSession;
	config: ServerConfig | null;
	currentHostId: string | undefined;
	setLoadedReviewPromptStateKey: React.Dispatch<React.SetStateAction<string | undefined>>;
	setCollapsedReviewPRKey: React.Dispatch<React.SetStateAction<string | undefined>>;
	loadedReviewPromptStateKey: string | undefined;
	collapsedReviewPRKey: string | undefined;
}) {
	const reviewPromptPRCandidate = "projectName" in session ? undefined : sessionPRReadyForReview(session);
	const reviewPromptKey = reviewPromptPRCandidate ? `${reviewPromptPRCandidate.url}#${reviewPromptPRCandidate.number}` : undefined;
	const reviewPromptHostKey = config ? machineIdentity(config) : currentHostId;
	const reviewPromptStateStorageKey = reviewPromptKey && reviewPromptHostKey
		? `ao.chat.reviewPromptCollapsed:${encodeURIComponent(reviewPromptHostKey)}:${encodeURIComponent(session.id)}:${encodeURIComponent(reviewPromptKey)}`
		: undefined;
	useEffect(() => {
		let cancelled = false;
		setLoadedReviewPromptStateKey(undefined);
		setCollapsedReviewPRKey(undefined);
		if (!reviewPromptKey || !reviewPromptStateStorageKey) {
			setLoadedReviewPromptStateKey(reviewPromptStateStorageKey);
			return () => { cancelled = true; };
		}
		AsyncStorage.getItem(reviewPromptStateStorageKey).then((value) => {
			if (cancelled) return;
			setCollapsedReviewPRKey(value === "1" ? reviewPromptKey : undefined);
			setLoadedReviewPromptStateKey(reviewPromptStateStorageKey);
		}).catch(() => {
			if (cancelled) return;
			setLoadedReviewPromptStateKey(reviewPromptStateStorageKey);
		});
		return () => { cancelled = true; };
	}, [reviewPromptKey, reviewPromptStateStorageKey]);
	const reviewPromptStateReady = !reviewPromptStateStorageKey || loadedReviewPromptStateKey === reviewPromptStateStorageKey;
	const reviewPromptPR = reviewPromptStateReady ? reviewPromptPRCandidate : undefined;
	const reviewPromptCollapsed = Boolean(reviewPromptStateReady && reviewPromptKey && reviewPromptKey === collapsedReviewPRKey);
	const collapseReviewPrompt = useCallback(() => {
		if (!reviewPromptKey || !reviewPromptStateStorageKey) return;
		setCollapsedReviewPRKey(reviewPromptKey);
		void AsyncStorage.setItem(reviewPromptStateStorageKey, "1").catch(() => {});
	}, [reviewPromptKey, reviewPromptStateStorageKey]);
	const expandReviewPrompt = useCallback(() => {
		setCollapsedReviewPRKey(undefined);
		if (reviewPromptStateStorageKey) void AsyncStorage.removeItem(reviewPromptStateStorageKey).catch(() => {});
	}, [reviewPromptStateStorageKey]);
	const reviewSummaries = usePRSummaries(config && reviewPromptPR ? [{ config, sessionId: session.id }] : []);
	const reviewPromptSummary = config && reviewPromptPR ? reviewSummaries.summaryFor(config, session.id, reviewPromptPR.number) : undefined;

	return { reviewPromptPR, reviewPromptCollapsed, collapseReviewPrompt, expandReviewPrompt, reviewPromptSummary };
}
