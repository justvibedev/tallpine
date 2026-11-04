/* Split out of browser-profile-import.ts (B297, P53). */

import type { CookiesSetDetails } from "electron";
import {
BROWSER_IMPORT_MAX_COOKIES,
type BrowserImportWarning
} from "../../shared/browser-profile-import";
import { type BrowserHistoryEntry } from "../browser-history-store";
import { ImportSession, ImportedCookie, throwIfImportAborted } from "./common";

export function normalizeCookieRows(
	rows: Array<Record<string, unknown>>,
	now: Date,
): { cookies: ImportedCookie[]; skipped: number; warnings: BrowserImportWarning[] } {
	const cookies: ImportedCookie[] = [];
	let expired = 0;
	let invalid = 0;
	for (const row of rows) {
		const domain = stringValue(row.domain).trim().toLowerCase();
		const hostname = domain.replace(/^\.+/, "");
		if (!hostname) {
			invalid += 1;
			continue;
		}
		const name = stringValue(row.name);
		const value = stringValue(row.value);
		if (!name) {
			invalid += 1;
			continue;
		}
		const expirationDate = typeof row.expires === "number" && Number.isFinite(row.expires) && row.expires > 0 ? row.expires : undefined;
		if (expirationDate !== undefined && expirationDate <= now.getTime() / 1_000) {
			expired += 1;
			continue;
		}
		const cookiePath = stringValue(row.path) || "/";
		const secure = row.secure === true;
		const sameSite = row.sameSite as CookiesSetDetails["sameSite"];
		try {
			const url = new URL(`${secure ? "https" : "http"}://${hostname}${cookiePath.startsWith("/") ? cookiePath : `/${cookiePath}`}`).href;
			cookies.push({
				url,
				name,
				value,
				// Passing domain turns a host-only cookie into a domain cookie.
				// Electron also rejects __Host- cookies when domain is set.
				...(domain.startsWith(".") ? { domain } : {}),
				path: cookiePath,
				secure,
				httpOnly: row.httpOnly === true,
				...(sameSite ? { sameSite } : {}),
				...(expirationDate !== undefined ? { expirationDate } : {}),
				dedupeKey: `${name}\0${domain}\0${cookiePath}`,
			});
		} catch {
			invalid += 1;
		}
	}
	const warnings: BrowserImportWarning[] = [];
	if (expired > 0) warnings.push({ code: "expired-cookies-skipped", count: expired });
	if (invalid > 0) warnings.push({ code: "invalid-cookies-skipped", count: invalid });
	return { cookies, skipped: expired + invalid, warnings };
}

export function normalizeHistoryRow(
	rawURL: unknown,
	rawTitle: unknown,
	rawVisitCount: unknown,
	timestampMs: number,
): BrowserHistoryEntry[] {
	try {
		const url = new URL(stringValue(rawURL));
		if (url.protocol !== "http:" && url.protocol !== "https:") return [];
		const title = stringValue(rawTitle).trim().slice(0, 512);
		const lastVisited = Number.isFinite(timestampMs) && timestampMs > 0 ? new Date(timestampMs).toISOString() : new Date(0).toISOString();
		return [{
			url: url.href,
			...(title ? { title } : {}),
			lastVisited,
			visitCount: Math.max(1, Math.trunc(numberValue(rawVisitCount)) || 1),
		}];
	} catch {
		return [];
	}
}

export function stringValue(value: unknown): string {
	return typeof value === "string" ? value : "";
}

export function numberValue(value: unknown): number {
	return typeof value === "number" ? value : typeof value === "bigint" ? Number(value) : 0;
}

export function booleanValue(value: unknown): boolean {
	return value === true || value === 1 || value === 1n;
}

export function dedupeCookies(cookies: ImportedCookie[]): { cookies: ImportedCookie[]; truncated: number } {
	const byKey = new Map<string, ImportedCookie>();
	for (const cookie of cookies) {
		// profileIds order is user-selected order. For an identity collision,
		// retain the first selected profile's cookie rather than silently mixing
		// account state based on expiration timestamps.
		if (!byKey.has(cookie.dedupeKey)) byKey.set(cookie.dedupeKey, cookie);
	}
	const unique = [...byKey.values()].sort(
		(a, b) => (b.expirationDate ?? 0) - (a.expirationDate ?? 0) || a.dedupeKey.localeCompare(b.dedupeKey),
	);
	return {
		cookies: unique.slice(0, BROWSER_IMPORT_MAX_COOKIES),
		truncated: Math.max(0, unique.length - BROWSER_IMPORT_MAX_COOKIES),
	};
}

export async function setCookies(
	session: ImportSession,
	cookies: ImportedCookie[],
	signal: AbortSignal,
): Promise<{ imported: number; skipped: number; warnings: BrowserImportWarning[] }> {
	let imported = 0;
	let skipped = 0;
	for (const cookie of cookies) {
		throwIfImportAborted(signal);
		const { dedupeKey: _dedupeKey, ...details } = cookie;
		try {
			await session.cookies.set(details);
			throwIfImportAborted(signal);
			imported += 1;
		} catch {
			skipped += 1;
		}
	}
	return {
		imported,
		skipped,
		warnings: skipped > 0 ? [{ code: "cookie-write-failed", count: skipped }] : [],
	};
}

export function mergeWarnings(warnings: BrowserImportWarning[]): BrowserImportWarning[] {
	const counts = new Map<BrowserImportWarning["code"], number | undefined>();
	for (const warning of warnings) {
		const current = counts.get(warning.code);
		counts.set(warning.code, warning.count === undefined ? current : (current ?? 0) + warning.count);
	}
	return [...counts].map(([code, count]) => ({ code, ...(count === undefined ? {} : { count }) }));
}
