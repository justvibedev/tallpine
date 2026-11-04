/* Split out of browser-profile-import.ts (B297, P53). */

import type Database from "better-sqlite3";
import { createRequire } from "node:module";
import { numberValue, stringValue } from "./cookie-rows";

// The native addon loads with the first import instead of on every launch:
// only a person-started browser profile import reads SQLite. The main process
// is bundled as CommonJS, where import.meta.url is undefined and createRequire
// would throw while the app starts, so anchor it on __filename (as main.ts
// anchors paths on __dirname).
export const requireNative = createRequire(__filename);

let sqliteConstructor: typeof Database | undefined;
export function openSQLite(file: string, options: ConstructorParameters<typeof Database>[1]): Database {
	sqliteConstructor ??= requireNative("better-sqlite3") as typeof Database;
	return new sqliteConstructor(file, options);
}

export function withReadOnlyDatabase<T>(file: string, read: (database: Database) => T): T {
	const database = openSQLite(file, { readonly: true, fileMustExist: true, timeout: 5_000 });
	try {
		database.pragma("query_only = ON");
		return read(database);
	} finally {
		database.close();
	}
}

export function requireTable(database: Database, table: string, message: string): void {
	if (!hasTable(database, table)) throw new Error(message);
}

export function hasTable(database: Database, table: string): boolean {
	return Boolean(database.prepare("SELECT 1 AS present FROM sqlite_master WHERE type = 'table' AND name = ?").get(table));
}

export function tableColumns(database: Database, table: "cookies" | "moz_cookies"): Set<string> {
	return new Set(
		(database.prepare(`PRAGMA table_info(${table})`).all() as Array<{ name?: unknown }>)
			.map((row) => stringValue(row.name))
			.filter(Boolean),
	);
}

export function countRows(
	database: Database,
	table: "cookies" | "moz_cookies" | "urls" | "moz_places" | "history_items",
	where = "",
): number {
	const row = database.prepare(`SELECT COUNT(*) AS count FROM ${table} ${where}`).get() as { count?: unknown };
	return numberValue(row.count);
}

export function cookieOrder(columns: Set<string>, preferredNewestColumns: string[]): string {
	const newest = preferredNewestColumns.filter((column) => columns.has(column));
	return [...newest.map((column) => `${column} DESC`), "rowid DESC"].join(", ");
}
