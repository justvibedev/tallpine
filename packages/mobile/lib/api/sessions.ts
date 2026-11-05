import type { components } from "@tallpine/shared/api/schema";
import type { ArchiveSummary } from "../archive/archiveSummary";
import type { ServerConfig } from "../config";
import { API, req } from "./http";
import { orchestratorLinks, mapProjectKind, mapSession, type KanbanColumn, type WireProject, type WireSession } from "./normalize";
import type { ReviewerAgentConfig } from "./reviews";

export type SessionMode = "chat" | "tui";

export type DashboardPR = {
	number: number;
	url: string;
	title?: string;
	owner?: string;
	repo?: string;
	branch?: string;
	baseBranch?: string;
	isDraft?: boolean;
	state?: "open" | "merged" | "closed";
	additions?: number;
	deletions?: number;
	changedFiles?: number;
	ciStatus?: "pending" | "passing" | "failing" | "none";
	reviewDecision?: "approved" | "changes_requested" | "pending" | "none";
	mergeability?: {
		state?: "unknown" | "mergeable" | "conflicting" | "blocked" | "unstable";
		mergeable?: boolean;
		ciPassing?: boolean;
		approved?: boolean;
		noConflicts?: boolean;
		blockers?: string[];
	};
	unresolvedThreads?: number;
};

/**
 * Where the daemon placed a session in its delivery lifecycle, derived
 * server-side from durable facts and independently of `status`.
 *
 * Mirrors the desktop contract (backend/pkg/contract/kanban.go, surfaced to the
 * renderer as KANBAN_COLUMNS). The daemon has always sent this; mobile simply
 * discarded it and re-derived its own grouping, which is how the two boards
 * came to disagree about the same session.
 */
export type SessionProvisionStep = components["schemas"]["SessionProvisionStep"];

export type DashboardSession = {
	/** Files in the session's artifact directory, newest first. */
	artifacts?: import("../artifacts").SessionArtifact[];
	/** Whether the artifact directory has files; the board row says so without listing them. */
	hasArtifacts?: boolean;
	id: string;
	projectId: string;
	/** Opaque daemon runtime handle used only for terminal mux operations. */
	terminalHandleId?: string;
	status: string | null;
	/** The daemon's own board placement. Absent on a daemon too old to send it. */
	kanbanColumn?: KanbanColumn | null;
	/** Who a needs_input state waits on, as the daemon derived it. Absent from older daemons: treat as the human. */
	needsInputAudience?: "orchestrator" | "human";
	/**
	 * The daemon's phrase for what is happening inside that column — richer than
	 * `status` ("Awaiting PR", "Fixing CI failures"), and already localized on the
	 * wire so clients print it without a mapping table.
	 */
	displayStatus?: string | null;
	activity?: string | null;
	// Which agent CLI drives this session (claude-code, codex, …). Parsed off the
	// wire but discarded until the orchestrator tab needed it for brand marks.
	harness?: string | null;
	reviewerHarness?: string | null;
	reviewerConfig?: ReviewerAgentConfig;
	/** Controller currently committed for this Tallpine session. */
	mode: SessionMode;
	branch: string | null;
	issueId: string | null;
	issueUrl?: string | null;
	issueLabel?: string | null;
	issueTitle: string | null;
	userPrompt: string | null;
	displayName: string | null;
	summary: string | null;
	createdAt: string;
	lastActivityAt: string;
	/**
	 * When something a person would notice last happened (activity-state change,
	 * PR lifecycle or CI change, review). Absent from daemons that predate it;
	 * read it through `eventAtOf`, which falls back.
	 */
	lastEventAt?: string;
	pr?: DashboardPR | null;
	prs?: DashboardPR[];
	metadata?: Record<string, string>;
	// Browser-preview target the daemon detected/served for this session (e.g. a
	// dist/index.html entrypoint). Consumed by the in-app browser.
	previewUrl?: string | null;
	// Whether the runtime is dead. The board archives on this rather than on a
	// finished status: a merged session whose agent is still running belongs on
	// the board, only a terminated one belongs in the archive.
	isTerminated?: boolean;
	provisionState?: "provisioning" | "ready" | "failed";
	provisionError?: string;
	/** The daemon's start-up stages while a Chat session provisions. Absent from older daemons. */
	provisionSteps?: SessionProvisionStep[];
	/** Set while the task's generated branch name is on the way; keep `branch` private meanwhile. */
	branchNaming?: boolean;
	/** worker or orchestrator. Absent from rows built before the daemon named kinds. */
	kind?: string;
	/** When the stored row last changed. The archive is ordered by it, as on desktop. */
	updatedAt?: string;
	isPinned?: boolean;
	pinnedAt?: string | null;
	/** Automatically review each new pull-request head. */
	autoReviewEnabled?: boolean;
	/** Automatically deliver completed review feedback to the worker. */
	autoInjectReview?: boolean;
	/** Automatically deliver failing CI checks to the worker. */
	autoInjectCI?: boolean;
	/** Backlog ref of the lane a worker was spawned in; unset for workers outside any lane. */
	workerLane?: string;
};

