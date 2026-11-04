/** Types, errors and small helpers shared by the TaskComposer pieces. */
import type { components } from "../../../api/schema";
import { apiClient } from "../../lib/api-client";
import { clientForHost } from "../../lib/host-clients";
import { type FileAttachmentPayload } from "../../hooks/useFileAttachments";

export type Project = components["schemas"]["Project"];
export type DelegateAgent = components["schemas"]["DelegateTaskRequest"]["agent"];

export type CreateTaskInput = {
	clientRequestId?: string;
	projectId: string;
	brief: string;
	agent?: DelegateAgent;
	model?: string;
	effort?: string;
	mode?: "chat" | "tui";
	approvalMode?: "bypass-permissions";
	attachments?: FileAttachmentPayload[];
	taskPreparation?: string;
	backlogRef?: string;
	backlogForce?: boolean;
};

export const CHAT_PREFLIGHT_CODES = new Set([
	"SESSION_MODE_UNSUPPORTED",
	"CHAT_DRIVER_UNAVAILABLE",
	"CHAT_DRIVER_INCOMPATIBLE",
	"CHAT_AUTH_REQUIRED",
]);

export const READINESS_RECONCILE_CODES = new Set(["AGENT_BINARY_NOT_FOUND", "AGENT_AUTH_REQUIRED", "CHAT_AUTH_REQUIRED"]);

export function cancelTaskPreparation(token: string, hostId?: string): void {
	if (!token) return;
	try {
		void (hostId ? clientForHost(hostId) : apiClient).DELETE("/api/v1/task-preparations/{token}", {
			params: { path: { token } },
		}).catch(() => { /* The host reclaims abandoned preparations after their TTL. */ });
	} catch {
		// A disconnected host will reclaim this preparation on its own TTL.
	}
}

export class TaskCreateError extends Error {
	constructor(
		message: string,
		readonly code?: string,
		readonly details?: components["schemas"]["APIError"]["details"],
	) {
		super(message);
		this.name = "TaskCreateError";
	}
}

export type FallbackAction = "tui" | "bypass-permissions";

export function hasErrorDetail(details: components["schemas"]["APIError"]["details"] | undefined, key: string, value: string) {
	const values = details?.[key];
	return Array.isArray(values) && values.includes(value);
}

export type TaskComposerProps = {
	projectId?: string;
	hostId?: string;
	onCreated: (sessionId: string) => void;
	onDirtyChange?: (dirty: boolean) => void;
	onSubmittingChange?: (submitting: boolean) => void;
	autoFocusTitle?: boolean;
	createLabel?: string;
	/** Starts the worker on a backlog item: its brief is prefilled and the daemon links the session to it. */
	backlog?: { ref: string; title: string; force?: boolean };
};
