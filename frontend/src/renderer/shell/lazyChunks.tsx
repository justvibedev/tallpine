import { lazy } from "react";
import { loadSessionView } from "../lib/session-view-chunk";

// Only shown while an orchestrator is starting, but it pulls in the whole chat
// stack (ChatWorkspace, lexical, markdown). Lazy keeps that off every route.
export const loadOrchestratorStartingChat = () => import("../components/chat/OrchestratorStartingChat");

export const OrchestratorStartingChat = lazy(() =>
	loadOrchestratorStartingChat().then((module) => ({ default: module.OrchestratorStartingChat })),
);

export function preloadOrchestratorStartingChat(): void {
	void loadOrchestratorStartingChat().catch(() => undefined);
}

export const SessionView = lazy(() => loadSessionView().then((module) => ({ default: module.SessionView })));

export function preloadSessionView(): void {
	void loadSessionView().catch(() => undefined);
}
