import { defineConfig } from "vite";
import { fileURLToPath } from "node:url";
import { resolveBuildInfo, teamIdFromIdentity } from "./scripts/build-info.mjs";

// better-sqlite3 is a native module rebuilt for Electron by Forge. Keep it out
// of Vite's bundle so the packaged app loads the rebuilt binary from
// node_modules (the auto-unpack plugin moves that binary outside app.asar).
export default defineConfig({
	resolve: { alias: { "@tallpine/shared": fileURLToPath(new URL("../packages/shared", import.meta.url)) } },
	// Build metadata for the About dialog (see scripts/build-info.mjs).
	define: {
		__TALLPINE_BUILD_INFO__: JSON.stringify(resolveBuildInfo()),
		// Developer ID team the helper-signature check pins to; empty for unsigned builds.
		__TALLPINE_TEAM_ID__: JSON.stringify(teamIdFromIdentity((process.env.TALLPINE_SIGN_IDENTITY || process.env.APPLE_SIGNING_IDENTITY || "").trim())),
	},
	build: {
		rollupOptions: {
			external: ["better-sqlite3"],
		},
	},
});
