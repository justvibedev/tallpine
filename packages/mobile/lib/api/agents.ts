import type { components } from "@tallpine/shared/api/schema";
import type { ServerConfig } from "../config";
import { API, req } from "./http";
import type { SessionMode } from "./sessions";

// ---- Agent catalog ----------------------------------------------------------

export type AgentInfo = components["schemas"]["AgentInfo"];

export type AgentCatalog = {
	supported: AgentInfo[];
	installed: AgentInfo[];
	authorized: AgentInfo[];
};

export type AgentModelInfo = components["schemas"]["AgentModelInfo"];
export type AgentModelCatalog = components["schemas"]["AgentModelsResponse"];

export type TallpineSettings = {
	defaultSessionMode: SessionMode;
	chatHarnesses: string[];
};

export async function getSettings(cfg: ServerConfig): Promise<TallpineSettings> {
	const res = await req(cfg, `${API}/settings`);
	const data = await res.json();
	return {
		defaultSessionMode: data?.defaultSessionMode === "tui" ? "tui" : "chat",
		chatHarnesses: Array.isArray(data?.chatHarnesses) ? data.chatHarnesses.filter((value: unknown): value is string => typeof value === "string") : [],
	};
}

export type DesktopUpdateStatus = {
	installedCommit: string;
	candidate: string;
	updateAvailable: boolean;
	running: boolean;
	last?: { state: string; commit: string; message: string };
};

/** The Mac's "Update & restart" status: installed vs the newest CI-passed build. */
export async function getDesktopUpdate(cfg: ServerConfig): Promise<DesktopUpdateStatus> {
	const res = await req(cfg, `${API}/update`);
	const data = await res.json();
	return {
		installedCommit: typeof data?.installedCommit === "string" ? data.installedCommit : "",
		candidate: typeof data?.candidate === "string" ? data.candidate : "",
		updateAvailable: data?.updateAvailable === true,
		running: data?.running === true,
		last: data?.last && typeof data.last.state === "string"
			? { state: data.last.state, commit: String(data.last.commit ?? ""), message: String(data.last.message ?? "") }
			: undefined,
	};
}

/** Starts the detached update job for a CI-passed commit; the Mac's app and daemon restart. */
export async function startDesktopUpdate(cfg: ServerConfig, commit: string): Promise<void> {
	await req(cfg, `${API}/update`, {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify({ commit }),
	});
}

export async function getAgents(cfg: ServerConfig): Promise<AgentCatalog> {
	const res = await req(cfg, `${API}/agents`);
	const data = await res.json();
	return {
		supported: Array.isArray(data?.supported) ? data.supported : [],
		installed: Array.isArray(data?.installed) ? data.installed : [],
		authorized: Array.isArray(data?.authorized) ? data.authorized : [],
	};
}

export async function refreshAgents(cfg: ServerConfig): Promise<AgentCatalog> {
	const res = await req(cfg, `${API}/agents/refresh`, { method: "POST" });
	const data = await res.json();
	return {
		supported: Array.isArray(data?.supported) ? data.supported : [],
		installed: Array.isArray(data?.installed) ? data.installed : [],
		authorized: Array.isArray(data?.authorized) ? data.authorized : [],
	};
}

// The daemon answers an agent it has never discovered with an empty catalog
// marked refreshing while discovery runs in the background, rather than making
// the picker wait. Re-read until the discovered list arrives.
const MODEL_CATALOG_REFRESH_POLL_MS = 1_500;

function pollDelay(signal: AbortSignal | undefined): Promise<void> {
	return new Promise((resolve) => {
		const finish = () => {
			clearTimeout(timer);
			signal?.removeEventListener("abort", finish);
			resolve();
		};
		const timer = setTimeout(finish, MODEL_CATALOG_REFRESH_POLL_MS);
		signal?.addEventListener("abort", finish, { once: true });
		if (signal?.aborted) finish();
	});
}

/**
 * Loads an agent's model catalog, re-reading while the daemon is still
 * discovering it. There is no attempt cap: the daemon bounds discovery (a
 * readiness wait, then discovery's own timeout) and then saves a result, so a
 * cold load ends with models or an error. `signal` stops polling when the view
 * that asked closes.
 */
export async function getAgentModels(cfg: ServerConfig, agent: string, projectId?: string, signal?: AbortSignal): Promise<AgentModelCatalog> {
	const query = projectId ? `?projectId=${encodeURIComponent(projectId)}` : "";
	for (;;) {
		const res = await req(cfg, `${API}/agents/${encodeURIComponent(agent)}/models${query}`);
		const catalog = await res.json() as AgentModelCatalog;
		const discovering = catalog?.refreshState === "refreshing" && !catalog.models?.length;
		if (!discovering || signal?.aborted) return catalog;
		await pollDelay(signal);
		if (signal?.aborted) return catalog;
	}
}

export async function refreshAgentModels(cfg: ServerConfig, agent: string, projectId?: string): Promise<AgentModelCatalog> {
	const query = projectId ? `?projectId=${encodeURIComponent(projectId)}` : "";
	const res = await req(cfg, `${API}/agents/${encodeURIComponent(agent)}/models/refresh${query}`, { method: "POST" });
	return await res.json() as AgentModelCatalog;
}

