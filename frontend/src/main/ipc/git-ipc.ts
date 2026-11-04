/* Split out of main.ts (B297, P52). */
declare const MAIN_WINDOW_VITE_DEV_SERVER_URL: string | undefined;
declare const MAIN_WINDOW_VITE_NAME: string;
declare const __TALLPINE_TEAM_ID__: string | undefined;
declare const __TALLPINE_BUILD_INFO__: { version: string; commit: string; date: string; dirty: string } | undefined;
import os from "node:os";
import { execFileAsync } from "../app-bootstrap";
import { ipcMain } from "../app-runtime-state";
import { daemonEnv } from "../daemon/daemon-lifecycle";
import { ancestorRepositorySetupWarning, resolveCheckedOutBranch, scanImportFolder } from "../import-folder-scan";
import { ensureShellEnv } from "../shell-env-host";



export function registerGitIpc(): void {

	ipcMain.handle("app:checkGitRepository", async (_event, remoteUrl: string) => {
		await ensureShellEnv();
		try {
			await execFileAsync("git", ["ls-remote", "--quiet", remoteUrl, "HEAD"], {
				env: daemonEnv(),
				timeout: 8000,
			});
			return true;
		} catch {
			return false;
		}
	});

	ipcMain.handle("app:scanImportFolder", async (_event, input: { path: string; mode: "project" | "workspace" }) => {
		await ensureShellEnv();
		return scanImportFolder(input.path, input.mode, { env: daemonEnv(), homeDir: os.homedir() });
	});

	ipcMain.handle("app:checkAncestorRepo", async (_event, path: string) => {
		await ensureShellEnv();
		return ancestorRepositorySetupWarning(path, { env: daemonEnv(), homeDir: os.homedir() });
	});

	ipcMain.handle("app:getRepositoryBranch", async (_event, path: string) => {
		await ensureShellEnv();
		return resolveCheckedOutBranch(path, { env: daemonEnv(), homeDir: os.homedir() });
	});
}
