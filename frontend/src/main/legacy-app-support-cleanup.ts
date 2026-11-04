import { readdir, rm } from "node:fs/promises";
import path from "node:path";
import { PRODUCT_NAME } from "../shared/brand";

// What an older build or an un-pinned dev run left in Electron's default
// macOS location: Chromium caches and the Local State file. Anything else in
// the directory (cookies, storage, a user's data) means it is not stale, so
// nothing is removed.
const DISPOSABLE = new Set([
	"Local State",
	"GPUCache",
	"DawnCache",
	"DawnGraphiteCache",
	"DawnWebGPUCache",
	"GrShaderCache",
	"ShaderCache",
	"Code Cache",
	"Cache",
	"component_crx_cache",
	"extensions_crx_cache",
	"Crashpad",
	".DS_Store",
]);

/**
 * Removes `<appSupport>/<product name>` when it holds only caches and Local
 * State. App state belongs under ~/.tallpine only, so this strays from an old build.
 * Returns true when it removed the directory.
 */
export async function removeStaleAppSupportDir(appSupportDir: string, productName: string = PRODUCT_NAME): Promise<boolean> {
	if (!appSupportDir || !productName) return false;
	const dir = path.join(appSupportDir, productName);
	let names: string[];
	try {
		names = await readdir(dir);
	} catch {
		return false;
	}
	if (names.some((name) => !DISPOSABLE.has(name))) return false;
	await rm(dir, { recursive: true, force: true });
	return true;
}
