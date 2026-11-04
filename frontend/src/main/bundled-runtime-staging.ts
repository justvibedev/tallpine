/* Split out of main.ts (B297, P52). */
declare const MAIN_WINDOW_VITE_DEV_SERVER_URL: string | undefined;
declare const MAIN_WINDOW_VITE_NAME: string;
declare const __TALLPINE_TEAM_ID__: string | undefined;
declare const __TALLPINE_BUILD_INFO__: { version: string; commit: string; date: string; dirty: string } | undefined;
import {
app
} from "electron";
import { randomBytes, randomUUID } from "node:crypto";
import { existsSync } from "node:fs";
import { chmod, copyFile, mkdir, rename, rm } from "node:fs/promises";
import path from "node:path";
import { bundledTmuxBinaryPath } from "../shared/bundled-tmux";
import { desktopDataDir } from "./app-bootstrap";
import { ctx } from "./app-context";
import { resolveBrowserRuntimeToken } from "./browser-runtime-token";
import { bundledTmuxStagingPath } from "./desktop-data-dir";
import { ensureVerifiedStagedCopy } from "./helper-signature";



// One id per app launch, minted eagerly so every daemon spawn in this process
// (including supervisor restarts) reports the same run. An explicit
// TALLPINE_APP_RUN_ID in the environment wins, which lets a test or a wrapper pin it.
export const appRunId = process.env.TALLPINE_APP_RUN_ID ?? `apprun-${randomUUID()}`;

// The browser runtime token identifies this app to the daemon's runtime broker.
// It is derived from an owner-only key in the data dir (beside daemon.token) so
// it survives an app restart: a daemon kept alive by TALLPINE_KEEP_DAEMON (or adopted
// under a pinned TALLPINE_APP_RUN_ID) outlives the launch that spawned it, and a
// per-launch token left that daemon unable to authenticate this app. Falls back
// to a per-launch token when the key cannot be persisted.
export const browserRuntimeToken = (() => {
	try {
		return resolveBrowserRuntimeToken(desktopDataDir);
	} catch (error) {
		console.warn(`Tallpine: browser runtime key unavailable (${String(error)}); using a per-launch token`);
		return randomBytes(32).toString("base64url");
	}
})();


export function helperSignatureOptions() {
	return {
		isPackaged: app.isPackaged,
		platform: process.platform,
		teamId: typeof __TALLPINE_TEAM_ID__ === "undefined" ? "" : __TALLPINE_TEAM_ID__,
	};
}


export async function ensureBundledTmuxStaged(): Promise<void> {
	const source = bundledTmuxBinaryPath(app.isPackaged, process.resourcesPath, process.platform);
	const destination = bundledTmuxStagingPath(
		desktopDataDir,
		app.isPackaged,
		app.getVersion(),
		process.platform,
		process.arch,
	);
	if (!source || !destination) {
		ctx.stagedBundledTmuxBinary = null;
		return;
	}
	await mkdir(path.dirname(destination), { recursive: true, mode: 0o750 });
	const sigError = await ensureVerifiedStagedCopy(source, destination, helperSignatureOptions(), {
		exists: existsSync,
		remove: (p) => rm(p, { force: true }),
		copy: async (src, dest) => {
			const temporary = `${dest}.tmp-${process.pid}-${randomUUID()}`;
			try {
				await copyFile(src, temporary);
				await chmod(temporary, 0o755);
				await rename(temporary, dest);
			} finally {
				await rm(temporary, { force: true });
			}
		},
	});
	if (sigError) throw Object.assign(new Error(sigError), { code: "signature_invalid" });
	ctx.stagedBundledTmuxBinary = destination;
}
