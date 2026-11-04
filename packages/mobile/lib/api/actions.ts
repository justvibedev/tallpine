import type { ServerConfig } from "../config";
import { API, ATTACHMENT_REQUEST_TIMEOUT_MS, RESUME_REQUEST_TIMEOUT_MS, req } from "./http";
import { mapSession } from "./normalize";
import type { DashboardPR, DashboardSession, OrchestratorLink, SessionMode } from "./sessions";

// ---- Writes / actions -------------------------------------------------------

export async function killSession(cfg: ServerConfig, id: string): Promise<void> {
	await req(cfg, `${API}/sessions/${encodeURIComponent(id)}/kill`, { method: "POST" });
}

/** Rename the worker shown on the board without changing its conversation history. */
export async function renameSession(cfg: ServerConfig, id: string, displayName: string): Promise<void> {
	await req(cfg, `${API}/sessions/${encodeURIComponent(id)}`, {
		method: "PATCH",
		body: JSON.stringify({ displayName }),
	});
}

export async function pinSession(cfg: ServerConfig, id: string): Promise<void> {
	await req(cfg, `${API}/sessions/${encodeURIComponent(id)}/pin`, { method: "POST" });
}

export async function unpinSession(cfg: ServerConfig, id: string): Promise<void> {
	await req(cfg, `${API}/sessions/${encodeURIComponent(id)}/pin`, { method: "DELETE" });
}

/** Automatically launch a review whenever this session's PR head changes. */
export async function setSessionAutoReview(cfg: ServerConfig, id: string, enabled: boolean): Promise<DashboardSession> {
	const res = await req(cfg, `${API}/sessions/${encodeURIComponent(id)}/auto-review`, {
		method: "PUT",
		body: JSON.stringify({ enabled }),
	});
	const data = await res.json();
	return mapSession(data?.session ?? data);
}

/** Automatically send completed Tallpine and GitHub review feedback to the worker. */
export async function setSessionAutoInjectReview(cfg: ServerConfig, id: string, autoInjectReview: boolean): Promise<DashboardSession> {
	const res = await req(cfg, `${API}/sessions/${encodeURIComponent(id)}/auto-inject-review`, {
		method: "PATCH",
		body: JSON.stringify({ autoInjectReview }),
	});
	const data = await res.json();
	return mapSession(data?.session ?? data);
}

/** Automatically send failing CI checks to the worker. */
export async function setSessionAutoInjectCI(cfg: ServerConfig, id: string, autoInjectCI: boolean): Promise<DashboardSession> {
	const res = await req(cfg, `${API}/sessions/${encodeURIComponent(id)}/auto-inject-ci`, {
		method: "PATCH",
		body: JSON.stringify({ autoInjectCI }),
	});
	const data = await res.json();
	return mapSession(data?.session ?? data);
}

export async function restoreSession(cfg: ServerConfig, id: string): Promise<void> {
	await req(cfg, `${API}/sessions/${encodeURIComponent(id)}/restore`, { method: "POST" });
}

/** Restart a stopped agent/controller without restoring a terminated Tallpine session. */
export async function resumeSessionAgent(cfg: ServerConfig, id: string): Promise<void> {
	await req(cfg, `${API}/sessions/${encodeURIComponent(id)}/resume-agent`, { method: "POST" }, RESUME_REQUEST_TIMEOUT_MS);
}

export async function sendMessage(cfg: ServerConfig, id: string, message: string): Promise<void> {
	await req(cfg, `${API}/sessions/${encodeURIComponent(id)}/send`, {
		method: "POST",
		body: JSON.stringify({ message }),
	});
}

export async function spawnSession(
	cfg: ServerConfig,
	opts: { projectId?: string; prompt?: string; issueId?: string; harness?: string; model?: string; mode?: SessionMode; attachments?: SpawnAttachmentInput[]; clientRequestId?: string; parentSessionId?: string },
): Promise<DashboardSession> {
	const res = await req(cfg, `${API}/sessions`, {
		method: "POST",
		headers: opts.attachments?.length ? { "X-AO-Attachment-Upload": "1" } : undefined,
		body: JSON.stringify({
			projectId: opts.projectId,
			prompt: opts.prompt,
			issueId: opts.issueId,
			// The daemon needs an agent harness unless the project configures a
			// default worker.agent; the spawn screen lets the user pick one.
			harness: opts.harness || undefined,
			model: opts.model || undefined,
			// Mobile is Chat-first. Callers may deliberately request TUI for a harness
			// that cannot expose a structured controller, but omission must never make
			// the phone depend on a desktop preference it cannot see.
			mode: opts.mode ?? "chat",
			kind: "worker",
			// A lane orchestrator's id: the daemon gives the worker that lane.
			parentSessionId: opts.parentSessionId || undefined,
			attachments: opts.attachments?.length ? opts.attachments : undefined,
			clientRequestId: opts.clientRequestId,
		}),
	}, opts.attachments?.length ? ATTACHMENT_REQUEST_TIMEOUT_MS : undefined);
	const data = await res.json();
	return mapSession(data?.session ?? data);
}

