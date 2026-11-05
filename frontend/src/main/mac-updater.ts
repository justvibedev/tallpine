import { MacUpdater } from "electron-updater";
import { ElectronAppAdapter } from "electron-updater/out/ElectronAppAdapter";
import { ElectronHttpExecutor } from "electron-updater/out/electronHttpExecutor";
import path from "node:path";
import type { UpdaterLike } from "./auto-updater";
import type { UpdaterLogger } from "./updater-log";

/**
 * electron-updater keeps its download cache under the OS cache dir by default;
 * Tallpine keeps all of its state under the state root, so the adapter points
 * the cache there. Everything else is electron-updater's own Electron adapter.
 */
export function createMacUpdater(stateDir: string, logger?: UpdaterLogger): UpdaterLike {
	const adapter = new ElectronAppAdapter();
	Object.defineProperty(adapter, "baseCachePath", { get: () => path.join(stateDir, "caches") });
	const updater = new MacUpdater(undefined, adapter);
	// AppUpdater only builds its Electron HTTP executor when it creates the app
	// adapter itself. Handing it ours leaves httpExecutor null, and the first
	// feed request then fails with "Cannot read properties of null (reading 'request')".
	(updater as unknown as { httpExecutor: unknown }).httpExecutor = new ElectronHttpExecutor((authInfo, callback) => updater.emit("login", authInfo, callback));
	if (logger) updater.logger = logger;
	return updater as unknown as UpdaterLike;
}
