import { randomUUID } from "node:crypto";
import { mkdir, readFile, rename, rm, stat, writeFile } from "node:fs/promises";
import path from "node:path";
import { isBrowserProfileId } from "../shared/browser-profiles";
import {
	SITE_PERMISSION_KINDS,
	isSitePermissionKind,
	sitePermissionOrigin,
	type BrowserSitePermissionEntry,
	type SitePermissionDecision,
	type SitePermissionKind,
} from "../shared/browser-site-permissions";

const FILE_NAME = "browser-site-permissions.json";
const FILE_VERSION = 1 as const;
const MAX_FILE_BYTES = 1024 * 1024;

// scope -> origin -> kind -> decision
type Decisions = Map<string, Map<string, Map<SitePermissionKind, SitePermissionDecision>>>;

function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null && !Array.isArray(value);
}

function parseDecisions(raw: unknown): Decisions {
	const decisions: Decisions = new Map();
	if (!isRecord(raw) || raw.version !== FILE_VERSION || !isRecord(raw.profiles)) return decisions;
	for (const [profileId, origins] of Object.entries(raw.profiles)) {
		if (!isBrowserProfileId(profileId) || !isRecord(origins)) continue;
		const byOrigin = new Map<string, Map<SitePermissionKind, SitePermissionDecision>>();
		for (const [origin, kinds] of Object.entries(origins)) {
			if (sitePermissionOrigin(origin) !== origin || !isRecord(kinds)) continue;
			const byKind = new Map<SitePermissionKind, SitePermissionDecision>();
			for (const [kind, decision] of Object.entries(kinds)) {
				if (isSitePermissionKind(kind) && (decision === "allow" || decision === "block")) byKind.set(kind, decision);
			}
			if (byKind.size) byOrigin.set(origin, byKind);
		}
		if (byOrigin.size) decisions.set(profileId, byOrigin);
	}
	return decisions;
}

/**
 * Remembered site permission answers, per browser profile and origin, in
 * `<browser profile state dir>/browser-site-permissions.json` (under
 * ~/.tallpine). A scope that is not a profile ID (a temporary session
 * partition) is kept in memory only, for as long as the app runs.
 */
export class BrowserSitePermissionStore {
	private readonly file: string;
	private readonly decisions: Decisions;
	private writes: Promise<void> = Promise.resolve();

	private constructor(stateDir: string, decisions: Decisions) {
		this.file = path.join(stateDir, FILE_NAME);
		this.decisions = decisions;
	}

	static async open(stateDir: string): Promise<BrowserSitePermissionStore> {
		const file = path.join(stateDir, FILE_NAME);
		let decisions: Decisions = new Map();
		try {
			const metadata = await stat(file);
			if (metadata.isFile() && metadata.size <= MAX_FILE_BYTES) {
				decisions = parseDecisions(JSON.parse(await readFile(file, "utf8")));
			}
		} catch {
			// Missing or unreadable: start empty; the next answer rewrites it.
		}
		return new BrowserSitePermissionStore(stateDir, decisions);
	}

	get(scope: string, origin: string, kind: SitePermissionKind): SitePermissionDecision | undefined {
		return this.decisions.get(scope)?.get(origin)?.get(kind);
	}

	list(scope: string, origin: string): BrowserSitePermissionEntry[] {
		const byKind = this.decisions.get(scope)?.get(origin);
		if (!byKind) return [];
		return SITE_PERMISSION_KINDS.flatMap((kind) => {
			const decision = byKind.get(kind);
			return decision ? [{ kind, decision }] : [];
		});
	}

	set(scope: string, origin: string, kinds: readonly SitePermissionKind[], decision: SitePermissionDecision): Promise<void> {
		if (sitePermissionOrigin(origin) !== origin || !kinds.length) return Promise.resolve();
		let byOrigin = this.decisions.get(scope);
		if (!byOrigin) this.decisions.set(scope, (byOrigin = new Map()));
		let byKind = byOrigin.get(origin);
		if (!byKind) byOrigin.set(origin, (byKind = new Map()));
		for (const kind of kinds) byKind.set(kind, decision);
		return this.persist(scope);
	}

	/** Forgets one kind for an origin, or every kind when `kind` is omitted. */
	reset(scope: string, origin: string, kind?: SitePermissionKind): Promise<void> {
		const byOrigin = this.decisions.get(scope);
		const byKind = byOrigin?.get(origin);
		if (!byOrigin || !byKind) return Promise.resolve();
		if (kind) byKind.delete(kind);
		if (!kind || !byKind.size) byOrigin.delete(origin);
		if (!byOrigin.size) this.decisions.delete(scope);
		return this.persist(scope);
	}

	/** Drops a scope: a deleted profile, or a temporary partition that ended. */
	clear(scope: string): Promise<void> {
		if (!this.decisions.delete(scope)) return Promise.resolve();
		return this.persist(scope);
	}

	/** Resolves once every queued write has reached disk. */
	drain(): Promise<void> {
		return this.writes;
	}

	private persist(scope: string): Promise<void> {
		if (!isBrowserProfileId(scope)) return Promise.resolve();
		// Each write serializes the whole current state, so a failed write is
		// repaired by the next one; the queue itself never stays rejected.
		const next = this.writes.then(() => this.write());
		this.writes = next.catch(() => undefined);
		return next;
	}

	private serialize(): string {
		const profiles: Record<string, Record<string, Record<string, SitePermissionDecision>>> = {};
		for (const [scope, byOrigin] of this.decisions) {
			if (!isBrowserProfileId(scope)) continue;
			profiles[scope] = Object.fromEntries(
				[...byOrigin].map(([origin, byKind]) => [origin, Object.fromEntries(byKind)]),
			);
		}
		return `${JSON.stringify({ version: FILE_VERSION, profiles }, null, 2)}\n`;
	}

	private async write(): Promise<void> {
		const directory = path.dirname(this.file);
		await mkdir(directory, { recursive: true, mode: 0o750 });
		const temporary = path.join(directory, `.${FILE_NAME}.${process.pid}.${randomUUID()}.tmp`);
		try {
			await writeFile(temporary, this.serialize(), { mode: 0o600 });
			await rename(temporary, this.file);
		} catch (error) {
			await rm(temporary, { force: true }).catch(() => undefined);
			throw error;
		}
	}
}
