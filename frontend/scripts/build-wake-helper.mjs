import { spawnSync } from "node:child_process";
import { mkdirSync, copyFileSync, readFileSync, writeFileSync, rmSync } from "node:fs";
import { homedir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
const frontend = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
export function wakeHelperBuildOptions(arch, root = frontend) {
  const cpu = arch === "arm64" ? "arm64" : arch === "x64" ? "x86_64" : undefined;
  if (!cpu) throw new Error(`Unsupported macOS wake helper architecture: ${arch}`);
  return {
    output: path.join(root, "wake-helper", "tallpine-wake"),
    args: ["swiftc", "-target", `${cpu}-apple-macosx11.0`, "-O",
      path.join(root, "native/wake-helper/WakePolicy.swift"), path.join(root, "native/wake-helper/WakeLowPower.swift"), path.join(root, "native/wake-helper/WakeEffects.swift"), path.join(root, "native/wake-helper/WakeLights.swift"), path.join(root, "native/wake-helper/WakeBrowserActions.swift"), path.join(root, "native/wake-helper/WakeNetworkActions.swift"), path.join(root, "native/wake-helper/main.swift")],
  };
}
const STAMP = ".stamp";
// Clang module caches bake absolute paths and the toolchain/SDK they were built with; wipe on any change.
export function moduleCacheStamp(cache, run = spawnSync) {
  const out = (args) => { const r = run("xcrun", args, { encoding: "utf8" }); return r && r.status === 0 ? String(r.stdout ?? "").trim() : ""; };
  return JSON.stringify({ cache, swiftc: out(["swiftc", "--version"]), sdk: out(["--show-sdk-version"]) });
}
export function ensureModuleCache(cache, stamp, { readFile = readFileSync, writeFile = writeFileSync, remove = rmSync, ensureDirectory = mkdirSync } = {}) {
  let current;
  try { current = readFile(path.join(cache, STAMP), "utf8"); } catch { current = undefined; }
  if (current !== stamp) remove(cache, { recursive: true, force: true });
  ensureDirectory(cache, { recursive: true });
  if (current !== stamp) writeFile(path.join(cache, STAMP), stamp);
  return current === stamp;
}
export function buildWakeHelper({ arch = process.arch, platform = process.platform, run = spawnSync, ensureDirectory = mkdirSync, cacheFs = {} } = {}) {
  if (platform !== "darwin") throw new Error("The wake helper must be built on macOS");
  const { output, args } = wakeHelperBuildOptions(arch);
  ensureDirectory(path.dirname(output), { recursive: true });
  const cache = path.join(process.env.TALLPINE_BUILD_CACHE_DIR || path.join(homedir(), ".tallpine", "build-cache"), "wake-helper", arch);
  ensureModuleCache(cache, moduleCacheStamp(cache, run), { ensureDirectory, ...cacheFs });
  const result = run("xcrun", [...args, "-module-cache-path", cache, "-o", output], { stdio: "inherit" });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`macOS wake helper compilation failed (${result.status})`);
  copyFileSync(path.join(frontend, "native/wake-helper/THIRD_PARTY_NOTICES.md"), path.join(path.dirname(output), "THIRD_PARTY_NOTICES.md"));
}
if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const args = process.argv.slice(2);
  if (args.length === 1 && args[0] === "--if-supported") {
    if (process.platform === "darwin") buildWakeHelper();
    process.exit(0);
  }
  if (args.length !== 0 && (args.length !== 2 || args[0] !== "--arch")) throw new Error("Usage: build-wake-helper.mjs [--arch arm64|x64]");
  buildWakeHelper({ arch: args[1] ?? process.arch });
}
