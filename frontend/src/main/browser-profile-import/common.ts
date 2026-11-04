/* Split out of browser-profile-import.ts (B297, P53). */

import type { CookiesSetDetails } from "electron";
import { execFile } from "node:child_process";
import { createHash } from "node:crypto";
import { constants } from "node:fs";
import {
lstat,
open,
readdir,
realpath,
rm,
rmdir
} from "node:fs/promises";
import path from "node:path";
import { promisify } from "node:util";
import { PRODUCT_NAME } from "../../shared/brand";
import {
BROWSER_IMPORT_MAX_SOURCE_PROFILES,
type BrowserImportCookieSupportReason,
type BrowserImportRequest,
type BrowserImportSource,
type BrowserImportWarning
} from "../../shared/browser-profile-import";
import {
BROWSER_PROFILE_MAX_COUNT,
isBrowserProfileId,
normalizeBrowserProfileName,
type BrowserProfile
} from "../../shared/browser-profiles";
import { BrowserHistoryStore, type BrowserHistoryEntry } from "../browser-history-store";
import { BrowserProfileStore } from "../browser-profile-store";

export const execFileAsync = promisify(execFile);

export const SOURCE_FILE_MAX_BYTES = 256 * 1024 * 1024;

export const SOURCE_SIDECAR_MAX_BYTES = 64 * 1024 * 1024;

export const IMPORT_TOTAL_MAX_BYTES = 512 * 1024 * 1024;

export const LOCAL_STATE_MAX_BYTES = 4 * 1024 * 1024;

export const STAGING_STALE_AGE_MS = 24 * 60 * 60 * 1_000;

export const SOURCE_ID_PATTERN = /^[0-9a-f]{32}$/;

export type BrowserFamily = "chromium" | "firefox" | "safari";

export type BrowserDescriptor = {
	id: string;
	name: string;
	family: BrowserFamily;
	roots: (context: DiscoveryContext) => string[];
	chromiumKeychainNames?: string[];
};

export type DiscoveryContext = {
	platform: NodeJS.Platform;
	homeDir: string;
	env: NodeJS.ProcessEnv;
};

export type InternalSourceProfile = {
	id: string;
	name: string;
	default: boolean;
	root: string;
	cookieRelatives?: string[];
	historyRelatives?: string[];
};

export type InternalSource = {
	public: BrowserImportSource;
	descriptor: BrowserDescriptor;
	root: string;
	profiles: InternalSourceProfile[];
};

export type InternalDiscoveryOptions = {
	sourceId?: string;
	includeProtectedSource?: boolean;
};

export type ImportedCookie = CookiesSetDetails & {
	dedupeKey: string;
};

export type ReadProfileData = {
	profile: InternalSourceProfile;
	cookies: ImportedCookie[];
	history: BrowserHistoryEntry[];
	warnings: BrowserImportWarning[];
	skippedCookies: number;
};

export type ImportSession = {
	cookies: { set: (details: CookiesSetDetails) => Promise<void> };
	clearStorageData: () => Promise<void>;
	clearCache: () => Promise<void>;
};

export type BrowserProfileImportOptions = {
	stateDir: string;
	profileStore: BrowserProfileStore;
	historyStore: BrowserHistoryStore;
	fromPartition: (partition: string) => ImportSession;
	platform?: NodeJS.Platform;
	homeDir?: string;
	env?: NodeJS.ProcessEnv;
	now?: () => Date;
	sourceLstat?: typeof lstat;
};

export class SourceBudget {
	private used = 0;

	consume(bytes: number): void {
		if (!Number.isSafeInteger(bytes) || bytes < 0 || this.used + bytes > IMPORT_TOTAL_MAX_BYTES) {
			throw new Error("Selected browser data exceeds the import size limit.");
		}
		this.used += bytes;
	}
}

export function throwIfImportAborted(signal: AbortSignal): void {
	if (signal.aborted) throw new Error("Browser import was canceled because the app is closing.");
}

export function configHome(context: DiscoveryContext): string {
	return context.env.XDG_CONFIG_HOME || path.join(context.homeDir, ".config");
}

