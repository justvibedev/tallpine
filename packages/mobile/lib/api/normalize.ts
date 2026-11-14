import { parseArtifacts, sortArtifacts } from "../artifacts";
import { CI_STATES, MERGEABILITY_STATES, PULL_REQUEST_STATES, REVIEW_DECISIONS } from "@tallpine/product-ui/scm-models";
import type { components } from "@tallpine/shared/api/schema";
import type { DashboardPR, DashboardSession, OrchestratorLink, ProjectInfo } from "./sessions";

export const KANBAN_COLUMNS = ["building", "validating", "needs_review", "ready", "archive"] as const;
export type KanbanColumn = (typeof KANBAN_COLUMNS)[number];

export function isKanbanColumn(value: string | null | undefined): value is KanbanColumn {
	return !!value && (KANBAN_COLUMNS as readonly string[]).includes(value);
}

// ---- Wire types (this repo's Go daemon, /api/v1/*) --------------------------
//
// The app UI speaks Tallpine's OG "DashboardSession" shape; this daemon speaks a
// leaner read model. The maps below translate the daemon's SessionView/PR facts
// into the shapes the screens expect, so the rest of the app is unchanged.

type Schema = components["schemas"];

/** The daemon's PR facts on a session. Fields are optional because an older daemon omits some. */
export type WirePR = Partial<Omit<Schema["SessionPRFacts"], "url" | "number">> & Pick<Schema["SessionPRFacts"], "url" | "number">;

/** The daemon's session read model, as the phone tolerates it (every field but `id` may be absent). */
export type WireSession = Partial<Omit<Schema["SessionView"], "id" | "prs">> & { id: string; prs?: WirePR[] };

export type WireProject = Pick<Schema["Project"], "id" | "name"> & Partial<Pick<Schema["Project"], "kind">> & { sessionPrefix?: string };

export function mapProjectKind(kind?: string): ProjectInfo["kind"] {
	switch (kind) {
		case "single_repo":
		case "workspace":
		case "scratch":
			return kind;
		default:
			return undefined;
	}
}

/** A wire value that product-ui's PR vocabulary lists, else the fallback. */
function oneOf<T extends string>(allowed: readonly T[], value: string | undefined, fallback: T): T {
	return allowed.includes(value as T) ? (value as T) : fallback;
}

export function mapPR(pr: WirePR): DashboardPR {
	const ci = oneOf(CI_STATES, pr.ci, "unknown");
	const mergeability = oneOf(MERGEABILITY_STATES, pr.mergeability, "unknown");
	const decision = oneOf(REVIEW_DECISIONS, pr.review, "none");
	const state = oneOf(PULL_REQUEST_STATES, pr.state, "open");
	return {
		number: pr.number,
		url: pr.url,
		// The phone has no draft state of its own: a draft is an open PR flagged isDraft.
		state: state === "draft" ? "open" : state,
		isDraft: state === "draft",
		ciStatus: ci === "unknown" ? "none" : ci,
		reviewDecision: decision === "review_required" ? "pending" : decision,
		mergeability: { state: mergeability, mergeable: mergeability === "mergeable" },
		unresolvedThreads: pr.reviewComments ? 1 : 0,
	};
}

function activityString(a: unknown): string | null {
	if (typeof a === "string") return a || null;
	if (a && typeof a === "object" && "state" in a && typeof (a as { state: unknown }).state === "string") {
		return (a as { state: string }).state || null;
	}
	return null;
}

function activityLastAt(a: unknown): string | undefined {
	if (!a || typeof a !== "object" || !("lastActivityAt" in a)) return undefined;
	const value = (a as { lastActivityAt: unknown }).lastActivityAt;
	return typeof value === "string" && value ? value : undefined;
}