export type OrchestratorLink = {
	id: string;
	projectId: string;
	/** Opaque daemon runtime handle used only for terminal mux operations. */
	terminalHandleId?: string;
	projectName: string;
	status?: string | null;
	activity?: string | null;
	/** Agent CLI driving this orchestrator — drives its brand mark. */
	harness?: string | null;
	mode: SessionMode;
	updatedAt?: string | null;
	runtimeState?: string | null;
	hasRuntime?: boolean;
	isTerminal?: boolean;
	isRestorable?: boolean;
	/** Backlog ref of the lane this orchestrator owns (e.g. "B12"); unset for the project's default orchestrator. */
	lane?: string;
	/** Chip label for a live orchestrator that has no lane but is not the project's main one (a second default-lane orchestrator). */
	extraLabel?: string;
};

export type ProjectInfo = {
	id: string;
	name: string;
	kind?: "single_repo" | "workspace" | "scratch";
	sessionPrefix?: string;
};

export type ProjectDetail = ProjectInfo & {
	agent?: string;
	config?: {
		agentConfig?: { model?: string };
		worker?: { agent?: string; agentConfig?: { model?: string } };
		reviewers?: { harness?: string }[];
	};
};

export type DashboardStats = {
	totalSessions?: number;
	workingSessions?: number;
	openPRs?: number;
	needsReview?: number;
};

export type SessionsResponse = {
	sessions: DashboardSession[];
	orchestrators: OrchestratorLink[];
	orchestratorId: string | null;
	stats: DashboardStats;
	// Returned here so callers don't fetch /projects a second time — getSessions
	// already needs it to label orchestrators.
	//
	// Null when the /projects request itself failed, which is NOT the same fact
	// as an empty list: the board judges a saved project filter against this, so
	// folding a failure into [] told it every project was gone (#5058 review).
	projects: ProjectInfo[] | null;
	/**
	 * The archive's size, when the daemon pages it: `sessions` then holds live
	 * workers only and archived ones are fetched with getArchivedSessions. Null on
	 * (or absent on) a daemon that predates paging, whose `sessions` still carries
	 * the archive.
	 */
	archive?: ArchiveSummary | null;
};


export type { ArchiveSummary } from "../archive/archiveSummary";

/** One page of a machine's archive, in the daemon's order (pinned, then most recently updated). */
export type ArchivePage = { sessions: DashboardSession[]; nextCursor: string | null };

/** Archived workers fetched per page when Archive is opened. */
export const ARCHIVE_PAGE_SIZE = 50;

// Machines whose daemon answered the live-only poll without an archive summary.
// They predate paging, so they are polled for the full list as before. Checked
// again now and then, so a daemon upgraded under a running app is picked up.
const legacyArchiveHosts = new Map<string, number>();
const LEGACY_ARCHIVE_RECHECK_MS = 15 * 60_000;

/** Forget which machines answered like an older daemon. For tests. */
export function resetArchivePagingProbes(): void {
	legacyArchiveHosts.clear();
}

function archiveHostKey(cfg: ServerConfig): string {
	return cfg.hostId ?? `${cfg.host}:${cfg.httpPort}`;
}

function wireArchiveSummary(raw: unknown): ArchiveSummary | null {
	if (!raw || typeof raw !== "object") return null;
	const { total, byProject } = raw as { total?: unknown; byProject?: unknown };
	if (typeof total !== "number") return null;
	const counts: Record<string, number> = {};
	if (byProject && typeof byProject === "object") {
		for (const [project, count] of Object.entries(byProject)) if (typeof count === "number") counts[project] = count;
	}
	return { total, byProject: counts };
}

// ---- Reads ------------------------------------------------------------------

export async function getProjects(cfg: ServerConfig, init?: Pick<RequestInit, "signal">): Promise<ProjectInfo[]> {
	const res = await req(cfg, `${API}/projects`, init);
	const data = await res.json();
	const projects = Array.isArray(data?.projects) ? data.projects : [];
	return projects.map((p: WireProject) => ({
		id: p.id,
		name: p.name,
		kind: mapProjectKind(p.kind),
		sessionPrefix: p.sessionPrefix,
	}));
}

export async function getProject(cfg: ServerConfig, id: string): Promise<ProjectDetail> {
	const res = await req(cfg, `${API}/projects/${encodeURIComponent(id)}`);
	const data = await res.json();
	return (data?.project ?? data) as ProjectDetail;
}

