/* Split out of browser-profile-import.ts (B297, P53). */

import type { CookiesSetDetails } from "electron";
import { randomUUID } from "node:crypto";
import {
lstat,
mkdir,
readdir,
rm
} from "node:fs/promises";
import path from "node:path";
import {
BROWSER_IMPORT_MAX_COOKIES,
BROWSER_IMPORT_MAX_HISTORY_ENTRIES,
BROWSER_IMPORT_MAX_SOURCE_PROFILES,
type BrowserImportWarning
} from "../../shared/browser-profile-import";
import { type BrowserHistoryEntry } from "../browser-history-store";
import { BrowserDescriptor, ImportedCookie, InternalSourceProfile, SourceBudget, contained, existingRealDirectory, hasImportableDatabase, opaqueSourceId, removeEmptyDirectory, safariAccessError } from "./common";
import { findDatabase, snapshotSQLite } from "./snapshot";
import { mergeWarnings, normalizeCookieRows, normalizeHistoryRow, numberValue, stringValue } from "./cookie-rows";
import { countRows, hasTable, requireTable, withReadOnlyDatabase } from "./sqlite";

export const SAFARI_DEFAULT_HISTORY = path.join("Safari", "History.db");

export const SAFARI_DEFAULT_COOKIES = [
	path.join("Containers", "com.apple.Safari", "Data", "Library", "Cookies", "Cookies.binarycookies"),
	path.join("Cookies", "Cookies.binarycookies"),
];

export const SAFARI_PROFILE_ID = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

export async function discoverSafariProfiles(descriptor: BrowserDescriptor, root: string, stagingRoot: string, sourceLstat = lstat): Promise<InternalSourceProfile[]> {
	const containerSafari = path.join("Containers", "com.apple.Safari", "Data", "Library", "Safari");
	const profileParentRelative = path.join(containerSafari, "Profiles");
	let profileParent: string | null;
	try {
		profileParent = await existingRealDirectory(path.join(root, profileParentRelative), true, sourceLstat);
	} catch {
		throw safariAccessError();
	}
	const names = await readSafariProfileNames(root, path.join(containerSafari, "SafariTabs.db"), stagingRoot);
	const profiles: InternalSourceProfile[] = [];
	const defaultProfile: InternalSourceProfile = {
		id: opaqueSourceId(`${descriptor.id}:profile`, "DefaultProfile"),
		name: names.get("DefaultProfile") || "Personal",
		default: true,
		root,
		cookieRelatives: SAFARI_DEFAULT_COOKIES,
		historyRelatives: [SAFARI_DEFAULT_HISTORY],
	};
	if (await hasImportableDatabase(root, "safari", defaultProfile)) profiles.push(defaultProfile);

	if (profileParent && contained(root, profileParent)) {
		const entries = await readdir(profileParent, { withFileTypes: true });
		for (const entry of entries.sort((a, b) => a.name.localeCompare(b.name))) {
			if (!entry.isDirectory() || !SAFARI_PROFILE_ID.test(entry.name)) continue;
			const uuidUpper = entry.name.toUpperCase();
			const uuidLower = entry.name.toLowerCase();
			const profile: InternalSourceProfile = {
				id: opaqueSourceId(`${descriptor.id}:profile`, uuidUpper),
				name: names.get(uuidUpper) || `Profile ${uuidUpper.slice(0, 8)}`,
				default: false,
				root,
				cookieRelatives: [path.join(
					"Containers", "com.apple.Safari", "Data", "Library", "WebKit", "WebsiteDataStore",
					uuidLower, "Cookies", "Cookies.binarycookies",
				)],
				historyRelatives: [path.join(profileParentRelative, entry.name, "History.db")],
			};
			if (await hasImportableDatabase(root, "safari", profile)) profiles.push(profile);
		}
	}
	return profiles.slice(0, BROWSER_IMPORT_MAX_SOURCE_PROFILES);
}

export async function readSafariProfileNames(root: string, relativeDatabase: string, stagingRoot: string): Promise<Map<string, string>> {
	const names = new Map<string, string>();
	const staging = path.join(stagingRoot, randomUUID());
	try {
		const databaseFile = await findDatabase(root, [relativeDatabase]);
		if (!databaseFile) return names;
		await mkdir(staging, { recursive: true, mode: 0o700 });
		const snapshot = await snapshotSQLite(databaseFile, root, staging, new SourceBudget());
		withReadOnlyDatabase(snapshot, (database) => {
			if (!hasTable(database, "bookmarks")) return;
			const rows = database.prepare(`
				SELECT external_uuid, title
				FROM bookmarks
				WHERE subtype = 2 AND external_uuid IS NOT NULL
				ORDER BY rowid
				LIMIT 1024
			`).all() as Record<string, unknown>[];
			for (const row of rows) {
				const rawId = stringValue(row.external_uuid).trim();
				const id = rawId === "DefaultProfile" ? rawId : rawId.toUpperCase();
				const name = stringValue(row.title).trim().slice(0, 64);
				if ((id === "DefaultProfile" || SAFARI_PROFILE_ID.test(id)) && name && !names.has(id)) names.set(id, name);
			}
		});
	} catch {
		// TCC may block SafariTabs.db. UUID directory discovery still works.
	} finally {
		await rm(staging, { recursive: true, force: true }).catch(() => undefined);
		await removeEmptyDirectory(stagingRoot);
	}
	return names;
}

export function readSafariCookies(
	bytes: Buffer,
	now: Date,
): { cookies: ImportedCookie[]; skipped: number; warnings: BrowserImportWarning[] } {
	const parsed = parseSafariBinaryCookies(bytes);
	const normalized = normalizeCookieRows(parsed.rows, now);
	if (parsed.invalid > 0) normalized.warnings.push({ code: "invalid-cookies-skipped", count: parsed.invalid });
	if (parsed.truncated > 0) normalized.warnings.push({ code: "cookie-limit-truncated", count: parsed.truncated });
	normalized.skipped += parsed.invalid + parsed.truncated;
	normalized.warnings = mergeWarnings(normalized.warnings);
	return normalized;
}

