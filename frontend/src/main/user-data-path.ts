import path from "node:path";
import { HOME_DIR_NAME } from "../shared/brand";

/**
 * Electron's `userData`, which cache, cookies, storage, crash dumps and
 * sessionData all derive from. Always below ~/.tallpine: never Electron's macOS
 * default under ~/Library/Application Support (see CLAUDE.md).
 */
export function resolveUserDataDir(input: { isPackaged: boolean; home: string; devOverride?: string }): string {
	return input.isPackaged
		? path.join(input.home, HOME_DIR_NAME, "electron")
		: (input.devOverride ?? path.join(input.home, HOME_DIR_NAME, "dev", "electron"));
}
