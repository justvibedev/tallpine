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
import { BrowserDescriptor, ImportedCookie, InternalSourceProfile, LOCAL_STATE_MAX_BYTES, contained, existingRealDirectory, hasImportableDatabase, opaqueSourceId, readSmallText } from "./common";
import { booleanValue, normalizeCookieRows, normalizeHistoryRow, numberValue } from "./cookie-rows";
import { cookieOrder, countRows, hasTable, requireTable, tableColumns, withReadOnlyDatabase } from "./sqlite";

export async function discoverFirefoxProfiles(descriptor: BrowserDescriptor, root: string): Promise<InternalSourceProfile[]> {
	const registered = new Map<string, { name: string; default: boolean }>();
	const installDefaults = new Set<string>();
	try {
		const text = await readSmallText(path.join(root, "profiles.ini"), LOCAL_STATE_MAX_BYTES);
		const sections: Array<{ section: string; values: Map<string, string> }> = [];
		let current: typeof sections[number] | undefined;
		for (const raw of text.split(/\r?\n/)) {
			const line = raw.trim();
			if (!line || line.startsWith(";") || line.startsWith("#")) continue;
			if (line.startsWith("[")) {
				current = /^\[[^\[\]]+\]$/.test(line) ? { section: line.slice(1, -1), values: new Map() } : undefined;
				if (current) sections.push(current);
				continue;
			}
			const equals = line.indexOf("=");
			if (current && equals > 0) current.values.set(line.slice(0, equals).trim(), line.slice(equals + 1).trim());
		}
		for (const { section, values } of sections) {
			if (/^Install[0-9a-f]+$/i.test(section)) {
				const directory = values.get("Default");
				if (!directory) continue;
				const candidate = path.resolve(root, directory);
				const profileRoot = await existingRealDirectory(candidate);
				if (profileRoot && contained(root, profileRoot)) installDefaults.add(profileRoot);
				continue;
			}
			if (!/^Profile\d+$/.test(section)) continue;
			const name = values.get("Name");
			const directory = values.get("Path");
			const relative = values.get("IsRelative");
			if (!name || !directory || !["0", "1"].includes(relative ?? "")) continue;
			if (path.isAbsolute(directory) !== (relative === "0")) continue;
			const candidate = path.resolve(root, directory);
			const profileRoot = await existingRealDirectory(candidate);
			if (!profileRoot || !contained(root, profileRoot)) continue;
			registered.set(profileRoot, { name, default: values.get("Default") === "1" });
		}
	} catch {
		registered.clear();
		installDefaults.clear();
	}
	const profileParent = (await existingRealDirectory(path.join(root, "Profiles"))) ?? root;
	const entries = contained(root, profileParent) ? await readdir(profileParent, { withFileTypes: true }).catch(() => []) : [];
	const directories = new Set([
		...registered.keys(),
		...entries.filter((entry) => entry.isDirectory()).map((entry) => path.join(profileParent, entry.name)),
	]);
	const profiles: InternalSourceProfile[] = [];
	for (const directory of directories) {
		const profileRoot = await existingRealDirectory(directory);
		if (!profileRoot || !contained(root, profileRoot) || !(await hasImportableDatabase(profileRoot, "firefox"))) continue;
		const basename = path.basename(profileRoot);
		const dot = basename.indexOf(".");
		const suffix = dot >= 0 ? basename.slice(dot + 1) : basename;
		const metadata = registered.get(profileRoot);
		profiles.push({
			id: opaqueSourceId(`${descriptor.id}:profile`, profileRoot),
			name: metadata?.name ?? (suffix || basename),
			default: installDefaults.size > 0
				? installDefaults.has(profileRoot)
				: metadata?.default ?? /default|release/i.test(suffix),
			root: profileRoot,
		});
	}
	return profiles.sort((a, b) => Number(b.default) - Number(a.default) || a.name.localeCompare(b.name));
}

export function readFirefoxCookies(file: string, now: Date): { cookies: ImportedCookie[]; skipped: number; warnings: BrowserImportWarning[] } | null {
	return withReadOnlyDatabase(file, (database) => {
		if (!hasTable(database, "moz_cookies")) return null;
		const columns = tableColumns(database, "moz_cookies");
		const hasOriginAttributes = columns.has("originAttributes");
		const hasSameSite = columns.has("sameSite");
		const isolationWhere = hasOriginAttributes ? "WHERE COALESCE(originAttributes, '') = ''" : "";
		const total = countRows(database, "moz_cookies");
		const eligible = countRows(database, "moz_cookies", isolationWhere);
		const isolatedSkipped = total - eligible;
		const truncated = Math.max(0, eligible - BROWSER_IMPORT_MAX_COOKIES);
		const orderBy = cookieOrder(columns, ["lastAccessed", "creationTime"]);
		const rows = database.prepare(`
			SELECT host, name, value, path, expiry, isSecure, isHttpOnly, ${hasSameSite ? "sameSite" : "-1 AS sameSite"}
			FROM moz_cookies
			${isolationWhere}
			ORDER BY ${orderBy}
			LIMIT ${BROWSER_IMPORT_MAX_COOKIES}
		`).all() as Record<string, unknown>[];
		const normalized = normalizeCookieRows(rows.map((row) => ({
			domain: row.host,
			name: row.name,
			value: row.value,
			path: row.path,
			expires: numberValue(row.expiry),
			secure: booleanValue(row.isSecure),
			httpOnly: booleanValue(row.isHttpOnly),
			sameSite: firefoxSameSite(numberValue(row.sameSite)),
		})), now);
		if (isolatedSkipped > 0) normalized.warnings.push({ code: "isolated-cookies-skipped", count: isolatedSkipped });
		if (truncated > 0) normalized.warnings.push({ code: "cookie-limit-truncated", count: truncated });
		if (!hasSameSite && rows.length > 0) normalized.warnings.push({ code: "cookie-attributes-defaulted", count: rows.length });
		normalized.skipped += isolatedSkipped + truncated;
		return normalized;
	});
}

export function readFirefoxHistory(file: string): { history: BrowserHistoryEntry[]; warnings: BrowserImportWarning[] } {
	return withReadOnlyDatabase(file, (database) => {
		requireTable(database, "moz_places", "The selected Firefox profile does not contain supported history data.");
		const eligible = countRows(database, "moz_places", "WHERE url LIKE 'http%'");
		const truncated = Math.max(0, eligible - BROWSER_IMPORT_MAX_HISTORY_ENTRIES);
		const history = (database.prepare(`
			SELECT url, title, visit_count, last_visit_date
			FROM moz_places
			WHERE url LIKE 'http%'
			ORDER BY last_visit_date DESC, rowid DESC
			LIMIT ${BROWSER_IMPORT_MAX_HISTORY_ENTRIES}
		`).all() as Record<string, unknown>[]).flatMap((row) => {
			const timestamp = numberValue(row.last_visit_date) / 1_000;
			return normalizeHistoryRow(row.url, row.title, row.visit_count, timestamp);
		});
		return {
			history,
			warnings: truncated > 0 ? [{ code: "history-limit-truncated", count: truncated }] : [],
		};
	});
}

export function firefoxSameSite(value: number): CookiesSetDetails["sameSite"] {
	if (value === 2) return "strict";
	if (value === 1) return "lax";
	if (value === 0) return "no_restriction";
	return "unspecified";
}