export function mapSession(s: WireSession): DashboardSession {
	const prs = (s.prs ?? []).map(mapPR);
	return {
		id: s.id,
		// Only the single-session read lists the files; a board row carries hasArtifacts.
		artifacts: s.artifactFiles === undefined ? undefined : sortArtifacts(parseArtifacts(s.artifactFiles)),
		hasArtifacts: s.hasArtifacts === true || (s.artifactFiles?.length ?? 0) > 0,
		// "" rather than undefined: every consumer treats the id as a string, and
		// `shortLabel(undefined)` threw inside render and took the whole board down.
		projectId: s.projectId ?? "",
		terminalHandleId: s.terminalHandleId,
		status: s.status ?? null,
		kanbanColumn: isKanbanColumn(s.kanbanColumn) ? s.kanbanColumn : null,
		...(s.needsInputAudience === "orchestrator" || s.needsInputAudience === "human" ? { needsInputAudience: s.needsInputAudience } : {}),
		displayStatus: s.displayStatus?.trim() || null,
		activity: activityString(s.activity),
		harness: s.harness ?? null,
		reviewerHarness: s.reviewerHarness ?? null,
		reviewerConfig: s.reviewerConfig,
		mode: s.mode === "chat" ? "chat" : "tui",
		branch: s.branch ?? null,
		issueId: s.issueId ?? null,
		issueTitle: null,
		userPrompt: null,
		displayName: s.displayName ?? null,
		summary: null,
		createdAt: s.createdAt ?? "",
		lastActivityAt: activityLastAt(s.activity) ?? s.updatedAt ?? s.createdAt ?? "",
		lastEventAt: s.lastEventAt || undefined,
		pr: prs[0] ?? null,
		prs,
		previewUrl: s.previewUrl ?? null,
		isTerminated: !!s.isTerminated,
		provisionState: s.provisionState,
		provisionError: s.provisionError,
		provisionSteps: s.provisionSteps?.length ? s.provisionSteps : undefined,
		branchNaming: !!s.branchNaming,
		isPinned: !!s.isPinned,
		pinnedAt: s.pinnedAt ?? null,
		kind: s.kind || undefined,
		updatedAt: s.updatedAt || undefined,
		autoReviewEnabled: !!s.autoReviewEnabled,
		autoInjectReview: s.autoInjectReview ?? true,
		autoInjectCI: s.autoInjectCI ?? true,
		...(s.workerLane ? { workerLane: s.workerLane } : {}),
		...(s.kind === "orchestrator" && s.lane ? { lane: s.lane } : {}),
	};
}

function mapOrchestrator(s: WireSession, projectName: string): OrchestratorLink {
	return {
		id: s.id,
		projectId: s.projectId ?? "",
		terminalHandleId: s.terminalHandleId,
		projectName,
		status: s.status ?? null,
		activity: activityString(s.activity),
		harness: s.harness ?? null,
		mode: s.mode === "chat" ? "chat" : "tui",
		updatedAt: s.updatedAt ?? null,
		hasRuntime: !s.isTerminated,
		isTerminal: !!s.isTerminated,
		isRestorable: !!s.isTerminated,
		...(s.lane ? { lane: s.lane } : {}),
	};
}

/**
 * The orchestrator links the phone shows. The daemon marks the current
 * orchestrator of each (project, lane) (`currentOrchestrator`); this only orders
 * them (per project the default lane first, then live before stopped, so
 * `links[0]` is what a single-orchestrator screen shows) and adds the other live
 * default-lane orchestrators after their main one, labelled, so each stays reachable.
 */
export function orchestratorLinks(raw: readonly WireSession[], nameOf: ReadonlyMap<string, string>): OrchestratorLink[] {
	// A daemon that predates the mark sends none; then every project row counts as
	// current (respawns show as duplicates, but nothing disappears).
	const marked = raw.some((s) => s.currentOrchestrator);
	const isCurrent = (s: WireSession) => (marked ? !!s.currentOrchestrator : !(s.lane && s.isTerminated));
	const current = raw.filter((s) => s.projectId && isCurrent(s));
	const rank = (s: WireSession) => (s.lane ? 2 : 0) + (s.isTerminated ? 1 : 0);
	const out = current
		.map((s, index) => ({ s, index }))
		.sort((a, b) => rank(a.s) - rank(b.s) || a.index - b.index)
		.map(({ s }) => mapOrchestrator(s, nameOf.get(s.projectId ?? "") ?? s.projectId ?? ""));
	for (const main of current) {
		if (main.lane || main.isTerminated) continue;
		const projectId = main.projectId ?? "";
		const extras = raw
			.filter((s) => s.projectId === projectId && !s.lane && !s.isTerminated && !isCurrent(s))
			.map((s) => ({ ...mapOrchestrator(s, nameOf.get(projectId) ?? projectId), extraLabel: extraOrchestratorLabel(s.id) }));
		const at = out.findIndex((o) => o.id === main.id) + 1;
		out.splice(at, 0, ...extras);
	}
	return out;
}

function extraOrchestratorLabel(id: string): string {
	const n = /(\d+)$/.exec(id)?.[1];
	return n ? `Orchestrator ${n}` : id;
}

// ---- Derived helpers --------------------------------------------------------

// Derived status helpers live in sessionStatus.ts so pure modules can import
// them; re-exported here because call sites reach for them via api.
export { attentionOf, sessionTitle, wantsAttention } from "../sessionStatus";

// Project ids carry a generated hash suffix (`my-app_98d163a851`), which is
// wider than a phone card. Middle-truncate: a plain tail-cut would drop the hash
// and make two projects sharing a base name render identically, so keep the head
// (the readable part) AND the tail (the part that disambiguates).
const MAX_LABEL = 20;

export function shortLabel(value: string, max = MAX_LABEL): string {
	if (value.length <= max) return value;
	const keep = max - 1; // room for the ellipsis
	const head = Math.ceil(keep / 2);
	const tail = Math.floor(keep / 2);
	return `${value.slice(0, head)}…${value.slice(value.length - tail)}`;
}
