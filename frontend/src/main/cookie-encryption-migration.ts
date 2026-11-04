import { CLI_NAME, LEGACY_CLI_NAME } from "../shared/brand";
import { readdir, stat, writeFile } from "node:fs/promises";
import path from "node:path";

// Chromium encrypts a cookie when it writes it, so rows stored before the
// EnableCookieEncryption fuse shipped keep a plaintext value until they are
// written again. Reading each cookie and setting it back rewrites the row
// encrypted, and keeps every login: nothing is cleared.
const MARKER = "cookie-encryption-rewritten-v1";
const TEMPORARY_PARTITION = new RegExp(`^(ao|${LEGACY_CLI_NAME}|${CLI_NAME})-browser-temporary-`, "i");

export type MigratableCookie = {
	name: string;
	value: string;
	domain?: string;
	/** Read-only: set when the cookie had no Domain attribute. */
	hostOnly?: boolean;
	/** Read-only: true for a session cookie. */
	session?: boolean;
	path?: string;
	secure?: boolean;
	httpOnly?: boolean;
	expirationDate?: number;
	sameSite?: "unspecified" | "no_restriction" | "lax" | "strict";
};

export type CookieSetDetails = Omit<MigratableCookie, "hostOnly" | "session"> & { url: string };

export type CookieStore = {
	get(filter: Record<string, never>): Promise<MigratableCookie[]>;
	set(details: CookieSetDetails): Promise<void>;
	flushStore(): Promise<void>;
};

export type CookieEncryptionMigrationDeps = {
	userDataDir: string;
	defaultCookies: CookieStore;
	partitionCookies: (partitionName: string) => CookieStore;
};

function cookieUrl(cookie: MigratableCookie): string | undefined {
	const host = (cookie.domain ?? "").replace(/^\./, "");
	if (!host) return undefined;
	return `${cookie.secure ? "https" : "http"}://${host}${cookie.path || "/"}`;
}

async function rewrite(store: CookieStore): Promise<boolean> {
	let ok = true;
	for (const cookie of await store.get({})) {
		const url = cookieUrl(cookie);
		if (!url) continue;
		try {
			// A host-only cookie must stay host-only: passing its domain would
			// re-create it as a domain cookie, leave the plaintext row behind, and
			// be refused outright for __Host- cookies.
			const { hostOnly, session: _session, domain, ...rest } = cookie;
			await store.set(hostOnly ? { ...rest, url } : { ...rest, domain, url });
		} catch (error) {
			ok = false;
			console.warn(`cookie rewrite failed for ${cookie.name}:`, error);
		}
	}
	await store.flushStore();
	return ok;
}

async function exists(target: string): Promise<boolean> {
	return stat(target).then(() => true, () => false);
}

/**
 * Rewrites every persistent cookie store once so rows written before cookie
 * encryption was enabled become encrypted. The marker is written only when
 * every cookie was rewritten, so a partial failure is retried on the next
 * start. Returns true when it ran to completion.
 */
export async function rewriteCookiesForEncryption(deps: CookieEncryptionMigrationDeps): Promise<boolean> {
	const marker = path.join(deps.userDataDir, MARKER);
	if (await exists(marker)) return false;
	let ok = await rewrite(deps.defaultCookies);
	const names = await readdir(path.join(deps.userDataDir, "Partitions")).catch(() => [] as string[]);
	for (const name of names) {
		if (TEMPORARY_PARTITION.test(name)) continue;
		ok = (await rewrite(deps.partitionCookies(name)).catch(() => false)) && ok;
	}
	if (ok) await writeFile(marker, `${new Date().toISOString()}\n`, { mode: 0o600 });
	return ok;
}
