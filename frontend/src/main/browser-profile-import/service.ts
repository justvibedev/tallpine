import { readFile } from "node:fs/promises";
import type { BrowserHistoryEntry } from "../browser-history-store";
import { discoverChromiumProfiles, readChromiumCookies, readChromiumHistory } from "./chromium";
import { ChromiumCookieDecryptor } from "./chromium-decrypt";
import { BrowserProfileImportOptions, DiscoveryContext, ImportSession, ImportedCookie, InternalDiscoveryOptions, InternalSource, InternalSourceProfile, ReadProfileData, SOURCE_ID_PATTERN, SourceBudget, cookieCapability, existingRealDirectory, isAccessDenied, isRecord, opaqueSourceId, redactImportPaths, removeEmptyDirectory, removeStaleStagingDirectories, throwIfImportAborted, validateRequest } from "./common";
import { dedupeCookies, mergeWarnings, setCookies } from "./cookie-rows";
import { DESCRIPTORS } from "./descriptors";
import { discoverFirefoxProfiles, readFirefoxCookies, readFirefoxHistory } from "./firefox";
import { discoverSafariProfiles, readSafariCookies, readSafariHistory } from "./safari";
import { findDatabase, snapshotFile, snapshotSQLite } from "./snapshot";
/* Split out of browser-profile-import.ts (B297, P53). */

import { randomUUID } from "node:crypto";
import {
mkdir,
rm
} from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import {
type BrowserImportDiscovery,
type BrowserImportDiscoveryRequest,
type BrowserImportProgress,
type BrowserImportRequest,
type BrowserImportResult,
type BrowserImportResultEntry,
type BrowserImportWarning
} from "../../shared/browser-profile-import";
import {
browserProfilePartition,
type BrowserProfile
} from "../../shared/browser-profiles";

export class BrowserProfileImportService {
	private readonly context: DiscoveryContext;
	private readonly now: () => Date;
	private readonly stagingInstanceId = randomUUID();
	private activeImport: Promise<BrowserImportResult> | null = null;
	private activeController: AbortController | null = null;
	private disposePromise: Promise<void> | null = null;
	private disposed = false;

	constructor(private readonly options: BrowserProfileImportOptions) {
		this.context = {
			platform: options.platform ?? process.platform,
			homeDir: options.homeDir ?? os.homedir(),
			env: options.env ?? process.env,
		};
		this.now = options.now ?? (() => new Date());
	}

	async initialize(): Promise<void> {
		if (this.disposed) throw new Error("Browser profile import is unavailable.");
		if (this.activeImport) throw new Error("Another browser import is already running.");
		await removeStaleStagingDirectories(this.stagingBase());
		await removeEmptyDirectory(this.stagingBase());
	}

	dispose(): Promise<void> {
		if (this.disposePromise) return this.disposePromise;
		this.disposed = true;
		this.activeController?.abort();
		const activeImport = this.activeImport;
		this.disposePromise = (async () => {
			if (activeImport) await activeImport.catch(() => undefined);
			await rm(this.stagingRoot(), { recursive: true, force: true }).catch(() => undefined);
			await removeEmptyDirectory(this.stagingBase());
		})();
		return this.disposePromise;
	}

	private stagingBase(): string {
		return path.join(this.options.stateDir, "browser-import-staging");
	}

	private stagingRoot(): string {
		return path.join(this.stagingBase(), this.stagingInstanceId);
	}

	async discover(request: BrowserImportDiscoveryRequest = {}): Promise<BrowserImportDiscovery> {
		const warnings: NonNullable<BrowserImportDiscovery["warnings"]> = [];
		const sourceId = typeof request.sourceId === "string" && SOURCE_ID_PATTERN.test(request.sourceId)
			? request.sourceId
			: undefined;
		const sources = await this.discoverInternal(warnings, {
			...(sourceId ? { sourceId, includeProtectedSource: true } : {}),
		});
		return { sources: sources.map((source) => source.public), ...(warnings.length ? { warnings } : {}) };
	}

	private async discoverInternal(
		warnings: NonNullable<BrowserImportDiscovery["warnings"]> = [],
		options: InternalDiscoveryOptions = {},
	): Promise<InternalSource[]> {
		const sources: InternalSource[] = [];
		for (const descriptor of DESCRIPTORS) {
			for (const candidate of descriptor.roots(this.context)) {
				const root = await existingRealDirectory(candidate);
				if (!root) continue;
				const publicSourceId = opaqueSourceId(`${descriptor.id}:source`, root);
				if (options.sourceId && publicSourceId !== options.sourceId) continue;
				const capability = cookieCapability(descriptor.family, this.context.platform);
				if (descriptor.family === "safari" && !options.includeProtectedSource) {
					sources.push({
						descriptor,
						root,
						profiles: [],
						public: {
							id: publicSourceId,
							name: descriptor.name,
							family: descriptor.family,
							profiles: [],
							profilesDeferred: true,
							cookieSupport: capability.support,
							cookieSupportReason: capability.reason,
							historySupport: true,
						},
					});
					break;
				}
				let profiles: InternalSourceProfile[];
				try {
					profiles = descriptor.family === "chromium"
						? await discoverChromiumProfiles(descriptor, root)
						: descriptor.family === "firefox"
							? await discoverFirefoxProfiles(descriptor, root)
							: await discoverSafariProfiles(descriptor, root, this.stagingRoot(), this.options.sourceLstat);
				} catch (error) {
					if (descriptor.family !== "safari" || !isAccessDenied(error)) throw error;
					warnings.push("safari-access-denied");
					continue;
				}
				if (profiles.length === 0) continue;
				sources.push({
					descriptor,
					root,
					profiles,
					public: {
						id: publicSourceId,
						name: descriptor.name,
						family: descriptor.family,
						profiles: profiles.map(({ id, name, default: isDefault }) => ({ id, name, default: isDefault })),
						cookieSupport: capability.support,
						cookieSupportReason: capability.reason,
						historySupport: true,
					},
				});
				break;
			}
		}
		return sources;
	}

