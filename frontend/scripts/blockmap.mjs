// frontend/scripts/blockmap.mjs (restored from upstream, trimmed to macOS)
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);

// The ONE fragile import. app-builder-lib 26.x generates blockmaps in pure JS.
// This internal path is pinned via package-lock; if it moves on a major
// upgrade, only this file changes. Smoke-tested by blockmap.test.mjs.
const { buildBlockMap } = require("app-builder-lib/out/targets/blockmap/blockmap.js");

// writeBlockmap creates "<filePath>.blockmap" (gzip sidecar) and returns the
// file's base64 sha512 + byte size, exactly as electron-updater reads them from
// the feed yml. blockMapSize is not surfaced: MacUpdater fetches the sidecar.
export async function writeBlockmap(filePath, destination = `${filePath}.blockmap`) {
	const { sha512, size } = await buildBlockMap(filePath, "gzip", destination);
	return { sha512, size };
}
