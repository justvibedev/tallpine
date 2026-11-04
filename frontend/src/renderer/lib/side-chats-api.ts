/* Side-chat HTTP calls, split out of chat/IndependentSideChats.tsx (B298, P56). Each wrapper returns the apiClient result unchanged. */

import type { SideChatDraft } from "../components/chat/sideChatDraft";
import { apiClient } from "./api-client";

const SIDES = "/api/v1/sessions/{sessionId}/conversation/side-chats" as const;
const SIDE = "/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}" as const;

export type SideMessageRequest = NonNullable<SideChatDraft["pendingDelivery"]>["request"];

export function listSideChats(sessionId: string) {
	return apiClient.GET(SIDES, { params: { path: { sessionId } } });
}

export function getSideSnapshot(sessionId: string, sideId: string, query?: { before: string; limit: number }) {
	return apiClient.GET(SIDE, { params: { path: { sessionId, sideId }, ...(query ? { query } : {}) } });
}

export function createSideChat(
	sessionId: string,
	body: {
		idempotencyKey: string;
		forceNew: boolean;
		reference?: { conversationId: string; messageId: string; revision: number; text: string };
	},
) {
	return apiClient.POST(SIDES, { params: { path: { sessionId } }, body });
}

export function postSideMessage(sessionId: string, sideId: string, body: SideMessageRequest) {
	return apiClient.POST(`${SIDE}/messages`, { params: { path: { sessionId, sideId } }, body });
}

export function deleteSideChat(sessionId: string, sideId: string) {
	return apiClient.DELETE(SIDE, { params: { path: { sessionId, sideId } } });
}

export function interruptSideChat(sessionId: string, sideId: string) {
	return apiClient.POST(`${SIDE}/interrupt`, { params: { path: { sessionId, sideId } } });
}

export function compactSideChat(sessionId: string, sideId: string) {
	return apiClient.POST(`${SIDE}/compact`, { params: { path: { sessionId, sideId } } });
}

export function recoverSideChat(sessionId: string, sideId: string) {
	return apiClient.POST(`${SIDE}/recover`, { params: { path: { sessionId, sideId } } });
}

export function resolveSideApproval(sessionId: string, sideId: string, requestId: string, decisionId: string) {
	return apiClient.POST(`${SIDE}/approvals/{requestId}/resolve`, {
		params: { path: { sessionId, sideId, requestId } },
		body: { decisionId },
	});
}

export function resolveSideInput(
	sessionId: string,
	sideId: string,
	requestId: string,
	action: "accept" | "decline" | "cancel",
	content: Record<string, string>,
) {
	return apiClient.POST(`${SIDE}/inputs/{requestId}/resolve`, {
		params: { path: { sessionId, sideId, requestId } },
		body: { action, content },
	});
}

export function updateSideSettings(sessionId: string, sideId: string, model: string, effort: string) {
	return apiClient.PATCH(`${SIDE}/settings`, { params: { path: { sessionId, sideId } }, body: { model, effort } });
}

export function retrySideTurn(sessionId: string, sideId: string, turnId: string) {
	return apiClient.POST(`${SIDE}/turns/{turnId}/retry`, { params: { path: { sessionId, sideId, turnId } } });
}

export function cancelSideTurn(sessionId: string, sideId: string, turnId: string) {
	return apiClient.POST(`${SIDE}/turns/{turnId}/cancel`, { params: { path: { sessionId, sideId, turnId } } });
}

export function editQueuedSideTurn(sessionId: string, sideId: string, turnId: string, text: string) {
	return apiClient.PATCH(`${SIDE}/turns/{turnId}`, {
		params: { path: { sessionId, sideId, turnId } },
		body: { text, clientMessageId: "" },
	});
}

export function renameSideChat(sessionId: string, sideId: string, label: string) {
	return apiClient.PATCH(`${SIDE}/label`, { params: { path: { sessionId, sideId } }, body: { label } });
}

export function sideEventsUrl(baseUrl: string, sessionId: string, sideId: string): string {
	return `${baseUrl}/api/v1/sessions/${encodeURIComponent(sessionId)}/conversation/side-chats/${encodeURIComponent(sideId)}/events`;
}

export async function sideFilePayload(file: File): Promise<{ mimeType: string; data: string }> {
	const dataUrl = await new Promise<string>((resolve, reject) => {
		const reader = new FileReader();
		reader.onload = () => resolve(String(reader.result));
		reader.onerror = () => reject(reader.error);
		reader.readAsDataURL(file);
	});
	return {
		mimeType: file.type || "application/octet-stream",
		data: dataUrl.split(",", 2)[1] ?? "",
	};
}