	async import(
		rawRequest: BrowserImportRequest,
		onProgress: (progress: BrowserImportProgress) => void,
	): Promise<BrowserImportResult> {
		if (this.disposed) throw new Error("Browser profile import is unavailable.");
		if (this.activeImport) throw new Error("Another browser import is already running.");
		const controller = new AbortController();
		const pending = this.runImport(rawRequest, onProgress, controller.signal);
		this.activeController = controller;
		this.activeImport = pending;
		try {
			return await pending;
		} finally {
			if (this.activeImport === pending) {
				this.activeImport = null;
				this.activeController = null;
			}
		}
	}

	private async runImport(
		rawRequest: BrowserImportRequest,
		onProgress: (progress: BrowserImportProgress) => void,
		signal: AbortSignal,
	): Promise<BrowserImportResult> {
		const staging = path.join(this.stagingRoot(), randomUUID());
		let sourceRoots: string[] = [];
		try {
			throwIfImportAborted(signal);
			const sourceId = isRecord(rawRequest) && typeof rawRequest.sourceId === "string" && SOURCE_ID_PATTERN.test(rawRequest.sourceId)
				? rawRequest.sourceId
				: undefined;
			const sources = await this.discoverInternal(undefined, {
				...(sourceId ? { sourceId, includeProtectedSource: true } : {}),
			});
			throwIfImportAborted(signal);
			sourceRoots = sources.map((source) => source.root);
			const request = validateRequest(rawRequest, sources, this.options.profileStore.profiles);
			const source = sources.find((candidate) => candidate.public.id === request.sourceId)!;
			const selected = request.profileIds.map((id) => source.profiles.find((profile) => profile.id === id)!);
			onProgress({ requestId: request.requestId, phase: "preparing", completed: 0, total: selected.length });
			await mkdir(staging, { recursive: true, mode: 0o700 });
			const budget = new SourceBudget();
			if (request.includeCookies && source.descriptor.family === "chromium" && this.context.platform === "darwin") {
				onProgress({ requestId: request.requestId, phase: "permission", completed: 0, total: selected.length });
			}
			const decryptor = request.includeCookies
				? await ChromiumCookieDecryptor.create(source, this.context.platform, signal)
				: null;
			const readData: ReadProfileData[] = [];
			for (const [index, profile] of selected.entries()) {
				throwIfImportAborted(signal);
				onProgress({ requestId: request.requestId, phase: "reading", completed: index, total: selected.length });
				readData.push(await readProfileData(source, profile, request, staging, budget, decryptor, this.now()));
				throwIfImportAborted(signal);
				onProgress({ requestId: request.requestId, phase: "reading", completed: index + 1, total: selected.length });
			}
			return await this.commitImport(source, request, readData, onProgress, signal);
		} catch (error) {
			throw redactImportPaths(error, sourceRoots, this.stagingBase());
		} finally {
			await rm(staging, { recursive: true, force: true }).catch(() => undefined);
			await removeEmptyDirectory(this.stagingRoot());
		}
	}

