import type { BrowserImportWarning } from "../../shared/browser-profile-import";
import type { ImportedCookie, SourceBudget } from "./common";
import { findDatabase, snapshotSQLite } from "./snapshot";
import { hasTable, withReadOnlyDatabase } from "./sqlite";
import { stringValue } from "./cookie-rows";

// Chrome registers Device Bound Session Credentials (DBSC) per site in this
// profile database. A bound site (Google today) keeps short-lived cookies such
// as __Secure-1PSIDTS fresh only by signing a challenge with a key that never
// leaves this device's Chrome, so a copied sign-in stops refreshing within
// minutes and the site asks the person to verify again.
const DEVICE_BOUND_SESSIONS_DATABASE = "Device Bound Sessions";
const DEVICE_BOUND_SESSIONS_TABLE = "dbsc_session_tbl";

export function readChromiumBoundSessionSites(file: string): string[] {
	return withReadOnlyDatabase(file, (database) => {
		if (!hasTable(database, DEVICE_BOUND_SESSIONS_TABLE)) return [];
		const sites = new Set<string>();
		// Only the site key is read; the session protos hold key material.
		for (const row of database.prepare(`SELECT key FROM ${DEVICE_BOUND_SESSIONS_TABLE}`).all() as Record<string, unknown>[]) {
			try {
				const hostname = new URL(stringValue(row.key)).hostname.toLowerCase();
				if (hostname) sites.add(hostname);
			} catch {
				// An unrecognized key format is not a bound site we can name.
			}
		}
		return [...sites].sort();
	});
}

export function boundSitesWithImportedCookies(sites: string[], cookies: ImportedCookie[]): string[] {
	const hosts = new Set(cookies.map((cookie) => new URL(cookie.url).hostname));
	return sites.filter((site) => [...hosts].some((host) => host === site || host.endsWith(`.${site}`)));
}

// Best effort: a missing, locked or unreadable session store never fails the
// cookie import; the person just does not get the warning.
export async function chromiumDeviceBoundSessionWarnings(
	profileRoot: string,
	staging: string,
	budget: SourceBudget,
	browserName: string,
	cookies: ImportedCookie[],
): Promise<BrowserImportWarning[]> {
	if (cookies.length === 0) return [];
	try {
		const database = await findDatabase(profileRoot, [DEVICE_BOUND_SESSIONS_DATABASE]);
		if (!database) return [];
		const sites = boundSitesWithImportedCookies(
			readChromiumBoundSessionSites(await snapshotSQLite(database, profileRoot, staging, budget, browserName)),
			cookies,
		);
		return sites.length > 0 ? [{ code: "device-bound-sessions", count: sites.length, sites }] : [];
	} catch {
		return [];
	}
}