export function parseSafariBinaryCookies(bytes: Buffer): {
	rows: Array<Record<string, unknown>>;
	invalid: number;
	truncated: number;
} {
	const unsupported = () => new Error("The selected Safari profile does not contain supported cookie data.");
	if (bytes.length < 16 || bytes.subarray(0, 4).toString("ascii") !== "cook") throw unsupported();
	const pageCount = bytes.readUInt32BE(4);
	if (pageCount > Math.floor((bytes.length - 8) / 4)) throw unsupported();
	const pageSizes: number[] = [];
	let cursor = 8;
	let totalPageBytes = 0;
	for (let index = 0; index < pageCount; index += 1) {
		const pageSize = bytes.readUInt32BE(cursor);
		pageSizes.push(pageSize);
		totalPageBytes += pageSize;
		cursor += 4;
	}
	if (!Number.isSafeInteger(totalPageBytes) || cursor + totalPageBytes + 8 > bytes.length) throw unsupported();
	const rows: Array<Record<string, unknown>> = [];
	let invalid = 0;
	let truncated = 0;
	for (const pageSize of pageSizes) {
		if (pageSize < 12 || cursor + pageSize > bytes.length) throw unsupported();
		const page = bytes.subarray(cursor, cursor + pageSize);
		cursor += pageSize;
		if (page.readUInt32BE(0) !== 0x100) throw unsupported();
		const cookieCount = page.readUInt32LE(4);
		if (cookieCount > Math.floor((page.length - 12) / 4)) throw unsupported();
		if (page.readUInt32LE(8 + cookieCount * 4) !== 0) throw unsupported();
		const parseCount = Math.min(cookieCount, Math.max(0, BROWSER_IMPORT_MAX_COOKIES - rows.length));
		for (let index = 0; index < parseCount; index += 1) {
			const recordOffset = page.readUInt32LE(8 + index * 4);
			if (recordOffset + 56 > page.length) {
				invalid += 1;
				continue;
			}
			const recordSize = page.readUInt32LE(recordOffset);
			if (recordSize < 56 || recordOffset + recordSize > page.length) {
				invalid += 1;
				continue;
			}
			const record = page.subarray(recordOffset, recordOffset + recordSize);
			const offsets = [16, 20, 24, 28].map((offset) => record.readUInt32LE(offset));
			if (offsets.some((offset) => offset < 56 || offset >= record.length)) {
				invalid += 1;
				continue;
			}
			try {
				const flags = record.readUInt32LE(8);
				rows.push({
					domain: readNullTerminatedUTF8(record, offsets[0]!),
					name: readNullTerminatedUTF8(record, offsets[1]!),
					path: readNullTerminatedUTF8(record, offsets[2]!),
					value: readNullTerminatedUTF8(record, offsets[3]!),
					expires: safariTimestamp(record.readDoubleLE(40)),
					secure: (flags & 0x1) !== 0,
					httpOnly: (flags & 0x4) !== 0,
					sameSite: safariSameSite((flags >> 3) & 0x7),
				});
			} catch {
				invalid += 1;
			}
		}
		truncated += cookieCount - parseCount;
	}
	return { rows, invalid, truncated };
}

export function readNullTerminatedUTF8(record: Buffer, offset: number): string {
	const end = record.indexOf(0, offset);
	if (end < 0) throw new Error("Safari cookie string is not terminated.");
	return new TextDecoder("utf-8", { fatal: true }).decode(record.subarray(offset, end));
}

export function readSafariHistory(file: string): { history: BrowserHistoryEntry[]; warnings: BrowserImportWarning[] } {
	return withReadOnlyDatabase(file, (database) => {
		requireTable(database, "history_items", "The selected Safari profile does not contain supported history data.");
		requireTable(database, "history_visits", "The selected Safari profile does not contain supported history data.");
		const eligible = countRows(database, "history_items", "WHERE url LIKE 'http%'");
		const truncated = Math.max(0, eligible - BROWSER_IMPORT_MAX_HISTORY_ENTRIES);
		const history = (database.prepare(`
			SELECT item.url,
				(SELECT recent.title FROM history_visits recent
				 WHERE recent.history_item = item.id ORDER BY recent.visit_time DESC, recent.id DESC LIMIT 1) AS title,
				item.visit_count,
				MAX(visit.visit_time) AS last_visit_time
			FROM history_items item
			LEFT JOIN history_visits visit ON visit.history_item = item.id
			WHERE item.url LIKE 'http%'
			GROUP BY item.id
			ORDER BY last_visit_time DESC, item.id DESC
			LIMIT ${BROWSER_IMPORT_MAX_HISTORY_ENTRIES}
		`).all() as Record<string, unknown>[]).flatMap((row) =>
			normalizeHistoryRow(
				row.url,
				row.title,
				row.visit_count,
				safariTimestamp(numberValue(row.last_visit_time)) * 1_000,
			),
		);
		return {
			history,
			warnings: truncated > 0 ? [{ code: "history-limit-truncated", count: truncated }] : [],
		};
	});
}

export function safariTimestamp(rawSeconds: number): number {
	if (!Number.isFinite(rawSeconds) || rawSeconds <= 0) return 0;
	return rawSeconds + 978_307_200;
}

export function safariSameSite(value: number): CookiesSetDetails["sameSite"] {
	if (value === 7) return "strict";
	if (value === 5) return "lax";
	if (value === 4) return "no_restriction";
	return "unspecified";
}
