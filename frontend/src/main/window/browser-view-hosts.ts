/* Split out of main-window.ts (B297, P53). */
declare const MAIN_WINDOW_VITE_DEV_SERVER_URL: string | undefined;
declare const MAIN_WINDOW_VITE_NAME: string;
declare const __TALLPINE_TEAM_ID__: string | undefined;
declare const __TALLPINE_BUILD_INFO__: { version: string; commit: string; date: string; dirty: string } | undefined;
import {
session
} from "electron";
import os from "node:os";
import path from "node:path";
import { HOME_DIR_NAME } from "../../shared/brand";
import { ctx } from "../app-context";
import { browserCleanupPromises } from "../app-runtime-state";
import { runFilePath } from "../daemon/daemon-lifecycle";

export async function disposeBrowserViewHost(): Promise<void> {
	const host = ctx.browserViewHost;
	ctx.browserViewHost = null;
	const profileIpc = ctx.browserProfileIpc;
	ctx.browserProfileIpc = null;
	profileIpc?.dispose();
	const profileImporter = ctx.browserProfileImporter;
	ctx.browserProfileImporter = null;
	if (!host && !profileImporter) return;
	const cleanup = Promise.resolve()
		.then(async () => {
			await profileImporter?.dispose();
			await host?.dispose();
		})
		.catch((error) => {
			console.error("browser host cleanup failed:", error);
		});
	browserCleanupPromises.add(cleanup);
	void cleanup.then(
		() => browserCleanupPromises.delete(cleanup),
		() => browserCleanupPromises.delete(cleanup),
	);
	await cleanup;
}

export function browserProfileStateDir(): string {
	const runFile = runFilePath();
	return path.dirname(runFile ?? path.join(os.homedir(), HOME_DIR_NAME, "running.json"));
}

export async function clearElectronBrowserProfileData(partition: string): Promise<void> {
	const browserSession = session.fromPartition(partition);
	await Promise.all([browserSession.clearStorageData(), browserSession.clearCache()]);
}

export async function disposeAllBrowserViewHosts(): Promise<void> {
	for (;;) {
		if (ctx.browserViewHost) await disposeBrowserViewHost();
		const pending = [...browserCleanupPromises];
		if (pending.length === 0 && !ctx.browserViewHost) return;
		if (pending.length > 0) await Promise.all(pending);
	}
}