	private async commitImport(
		source: InternalSource,
		request: BrowserImportRequest,
		readData: ReadProfileData[],
		onProgress: (progress: BrowserImportProgress) => void,
		signal: AbortSignal,
	): Promise<BrowserImportResult> {
		const destination = request.destination;
		const groups =
			destination.mode === "merge"
				? [{ name: destination.name, data: readData }]
				: readData.map((data) => ({ name: destination.names[data.profile.id]!, data: [data] }));
		const created: BrowserProfile[] = [];
		const results: BrowserImportResultEntry[] = [];
		try {
			for (const [index, group] of groups.entries()) {
				throwIfImportAborted(signal);
				onProgress({ requestId: request.requestId, phase: "importing", completed: index, total: groups.length });
				const profile = await this.options.profileStore.createProfile(group.name);
				created.push(profile);
				throwIfImportAborted(signal);
				const history = group.data.flatMap((data) => data.history);
				const cookieSelection = dedupeCookies(group.data.flatMap((data) => data.cookies));
				const historyOutcome = request.includeHistory
					? await this.options.historyStore.mergeImportedEntries(profile.id, history)
					: { imported: 0, truncated: 0 };
				throwIfImportAborted(signal);
				const cookieOutcome = request.includeCookies
					? await setCookies(this.options.fromPartition(browserProfilePartition(profile.id)), cookieSelection.cookies, signal)
					: { imported: 0, skipped: 0, warnings: [] };
				throwIfImportAborted(signal);
				const limitWarnings: BrowserImportWarning[] = [
					...(cookieSelection.truncated > 0
						? [{ code: "cookie-limit-truncated" as const, count: cookieSelection.truncated }]
						: []),
					...(historyOutcome.truncated > 0
						? [{ code: "history-limit-truncated" as const, count: historyOutcome.truncated }]
						: []),
				];
				results.push({
					sourceProfileNames: group.data.map((data) => data.profile.name),
					destinationProfile: profile,
					importedCookies: cookieOutcome.imported,
					skippedCookies: group.data.reduce((total, data) => total + data.skippedCookies, 0)
						+ cookieSelection.truncated
						+ cookieOutcome.skipped,
					importedHistoryEntries: historyOutcome.imported,
					warnings: mergeWarnings([
						...group.data.flatMap((data) => data.warnings),
						...limitWarnings,
						...cookieOutcome.warnings,
					]),
				});
				onProgress({ requestId: request.requestId, phase: "importing", completed: index + 1, total: groups.length });
			}
			return { sourceName: source.public.name, entries: results };
		} catch (error) {
			const cleanupFailures: string[] = [];
			for (const profile of created.reverse()) {
				let importedSession: ImportSession | undefined;
				try {
					importedSession = this.options.fromPartition(browserProfilePartition(profile.id));
				} catch {
					cleanupFailures.push(`${profile.name}: browser storage cleanup could not start`);
				}
				const cleanup = await Promise.allSettled([
					this.options.historyStore.clear(profile.id),
					...(importedSession ? [importedSession.clearStorageData(), importedSession.clearCache()] : []),
				]);
				if (cleanup.some((outcome) => outcome.status === "rejected")) {
					cleanupFailures.push(`${profile.name}: imported data could not be fully removed`);
					continue;
				}
				if (!importedSession) continue;
				try {
					await this.options.profileStore.deleteProfile(profile.id);
				} catch {
					cleanupFailures.push(`${profile.name}: profile record could not be removed`);
				}
			}
			if (cleanupFailures.length > 0) {
				const original = error instanceof Error ? error.message : "Browser data could not be imported.";
				throw new Error(
					`${original} Cleanup was incomplete (${cleanupFailures.join("; ")}). `
					+ "The affected profiles remain in Browser settings so you can retry deleting them.",
				);
			}
			throw error;
		}
	}
}

export async function readProfileData(
	source: InternalSource,
	profile: InternalSourceProfile,
	request: BrowserImportRequest,
	staging: string,
	budget: SourceBudget,
	decryptor: ChromiumCookieDecryptor | null,
	now: Date,
): Promise<ReadProfileData> {
	const warnings: BrowserImportWarning[] = [];
	let cookies: ImportedCookie[] = [];
	let history: BrowserHistoryEntry[] = [];
	let skippedCookies = 0;
	if (request.includeCookies) {
		const cookieDatabase = await findDatabase(profile.root, profile.cookieRelatives ?? (
			source.descriptor.family === "chromium"
				? [path.join("Network", "Cookies"), "Cookies"]
				: ["cookies.sqlite"]
		), source.descriptor.family === "safari");
		if (!cookieDatabase) {
			warnings.push({ code: "cookie-database-missing" });
		} else {
			const outcome = source.descriptor.family === "safari"
				? readSafariCookies(await readFile(await snapshotFile(cookieDatabase, profile.root, staging, budget)), now)
				: source.descriptor.family === "chromium"
					? readChromiumCookies(await snapshotSQLite(cookieDatabase, profile.root, staging, budget, source.public.name), decryptor, now)
					: readFirefoxCookies(await snapshotSQLite(cookieDatabase, profile.root, staging, budget), now);
			if (!outcome) {
				warnings.push({ code: "cookie-database-missing" });
			} else {
				cookies = outcome.cookies;
				skippedCookies += outcome.skipped;
				warnings.push(...outcome.warnings);
			}
		}
	}
	if (request.includeHistory) {
		const historyDatabase = await findDatabase(profile.root, profile.historyRelatives ?? (
			source.descriptor.family === "chromium" ? ["History"] : ["places.sqlite"]
		), source.descriptor.family === "safari");
		if (!historyDatabase) {
			warnings.push({ code: "history-database-missing" });
		} else {
			const snapshot = await snapshotSQLite(
				historyDatabase,
				profile.root,
				staging,
				budget,
				source.descriptor.family === "chromium" ? source.public.name : undefined,
			);
			const outcome = source.descriptor.family === "chromium"
				? readChromiumHistory(snapshot)
				: source.descriptor.family === "firefox"
					? readFirefoxHistory(snapshot)
					: readSafariHistory(snapshot);
			history = outcome.history;
			warnings.push(...outcome.warnings);
		}
	}
	return { profile, cookies, history, warnings: mergeWarnings(warnings), skippedCookies };
}