export type SpawnAttachmentInput = {
	mimeType: string;
	data: string;
};

export async function getSession(cfg: ServerConfig, id: string): Promise<DashboardSession> {
	const res = await req(cfg, `${API}/sessions/${encodeURIComponent(id)}`);
	const data = await res.json();
	return mapSession(data?.session ?? data);
}

export async function delegateTask(
	cfg: ServerConfig,
	opts: { projectId: string; brief: string; agent?: string; model?: string; mode: SessionMode; attachments?: SpawnAttachmentInput[]; clientRequestId?: string; backlogRef?: string; onWarnings?: (warnings: string[]) => void },
): Promise<DashboardSession> {
	const res = await req(cfg, `${API}/orchestrators/delegate`, {
		method: "POST",
		headers: opts.attachments?.length ? { "X-AO-Attachment-Upload": "1" } : undefined,
		body: JSON.stringify({
			projectId: opts.projectId,
			brief: opts.brief,
			agent: opts.agent || undefined,
			model: opts.model || undefined,
			mode: opts.mode,
			attachments: opts.attachments?.length ? opts.attachments : undefined,
			clientRequestId: opts.clientRequestId,
			backlogRef: opts.backlogRef || undefined,
		}),
	}, opts.attachments?.length ? ATTACHMENT_REQUEST_TIMEOUT_MS : undefined);
	const data = await res.json();
	if (Array.isArray(data?.warnings) && data.warnings.length) opts.onWarnings?.(data.warnings);
	if (!data?.workerId) throw new Error("Your computer didn't return the new worker. Refresh the board to check whether it started.");
	return getSession(cfg, data.workerId);
}

export async function launchOrchestrator(
	cfg: ServerConfig,
	projectId: string,
	clean = false,
	mode: SessionMode = "chat",
	lane?: string,
): Promise<OrchestratorLink> {
	const res = await req(cfg, `${API}/orchestrators`, {
		method: "POST",
		body: JSON.stringify({ projectId, clean, mode, ...(lane ? { lane } : {}) }),
	});
	const data = await res.json();
	const o = data?.orchestrator ?? {};
	return {
		id: o.id,
		projectId: o.projectId ?? projectId,
		projectName: o.projectName ?? projectId,
		...(lane ? { lane } : {}),
		// Legacy mobile presentation field: here this means "the orchestrator is
		// active", not that a Chat session owns a tmux runtime handle.
		hasRuntime: true,
		isTerminal: false,
		mode: o.mode === "tui" ? "tui" : o.mode === "chat" ? "chat" : mode,
	};
}

export type EndLaneResult = {
	/** Orchestrator the lane's live workers moved to; empty for a dry run. */
	orchestrator: string;
	/** Live workers that moved (dry run: that would move). */
	workers: string[];
};

/**
 * Remove a lane orchestrator and hand its live workers to the default one. With
 * `dryRun` nothing changes: the daemon lists the workers that would move, or
 * refuses when the lane's worktree holds unsaved work.
 */
export async function endLane(cfg: ServerConfig, sessionId: string, options: { dryRun?: boolean } = {}): Promise<EndLaneResult> {
	const res = await req(cfg, `${API}/orchestrators/end`, {
		method: "POST",
		body: JSON.stringify({ sessionId, dryRun: options.dryRun || undefined }),
	});
	const data = await res.json();
	return { orchestrator: data?.orchestrator ?? "", workers: Array.isArray(data?.workers) ? data.workers : [] };
}

/** Replace an orchestrator with a fresh one that continues from its newest state note. Returns the successor's id. */
export async function handoffOrchestrator(cfg: ServerConfig, id: string): Promise<string> {
	const res = await req(cfg, `${API}/orchestrators/${encodeURIComponent(id)}/handoff`, { method: "POST", body: "{}" });
	const data = await res.json();
	return data?.orchestrator?.id ?? "";
}

export async function mergePR(cfg: ServerConfig, pr: DashboardPR): Promise<void> {
	await req(cfg, `${API}/prs/${pr.number}/merge`, { method: "POST" });
}

// Quick reachability probe for the Settings "Test connection" button.