export function joinEnv(root: string | undefined, ...parts: string[]): string {
	return root ? path.join(root, ...parts) : "";
}

export function opaqueSourceId(kind: string, canonicalPath: string): string {
	return createHash("sha256").update(kind).update("\0").update(canonicalPath).digest("hex").slice(0, 32);
}

export function contained(root: string, candidate: string): boolean {
	const relative = path.relative(root, candidate);
	return relative === "" || (!relative.startsWith("..") && !path.isAbsolute(relative));
}

export async function existingRealDirectory(candidate: string, throwAccessDenied = false, sourceLstat = lstat): Promise<string | null> {
	if (!candidate) return null;
	try {
		const metadata = await sourceLstat(candidate);
		if (!metadata.isDirectory() || metadata.isSymbolicLink()) return null;
		return await realpath(candidate);
	} catch (error) {
		if (throwAccessDenied && isAccessDenied(error)) throw error;
		return null;
	}
}

export async function removeStaleStagingDirectories(root: string): Promise<void> {
	const entries = await readdir(root, { withFileTypes: true }).catch((error) => {
		if ((error as NodeJS.ErrnoException).code === "ENOENT") return [];
		throw error;
	});
	const cutoff = Date.now() - STAGING_STALE_AGE_MS;
	for (const entry of entries) {
		if (!entry.isDirectory() || entry.isSymbolicLink()) continue;
		const directory = path.join(root, entry.name);
		try {
			if ((await lstat(directory)).mtimeMs < cutoff) await rm(directory, { recursive: true, force: true });
		} catch (error) {
			if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
		}
	}
}

export async function removeEmptyDirectory(directory: string): Promise<void> {
	try {
		await rmdir(directory);
	} catch (error) {
		const code = (error as NodeJS.ErrnoException).code;
		if (code !== "ENOENT" && code !== "ENOTEMPTY" && code !== "EEXIST") throw error;
	}
}

export function isAccessDenied(error: unknown): boolean {
	const code = (error as NodeJS.ErrnoException | undefined)?.code;
	return code === "EACCES" || code === "EPERM";
}

export function safariAccessError(): Error {
	return Object.assign(new Error(
		`${PRODUCT_NAME} couldn't access Safari's data. In System Settings, open Privacy & Security > Full Disk Access, `
		+ `allow ${PRODUCT_NAME}, then restart ${PRODUCT_NAME} and try the import again.`,
	), { code: "EACCES" });
}

export async function readSmallJSON(file: string, maxBytes: number): Promise<unknown> {
	return JSON.parse(await readSmallText(file, maxBytes));
}

export async function readSmallText(file: string, maxBytes: number): Promise<string> {
	const metadata = await lstat(file);
	if (!metadata.isFile() || metadata.isSymbolicLink() || metadata.size > maxBytes) {
		throw new Error("Browser metadata exceeds the size limit.");
	}
	const noFollow = "O_NOFOLLOW" in constants ? constants.O_NOFOLLOW : 0;
	const handle = await open(file, constants.O_RDONLY | noFollow);
	try {
		const opened = await handle.stat();
		if (!opened.isFile() || opened.size > maxBytes) throw new Error("Browser metadata exceeds the size limit.");
		const bytes = Buffer.alloc(maxBytes + 1);
		let length = 0;
		while (length < bytes.length) {
			const { bytesRead } = await handle.read(bytes, length, bytes.length - length, null);
			if (bytesRead === 0) break;
			length += bytesRead;
		}
		if (length > maxBytes) throw new Error("Browser metadata exceeds the size limit.");
		return bytes.subarray(0, length).toString("utf8");
	} finally {
		await handle.close().catch(() => undefined);
	}
}

export function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null && !Array.isArray(value);
}

