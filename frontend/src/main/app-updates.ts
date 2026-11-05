import { app, type IpcMain, type WebContents } from "electron";
import path from "node:path";
import { createAutoUpdater, type AutoUpdaterController } from "./auto-updater";
import { createMacUpdater } from "./mac-updater";
import { createUpdaterLog } from "./updater-log";
import { agentsBusy } from "./idle-agents";
import { readBuildDirty, readBuildKind, readSigningTeam } from "./update-eligibility";
import { readManagedUpdatePolicy } from "./update-policy";
import { coerceUpdateSettings, type UpdatePreferences, type UpdateStatus } from "./update-settings";

export const UPDATE_STATUS_CHANNEL = "appUpdates:status";

/** The .app bundle of the running executable (…/Tallpine.app). */
function bundlePath(): string {
	return path.resolve(process.resourcesPath, "..", "..");
}

let controller: AutoUpdaterController | null = null;

/** Wire the updater and its IPC. Safe to call on any build: dev runs report "disabled". */
export function initAppUpdates(options: { ipcMain: Pick<IpcMain, "handle">; stateDir: string | null; shell: () => WebContents | null; onUpdateRestart: () => void; onUpdateRestartFailed: () => void; daemonPort: () => number | null }): void {
	const { ipcMain, stateDir } = options;
	ipcMain.handle("appUpdates:getStatus", (): UpdateStatus => controller?.getStatus() ?? { state: "disabled", reason: "unpackaged" });
	ipcMain.handle("appUpdates:getPreferences", async (): Promise<UpdatePreferences | null> => controller?.getPreferences() ?? null);
	ipcMain.handle("appUpdates:setSettings", async (_event, raw: unknown) => {
		await controller?.setSettings(coerceUpdateSettings(raw));
	});
	ipcMain.handle("appUpdates:check", async () => controller?.checkNow());
	ipcMain.handle("appUpdates:install", () => controller?.install() ?? false);
	ipcMain.handle("appUpdates:installWhenIdle", () => controller?.installWhenIdle() ?? false);
	ipcMain.handle("appUpdates:cancelInstallWhenIdle", () => controller?.cancelInstallWhenIdle());
	if (!stateDir) return;
	const updaterLog = createUpdaterLog(stateDir, (line) => console.warn(`updater: ${line.trimEnd()}`));
	controller = createAutoUpdater({
		stateDir,
		log: updaterLog.log,
		createUpdater: () => createMacUpdater(stateDir, updaterLog.logger),
		environment: async () => {
			const packaged = app.isPackaged;
			return {
				packaged,
				platform: process.platform,
				version: app.getVersion(),
				buildDirty: packaged ? await readBuildDirty(process.resourcesPath) : undefined,
				buildKind: packaged ? await readBuildKind(process.resourcesPath) : undefined,
				teamId: packaged && process.platform === "darwin" ? await readSigningTeam(bundlePath()) : null,
			};
		},
		readPolicy: () => readManagedUpdatePolicy(),
		onStatus: (status) => {
			const shell = options.shell();
			if (shell && !shell.isDestroyed()) shell.send(UPDATE_STATUS_CHANNEL, status);
		},
		beforeInstall: () => {
			console.info("updater: restarting to install the staged update");
			options.onUpdateRestart();
		},
		installFailed: () => options.onUpdateRestartFailed(),
		agentsBusy: () => agentsBusy(options.daemonPort()),
	});
	void controller.start();
	app.once("will-quit", () => controller?.dispose());
}
