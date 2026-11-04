import { execFile } from "node:child_process";
import { access } from "node:fs/promises";
import { promisify } from "node:util";
import { MANAGED_PREFERENCES_PATH } from "./update-policy";

/**
 * True when IT pre-accepted the first-run notice with `FirstRunNoticeAccepted`
 * in the managed preferences domain. Absent, unreadable, or non-boolean means
 * not accepted: the notice shows. See docs/agent-policy.md.
 */
export async function readManagedFirstRunAccepted(
	platform: NodeJS.Platform = process.platform,
	file = MANAGED_PREFERENCES_PATH,
	readPlist: (file: string) => Promise<string> = plutilToJson,
): Promise<boolean> {
	if (platform !== "darwin") return false;
	try {
		await access(file);
		const doc: unknown = JSON.parse(await readPlist(file));
		return typeof doc === "object" && doc !== null && (doc as Record<string, unknown>).FirstRunNoticeAccepted === true;
	} catch {
		return false;
	}
}

async function plutilToJson(file: string): Promise<string> {
	return (await promisify(execFile)("/usr/bin/plutil", ["-convert", "json", "-o", "-", file], { timeout: 5_000 })).stdout;
}
