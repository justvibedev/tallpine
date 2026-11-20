// Tallpine preload: caps the core count Node reports to TALLPINE_TEST_PARALLELISM,
// so jest, vitest, webpack and other pools sized from os.cpus() or
// os.availableParallelism() stay inside the session's test budget.
const os = require("node:os");
const n = Number.parseInt(process.env.TALLPINE_TEST_PARALLELISM || "", 10);
if (Number.isInteger(n) && n > 0) {
  const cpus = os.cpus;
  const avail = os.availableParallelism;
  os.cpus = function () {
    return cpus.apply(this, arguments).slice(0, n);
  };
  if (typeof avail === "function") {
    os.availableParallelism = function () {
      return Math.min(avail.apply(this, arguments), n);
    };
  }
}

// Also keeps watchman out of worker runs: the Homebrew daemon adds a macOS
// Files & Folders privacy entry and a persistent watch for every worktree a
// jest/metro run crawls. Jest (jest-haste-map) and Metro probe the binary and
// fall back to the node crawler when it cannot be spawned, so make "watchman"
// resolve to a missing command.
const child = require("node:child_process");
const MISSING = "tallpine-watchman-disabled";
const isWatchman = (c) => typeof c === "string" && /(^|[\\/])watchman$/.test(c);
const shellWatchman = /^(\s*)(?:\S*[\\/])?watchman(?=\s|$)/;
// exec/execFile carry util.promisify.custom (resolves {stdout, stderr}); keep it.
const util = require("node:util");
function keepPromisify(orig, wrapped, rewrite) {
  const p = orig[util.promisify.custom];
  if (p) wrapped[util.promisify.custom] = (cmd, ...rest) => p(rewrite(cmd), ...rest);
}
for (const name of ["spawn", "spawnSync", "execFile", "execFileSync"]) {
  const orig = child[name];
  const wrapped = function (cmd, ...rest) {
    return orig.call(this, isWatchman(cmd) ? MISSING : cmd, ...rest);
  };
  keepPromisify(orig, wrapped, (cmd) => (isWatchman(cmd) ? MISSING : cmd));
  child[name] = wrapped;
}
for (const name of ["exec", "execSync"]) {
  const orig = child[name];
  const rewrite = (cmd) => (typeof cmd === "string" ? cmd.replace(shellWatchman, "$1" + MISSING) : cmd);
  const wrapped = function (cmd, ...rest) {
    return orig.call(this, rewrite(cmd), ...rest);
  };
  keepPromisify(orig, wrapped, rewrite);
  child[name] = wrapped;
}
try {
  require("node:module").syncBuiltinESMExports();
} catch {}
