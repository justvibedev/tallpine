import { contained, isAccessDenied, SOURCE_FILE_MAX_BYTES, SOURCE_SIDECAR_MAX_BYTES, SourceBudget } from "./common";
import { openSQLite } from "./sqlite";
/* Split out of browser-profile-import.ts (B297, P53). */
import { randomUUID } from "node:crypto";
import { constants } from "node:fs";
import {
chmod,
lstat,
mkdir,
open,
realpath,
rename,
rm,
stat
} from "node:fs/promises";
import path from "node:path";
import { PRODUCT_NAME } from "../../shared/brand";

export async function snapshotFile(
	file: string,
	profileRoot: string,
	staging: string,
	budget: SourceBudget,
): Promise<string> {
	const destination = path.join(staging, `${randomUUID()}-${path.basename(file)}`);
	const canonical = await preflightContainedFile(file, profileRoot, SOURCE_FILE_MAX_BYTES, budget);
	const noFollow = "O_NOFOLLOW" in constants ? constants.O_NOFOLLOW : 0;
	const source = await open(canonical, constants.O_RDONLY | noFollow);
	try {
		const opened = await source.stat();
		if (!opened.isFile() || opened.size > SOURCE_FILE_MAX_BYTES) {
			throw new Error("Browser source file exceeds the snapshot size limit.");
		}
		const bytes = await source.readFile();
		if (bytes.length !== opened.size) throw new Error("Browser source file changed during import.");
		const output = await open(destination, constants.O_WRONLY | constants.O_CREAT | constants.O_EXCL, 0o600);
		try {
			await output.writeFile(bytes);
		} finally {
			await output.close();
		}
		return destination;
	} catch (error) {
		await rm(destination, { force: true }).catch(() => undefined);
		throw error;
	} finally {
		await source.close();
	}
}

export async function findDatabase(profileRoot: string, relatives: string[], throwAccessDenied = false): Promise<string | null> {
	for (const relative of relatives) {
		const candidate = path.join(profileRoot, relative);
		try {
			const metadata = await lstat(candidate);
			if (!metadata.isFile() || metadata.isSymbolicLink()) continue;
			const canonical = await realpath(candidate);
			if (contained(profileRoot, canonical)) return canonical;
		} catch (error) {
			if (throwAccessDenied && isAccessDenied(error)) throw error;
			// Try the next known location.
		}
	}
	return null;
}

export async function snapshotSQLite(
	database: string,
	profileRoot: string,
	staging: string,
	budget: SourceBudget,
	browserName?: string,
): Promise<string> {
	const destination = path.join(staging, `${randomUUID()}-${path.basename(database)}`);
	const canonical = await preflightContainedFile(database, profileRoot, SOURCE_FILE_MAX_BYTES, budget);
	for (const suffix of ["-wal", "-shm"]) {
		try {
			await preflightContainedFile(`${database}${suffix}`, profileRoot, SOURCE_SIDECAR_MAX_BYTES, budget);
		} catch (error) {
			if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
		}
	}
	const source = openSQLite(canonical, { readonly: true, fileMustExist: true, timeout: 5_000 });
	const temporarySnapshots: string[] = [];
	try {
		source.pragma("query_only = ON");
		for (let attempt = 0; attempt < 2; attempt += 1) {
			await mkdir(staging, { recursive: true, mode: 0o700 });
			const temporary = path.join(staging, `${randomUUID()}-${path.basename(database)}.tmp`);
			temporarySnapshots.push(temporary);
			try {
				await source.backup(temporary);
			} catch (error) {
				await rm(temporary, { force: true }).catch(() => undefined);
				if (attempt === 0) continue;
				if (browserName) throw temporarySnapshotError(browserName);
				throw error;
			}
			const output = await stat(temporary).catch((error) => {
				if ((error as NodeJS.ErrnoException).code === "ENOENT") return null;
				throw error;
			});
			if (!output) {
				await rm(temporary, { force: true }).catch(() => undefined);
				continue;
			}
			if (!output.isFile() || output.size > SOURCE_FILE_MAX_BYTES + SOURCE_SIDECAR_MAX_BYTES) {
				throw new Error("Browser source database exceeds the snapshot size limit.");
			}
			await chmod(temporary, 0o600);
			await rename(temporary, destination);
			return destination;
		}
		if (browserName) throw temporarySnapshotError(browserName);
		throw new Error(`${PRODUCT_NAME}'s temporary browser data snapshot disappeared before it could be read. Restart ${PRODUCT_NAME} and retry the import.`);
	} catch (error) {
		await rm(destination, { force: true }).catch(() => undefined);
		throw error;
	} finally {
		for (const temporary of temporarySnapshots) {
			await rm(temporary, { force: true }).catch(() => undefined);
		}
		source.close();
	}
}

export function temporarySnapshotError(browserName: string): Error {
	return new Error(
		`${PRODUCT_NAME} couldn't create a temporary copy of ${browserName}'s profile database. `
		+ `Fully close ${browserName}, including background processes, then retry the import. `
		+ `If this keeps happening, restart ${PRODUCT_NAME} and try again.`,
	);
}

export async function preflightContainedFile(
	source: string,
	root: string,
	maxBytes: number,
	budget: SourceBudget,
): Promise<string> {
	const metadata = await lstat(source);
	if (!metadata.isFile() || metadata.isSymbolicLink() || metadata.size > maxBytes) {
		throw new Error("Browser source database is invalid or exceeds the size limit.");
	}
	const canonical = await realpath(source);
	if (!contained(root, canonical)) throw new Error("Browser source database is outside its profile directory.");
	const canonicalMetadata = await lstat(canonical);
	if (!canonicalMetadata.isFile() || canonicalMetadata.isSymbolicLink() || canonicalMetadata.size > maxBytes) {
		throw new Error("Browser source database exceeds the size limit.");
	}
	budget.consume(canonicalMetadata.size);
	return canonical;
}
