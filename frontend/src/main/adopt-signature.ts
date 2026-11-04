import { existsSync } from "node:fs";
import path from "node:path";
import { type HelperSignatureOptions, verifyBundledHelpers } from "./helper-signature";

/** Bundled helpers Tallpine runs or adopts on macOS; shared by the spawn and adopt checks. */
export function bundledHelperPaths(daemonCommand: string, resourcesPath: string, platform: NodeJS.Platform): string[] {
	const helpers = [daemonCommand];
	if (platform === "darwin") {
		helpers.push(
			path.join(resourcesPath, "wake-helper", "tallpine-wake"),
			path.join(resourcesPath, "cloudflared", "cloudflared"),
			path.join(resourcesPath, "acp-runtime", "node", "bin", "node"),
		);
	}
	return helpers.filter((p) => existsSync(p));
}

/**
 * Re-verifies the on-disk helpers before Tallpine takes over an already-running
 * daemon (attach instead of spawn). Returns an error message, or null when the
 * daemon may be adopted. Only bundled daemons are checked.
 */
export async function adoptionSignatureError(
	launch: { source: string; command: string },
	resourcesPath: string,
	opts: HelperSignatureOptions,
	log?: (msg: string) => void,
): Promise<string | null> {
	if (launch.source !== "bundled") return null;
	return verifyBundledHelpers(bundledHelperPaths(launch.command, resourcesPath, opts.platform), opts, log);
}
