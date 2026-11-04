import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { wakeHelperBuildOptions, buildWakeHelper, ensureModuleCache, moduleCacheStamp } from "./build-wake-helper.mjs";
describe("native wake helper build", () => {
  for (const [arch, cpu] of [["arm64", "arm64"], ["x64", "x86_64"]]) {
    it(`cross-compiles ${arch} without raising the desktop minimum OS`, () => {
      const { args, output } = wakeHelperBuildOptions(arch, "/project/frontend");
      assert.ok(args.includes(`${cpu}-apple-macosx11.0`));
      for (const file of ["WakePolicy.swift", "WakeLowPower.swift", "WakeEffects.swift", "WakeLights.swift", "WakeBrowserActions.swift", "WakeNetworkActions.swift", "main.swift"]) {
        assert.ok(args.includes(`/project/frontend/native/wake-helper/${file}`));
      }
      assert.equal(output, "/project/frontend/wake-helper/tallpine-wake");
    });
  }
  it("rejects non-macOS and invalid architectures", () => {
    assert.throws(() => buildWakeHelper({ platform: "linux" }), /must be built on macOS/);
    assert.throws(() => wakeHelperBuildOptions("universal"), /Unsupported/);
  });
  it("does not mask a failed native compiler", () => {
    assert.throws(() => buildWakeHelper({ platform: "darwin", arch: "arm64", ensureDirectory: () => {}, cacheFs: { readFile: () => { throw new Error("x"); }, writeFile: () => {}, remove: () => {} }, run: () => ({ status: 1 }) }), /compilation failed/);
  });
  describe("module cache stamp", () => {
    const fsFor = (stored, log) => ({ readFile: () => { if (stored === undefined) throw new Error("ENOENT"); return stored; }, writeFile: (f, c) => log.push(["write", c]), remove: (d) => log.push(["rm", d]), ensureDirectory: () => {} });
    it("keeps a cache whose stamp matches", () => {
      const log = [];
      assert.equal(ensureModuleCache("/c", "S", fsFor("S", log)), true);
      assert.deepEqual(log, []);
    });
    it("wipes and restamps when the path, toolchain or SDK changed", () => {
      const log = [];
      assert.equal(ensureModuleCache("/c", "new", fsFor("old", log)), false);
      assert.deepEqual(log, [["rm", "/c"], ["write", "new"]]);
    });
    it("wipes an unstamped cache", () => {
      const log = [];
      ensureModuleCache("/c", "S", fsFor(undefined, log));
      assert.equal(log[0][0], "rm");
    });
    it("stamp covers cache path, swiftc and sdk versions", () => {
      const mk = (sw, sdk) => (_x, a) => ({ status: 0, stdout: a[0] === "swiftc" ? sw : sdk });
      const a = moduleCacheStamp("/a", mk("swift 6", "15.4"));
      assert.notEqual(a, moduleCacheStamp("/b", mk("swift 6", "15.4")));
      assert.notEqual(a, moduleCacheStamp("/a", mk("swift 7", "15.4")));
      assert.notEqual(a, moduleCacheStamp("/a", mk("swift 6", "16")));
    });
  });
  it("wipes a stale cache and stamps it before the compile", () => {
    const log = [];
    const run = (cmd, a) => {
      if (a.includes("-o")) { log.push(["compile", a[a.indexOf("-module-cache-path") + 1]]); return { status: 0 }; }
      return { status: 0, stdout: a[0] === "swiftc" ? "swift 6" : "15.4" };
    };
    const cacheFs = { readFile: () => "stale", writeFile: (f, c) => log.push(["write", c]), remove: (d) => log.push(["rm", d]) };
    try { buildWakeHelper({ platform: "darwin", arch: "arm64", ensureDirectory: () => {}, cacheFs, run }); } catch {}
    const cache = log.find((e) => e[0] === "compile")[1];
    assert.deepEqual(log, [["rm", cache], ["write", moduleCacheStamp(cache, run)], ["compile", cache]]);
  });
});
