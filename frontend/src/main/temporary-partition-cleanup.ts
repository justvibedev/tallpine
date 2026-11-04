import { CLI_NAME, LEGACY_CLI_NAME } from "../shared/brand";
import { readdir, rm } from "node:fs/promises";
import path from "node:path";

// Temporary browser views use a throwaway persist: partition that is cleared
// when the view closes. A crash or force-quit skips that, and the leftovers
// accumulate under <userData>/Partitions. The prefix has shipped as both "ao-"
// and "vo-", so match either.
const TEMPORARY_PARTITION_DIR = new RegExp(`^(ao|${LEGACY_CLI_NAME}|${CLI_NAME})-browser-temporary-`, "i");

/**
 * Removes leftover temporary browser partitions from userData. Call it before
 * any browser view exists, so none of them can still be in use. Failures are
 * logged and skipped. Returns how many directories were removed.
 */
export async function cleanupTemporaryBrowserPartitions(userDataDir: string): Promise<number> {
	const partitionsDir = path.join(userDataDir, "Partitions");
	let names: string[];
	try {
		names = await readdir(partitionsDir);
	} catch {
		return 0;
	}
	let removed = 0;
	for (const name of names) {
		if (!TEMPORARY_PARTITION_DIR.test(name)) continue;
		try {
			await rm(path.join(partitionsDir, name), { recursive: true, force: true });
			removed += 1;
		} catch (error) {
			console.warn(`failed to remove temporary browser partition ${name}:`, error);
		}
	}
	return removed;
}
