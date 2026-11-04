/* Split out of browser-profile-import.ts (B297, P53). */

import type { CookiesSetDetails } from "electron";
import {
readdir
} from "node:fs/promises";
import path from "node:path";
import {
BROWSER_IMPORT_MAX_COOKIES,
BROWSER_IMPORT_MAX_HISTORY_ENTRIES,
type BrowserImportWarning
} from "../../shared/browser-profile-import";
import { type BrowserHistoryEntry } from "../browser-history-store";
import { ChromiumCookieDecryptor } from "./chromium-decrypt";
import { BrowserDescriptor, ImportedCookie, InternalSourceProfile, LOCAL_STATE_MAX_BYTES, contained, existingRealDirectory, hasImportableDatabase, isRecord, opaqueSourceId, readSmallJSON } from "./common";
import { booleanValue, normalizeCookieRows, normalizeHistoryRow, numberValue, stringValue } from "./cookie-rows";
import { cookieOrder, countRows, requireTable, tableColumns, withReadOnlyDatabase } from "./sqlite";

export async function discoverChromiumProfiles(descriptor: BrowserDescriptor, root: string): Promise<InternalSourceProfile[]> {
	const names = new Map<string, string>();
	try {
		const localState = await readSmallJSON(path.join(root, "Local State"), LOCAL_STATE_MAX_BYTES);
		if (isRecord(localState) && isRecord(localState.profile) && isRecord(localState.profile.info_cache)) {
			for (const [directory, raw] of Object.entries(localState.profile.info_cache)) {
				if (!isRecord(raw)) continue;
				const name = typeof raw.name === "string" ? raw.name.trim() : "";
				if (name) names.set(directory, name);
			}
		}
	} catch {
		// A missing or malformed Local State does not hide otherwise valid profiles.
	}
	const entries = await readdir(root, { withFileTypes: true }).catch(() => []);
	const directories = new Set<string>([
		...names.keys(),
		...entries
			.filter((entry) => entry.isDirectory() && (entry.name === "Default" || /^Profile \d+$/.test(entry.name)))
			.map((entry) => entry.name),
	]);
	const profiles: InternalSourceProfile[] = [];
	for (const directory of directories) {
		const profileRoot = await existingRealDirectory(path.join(root, directory));
		if (!profileRoot || !contained(root, profileRoot) || !(await hasImportableDatabase(profileRoot, "chromium"))) continue;
		profiles.push({
			id: opaqueSourceId(`${descriptor.id}:profile`, profileRoot),
			name: names.get(directory) || (directory === "Default" ? "Default" : directory),
			default: directory === "Default",
			root: profileRoot,
		});
	}
	return profiles.sort((a, b) => Number(b.default) - Number(a.default) || a.name.localeCompare(b.name));
}

export function readChromiumCookies(
	file: string,
	decryptor: ChromiumCookieDecryptor | null,
	now: Date,
): { cookies: ImportedCookie[]; skipped: number; warnings: BrowserImportWarning[] } {
	return withReadOnlyDatabase(file, (database) => {
		requireTable(database, "cookies", "The selected Chromium profile does not contain supported cookie data.");
		const columns = tableColumns(database, "cookies");
		const hasPartitionKey = columns.has("top_frame_site_key");
		const isolationWhere = hasPartitionKey ? "WHERE COALESCE(top_frame_site_key, '') = ''" : "";
		const total = countRows(database, "cookies");
		const eligible = countRows(database, "cookies", isolationWhere);
		const isolatedSkipped = total - eligible;
		const truncated = Math.max(0, eligible - BROWSER_IMPORT_MAX_COOKIES);
		const orderBy = cookieOrder(columns, ["last_access_utc", "creation_utc"]);
		const rows = database.prepare(`
			SELECT host_key, name, value, encrypted_value, path, expires_utc, is_secure, is_httponly, samesite
			FROM cookies
			${isolationWhere}
			ORDER BY ${orderBy}
			LIMIT ${BROWSER_IMPORT_MAX_COOKIES}
		`).all() as Record<string, unknown>[];
		let encryptedSkipped = 0;
		const raw: Array<Record<string, unknown>> = [];
		for (const row of rows) {
			const domain = stringValue(row.host_key);
			let value = stringValue(row.value);
			if (!value) {
				const encrypted = Buffer.isBuffer(row.encrypted_value) ? row.encrypted_value : Buffer.alloc(0);
				value = decryptor?.decrypt(encrypted, domain) ?? "";
				if (!value && encrypted.length > 0) {
					encryptedSkipped += 1;
					continue;
				}
			}
			raw.push({
				domain,
				name: row.name,
				value,
				path: row.path,
				expires: chromiumTimestamp(numberValue(row.expires_utc)),
				secure: booleanValue(row.is_secure),
				httpOnly: booleanValue(row.is_httponly),
				sameSite: chromiumSameSite(numberValue(row.samesite)),
			});
		}
		const normalized = normalizeCookieRows(raw, now);
		if (encryptedSkipped > 0) normalized.warnings.push({ code: "encrypted-cookies-skipped", count: encryptedSkipped });
		if (isolatedSkipped > 0) normalized.warnings.push({ code: "isolated-cookies-skipped", count: isolatedSkipped });
		if (truncated > 0) normalized.warnings.push({ code: "cookie-limit-truncated", count: truncated });
		normalized.skipped += encryptedSkipped + isolatedSkipped + truncated;
		return normalized;
	});
}

export function readChromiumHistory(file: string): { history: BrowserHistoryEntry[]; warnings: BrowserImportWarning[] } {
	return withReadOnlyDatabase(file, (database) => {
		requireTable(database, "urls", "The selected Chromium profile does not contain supported history data.");
		const eligible = countRows(database, "urls", "WHERE url LIKE 'http%'");
		const truncated = Math.max(0, eligible - BROWSER_IMPORT_MAX_HISTORY_ENTRIES);
		const history = (database.prepare(`
			SELECT url, title, visit_count, last_visit_time
			FROM urls
			WHERE url LIKE 'http%'
			ORDER BY last_visit_time DESC, rowid DESC
			LIMIT ${BROWSER_IMPORT_MAX_HISTORY_ENTRIES}
		`).all() as Record<string, unknown>[]).flatMap((row) =>
			normalizeHistoryRow(row.url, row.title, row.visit_count, chromiumTimestamp(numberValue(row.last_visit_time)) * 1_000),
		);
		return {
			history,
			warnings: truncated > 0 ? [{ code: "history-limit-truncated", count: truncated }] : [],
		};
	});
}

export function chromiumTimestamp(rawMicroseconds: number): number {
	if (!Number.isFinite(rawMicroseconds) || rawMicroseconds <= 0) return 0;
	return rawMicroseconds / 1_000_000 - 11_644_473_600;
}

export function chromiumSameSite(value: number): CookiesSetDetails["sameSite"] {
	if (value === 2) return "strict";
	if (value === 1) return "lax";
	if (value === 0) return "no_restriction";
	return "unspecified";
}