export async function hasImportableDatabase(
	profileRoot: string,
	family: BrowserFamily,
	profile?: InternalSourceProfile,
): Promise<boolean> {
	const candidates =
		family === "chromium"
			? ["History", path.join("Network", "Cookies"), "Cookies"]
			: family === "firefox"
				? ["places.sqlite", "cookies.sqlite"]
				: [...(profile?.historyRelatives ?? []), ...(profile?.cookieRelatives ?? [])];
	for (const relative of candidates) {
		try {
			const metadata = await lstat(path.join(profileRoot, relative));
			if (metadata.isFile() && !metadata.isSymbolicLink()) return true;
		} catch (error) {
			if (family === "safari" && isAccessDenied(error)) throw safariAccessError();
			// Continue looking for another supported database.
		}
	}
	return false;
}

export function cookieCapability(
	family: BrowserFamily,
	platform: NodeJS.Platform,
): { support: BrowserImportSource["cookieSupport"]; reason: BrowserImportCookieSupportReason } {
	if (family === "firefox") return { support: "supported", reason: "firefox-plaintext" };
	if (family === "safari") return { support: "supported", reason: "safari-plaintext" };
	return {
		support: "partial",
		reason: platform === "linux" ? "chromium-encryption-unsupported" : "chromium-encryption-partial",
	};
}

export function redactImportPaths(error: unknown, sourceRoots: string[], stagingRoot: string): Error {
	let message = error instanceof Error ? error.message : "Browser data could not be imported.";
	for (const root of sourceRoots.sort((a, b) => b.length - a.length)) {
		for (const variant of new Set([root, root.replaceAll("\\", "/")])) {
			message = message.replaceAll(variant, "<browser source>");
		}
	}
	for (const variant of new Set([stagingRoot, stagingRoot.replaceAll("\\", "/")])) {
		message = message.replaceAll(variant, "<browser import staging>");
	}
	return new Error(message || "Browser data could not be imported.");
}

export function validateRequest(
	request: BrowserImportRequest,
	sources: InternalSource[],
	existingProfiles: BrowserProfile[],
): BrowserImportRequest {
	if (!isRecord(request) || !isBrowserProfileId(request.requestId)) throw new Error("Browser import request ID is invalid.");
	if (typeof request.sourceId !== "string" || !SOURCE_ID_PATTERN.test(request.sourceId)) throw new Error("Browser import source is invalid.");
	const source = sources.find((candidate) => candidate.public.id === request.sourceId);
	if (!source) throw new Error("The selected browser source is no longer available.");
	if (
		!Array.isArray(request.profileIds) ||
		request.profileIds.length === 0 ||
		request.profileIds.length > BROWSER_IMPORT_MAX_SOURCE_PROFILES ||
		new Set(request.profileIds).size !== request.profileIds.length ||
		request.profileIds.some((id) => typeof id !== "string" || !source.profiles.some((profile) => profile.id === id))
	) {
		throw new Error("Selected browser profiles are invalid.");
	}
	if (request.includeCookies !== true && request.includeHistory !== true) throw new Error("Select cookies, history, or both.");
	const destination = request.destination;
	if (!isRecord(destination) || (destination.mode !== "separate" && destination.mode !== "merge")) {
		throw new Error("Browser import destination is invalid.");
	}
	let names: string[];
	if (destination.mode === "merge") {
		const name = normalizeBrowserProfileName(destination.name);
		if (!name || name !== destination.name) throw new Error("Destination profile name is invalid.");
		names = [name];
	} else {
		if (!isRecord(destination.names)) throw new Error("Destination profile names are invalid.");
		names = request.profileIds.map((id) => {
			if (!Object.hasOwn(destination.names, id)) throw new Error("A destination profile name is missing.");
			const name = normalizeBrowserProfileName(destination.names[id]);
			if (!name || name !== destination.names[id]) throw new Error("Destination profile name is invalid.");
			return name;
		});
	}
	const normalizedNames = names.map((name) => name.toLowerCase());
	if (new Set(normalizedNames).size !== normalizedNames.length) throw new Error("Destination profile names must be unique.");
	if (existingProfiles.length + names.length > BROWSER_PROFILE_MAX_COUNT) {
		throw new Error("Not enough browser profile slots are available for this import.");
	}
	const existing = new Set(existingProfiles.map((profile) => profile.name.toLowerCase()));
	if (normalizedNames.some((name) => existing.has(name))) throw new Error("A destination browser profile already exists.");
	return request;
}