export async function getSessions(cfg: ServerConfig, _projectId?: string, init?: Pick<RequestInit, "signal">): Promise<SessionsResponse> {
	// The daemon exposes sessions and orchestrators as two lists. Fetch both,
	// keep worker sessions for the board, and map orchestrators for their screen.
	//
	// /sessions is probed FIRST, alone, rather than fanning out in one Promise.all.
	// The daemon locks a device out for a minute after 5 failed auths, so a stale
	// password used to cost 4 failures per poll tick (this call's three requests
	// plus the caller's own /projects) — enough to arm the lockout in one or two
	// ticks and make the user's next action, typically scanning a fresh pairing
	// code, fail with 429 before the new password was ever checked. Probing first
	// caps a bad-credential tick at a single failed attempt.
	//
	// The poll asks for live workers only, with the archive's size: the archive is
	// most of a long-running machine's sessions, and over a tunnel the poll runs
	// every two seconds. A daemon that predates paging leaves the summary out;
	// that machine is then polled for everything, as before.
	const hostKey = archiveHostKey(cfg);
	const legacySince = legacyArchiveHosts.get(hostKey);
	const tryPaged = legacySince === undefined || Date.now() - legacySince > LEGACY_ARCHIVE_RECHECK_MS;
	let sessRes = await req(cfg, tryPaged ? `${API}/sessions?active=true&kind=worker` : `${API}/sessions`, init);
	let sessData = await sessRes.json();
	let archive = tryPaged ? wireArchiveSummary(sessData?.archived) : null;
	if (tryPaged) {
		if (archive) legacyArchiveHosts.delete(hostKey);
		else {
			legacyArchiveHosts.set(hostKey, Date.now());
			// A daemon older still ignores `active` and has already sent everything,
			// archived rows included; only one that filtered must be asked again.
			const ignoredFilter = Array.isArray(sessData?.sessions) && sessData.sessions.some((s: WireSession) => s.isTerminated);
			if (!ignoredFilter) {
				sessRes = await req(cfg, `${API}/sessions`, init);
				sessData = await sessRes.json();
			}
		}
	}
	// A failed /projects is reported as null rather than [] so a caller can tell
	// "this daemon has no projects" from "we could not ask". It stays caught: an
	// older daemon, or one blip on that one route, must not knock the board
	// offline when /sessions answered fine.
	const [orchRes, projects] = await Promise.all([
		req(cfg, `${API}/orchestrators`, init),
		getProjects(cfg, init).catch(() => null),
	]);
	const orchData = await orchRes.json();
	const nameOf = new Map((projects ?? []).map((p) => [p.id, p.name]));

	const rawSessions: WireSession[] = Array.isArray(sessData?.sessions) ? sessData.sessions : [];
	const rawOrchestrators: WireSession[] = Array.isArray(orchData?.sessions) ? orchData.sessions : [];

	const sessions = rawSessions.filter((s) => s.kind !== "orchestrator").map(mapSession);

	const orchestrators = orchestratorLinks(rawOrchestrators, nameOf);

	return { sessions, orchestrators, orchestratorId: null, stats: {}, projects, archive };
}

/**
 * One page of a machine's archived workers. Only for a daemon whose poll
 * reported an archive summary (see getSessions); an older one carries its
 * archive in the poll itself.
 */
export async function getArchivedSessions(
	cfg: ServerConfig,
	options: { cursor?: string | null; projectId?: string; limit?: number } = {},
	init?: Pick<RequestInit, "signal">,
): Promise<ArchivePage> {
	const query = new URLSearchParams({ active: "false", kind: "worker", limit: String(options.limit ?? ARCHIVE_PAGE_SIZE) });
	if (options.cursor) query.set("cursor", options.cursor);
	if (options.projectId) query.set("project", options.projectId);
	const res = await req(cfg, `${API}/sessions?${query}`, init);
	const data = await res.json();
	const raw: WireSession[] = Array.isArray(data?.sessions) ? data.sessions : [];
	return {
		// The daemon has already filtered these; kept defensively so a row that is
		// live by the time it lands can never be drawn as archived.
		sessions: raw.filter((s) => s.kind !== "orchestrator" && s.isTerminated).map(mapSession),
		nextCursor: typeof data?.nextCursor === "string" && data.nextCursor ? data.nextCursor : null,
	};
}

// ---- Session artifacts (read-only; the single-session read lists them) ------

export type { SessionArtifact } from "../artifacts";

export async function getSessionArtifacts(cfg: ServerConfig, sessionId: string): Promise<import("../artifacts").SessionArtifact[]> {
	const res = await req(cfg, `${API}/sessions/${encodeURIComponent(sessionId)}`);
	const data = await res.json();
	const files = (data?.session ?? data)?.artifactFiles;
	return Array.isArray(files) ? files : [];
}

export async function pingServer(cfg: ServerConfig): Promise<number> {
	const res = await req(cfg, `${API}/sessions`);
	const data = await res.json();
	return Array.isArray(data?.sessions) ? data.sessions.length : 0;
}

