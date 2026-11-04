import { spawnSync } from "node:child_process";
import { cpus, release, tmpdir } from "node:os";
import {
	chmodSync,
	copyFileSync,
	existsSync,
	mkdirSync,
	readFileSync,
	renameSync,
	rmSync,
	writeFileSync,
} from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { buildCacheRoot, deriveCacheKey, publishBundle, sweepStaleDirs, sha256File, createManifest, verifyManifest } from "./tmux-build-cache.mjs";
import { BUNDLED_TMUX_SHA256, BUNDLED_TMUX_VERSION } from "./tmux-version.mjs";

const TMUX = {
	name: "tmux",
	version: BUNDLED_TMUX_VERSION,
	url: `https://github.com/tmux/tmux/releases/download/${BUNDLED_TMUX_VERSION}/tmux-${BUNDLED_TMUX_VERSION}.tar.gz`,
	sha256: BUNDLED_TMUX_SHA256,
	directory: `tmux-${BUNDLED_TMUX_VERSION}`,
	license: "COPYING",
};
const LIBEVENT = {
	name: "libevent",
	version: "2.1.12-stable",
	url: "https://github.com/libevent/libevent/releases/download/release-2.1.12-stable/libevent-2.1.12-stable.tar.gz",
	sha256: "92e6de1be9ec176428fd2367677e61ceffc2ee1cb119035037a27d346b0403bb",
	directory: "libevent-2.1.12-stable",
	license: "LICENSE",
};
const NCURSES = {
	name: "ncurses",
	version: "6.5",
	url: "https://invisible-mirror.net/archives/ncurses/ncurses-6.5.tar.gz",
	sha256: "136d91bc269a9a5785e5f9e980bc76ab57428f604ce3e5a5a90cebc767971cc6",
	directory: "ncurses-6.5",
	license: "COPYING",
};
const UTF8PROC = {
	name: "utf8proc",
	version: "2.10.0",
	url: "https://github.com/JuliaStrings/utf8proc/archive/refs/tags/v2.10.0.tar.gz",
	sha256: "6f4f1b639daa6dca9f80bc5db1233e9cbaa31a67790887106160b33ef743f136",
	directory: "utf8proc-2.10.0",
	license: "LICENSE.md",
};
const SOURCES = [TMUX, LIBEVENT, NCURSES, UTF8PROC];
// Linux intentionally links glibc dynamically. A fully static glibc tmux can
// enter __libc_early_init with an unset page size and SIGFPE on new-session
// even though `tmux -V` succeeds (#4616).

const scriptsDir = dirname(fileURLToPath(import.meta.url));
const frontendRoot = resolve(scriptsDir, "..");
const repoRoot = resolve(frontendRoot, "..");
const outputRoot = join(frontendRoot, "tmux");
const outputBinary = join(outputRoot, "bin", "tmux");

if (process.platform !== "darwin" && process.platform !== "linux") {
	rmSync(outputRoot, { recursive: true, force: true });
	console.log("Bundled tmux is only built for macOS and Linux.");
	process.exit(0);
}

// Source/build caches live under the app state root (~/.tallpine/build-cache), shared
// across worktrees and outside frontend/ (Electron Packager treats that
// directory as the app root). The cache holds unsigned products only; signing
// happens later on the copy placed in the packaged app.
const toolchain = toolchainFingerprint();
const cacheKey = deriveCacheKey({
	platform: process.platform,
	arch: process.arch,
	sources: SOURCES,
	scriptHash: sha256File(fileURLToPath(import.meta.url)),
	toolchain,
});
const cacheBase = buildCacheRoot();
const downloadsRoot = join(cacheBase, "downloads");
const bundleDir = join(cacheBase, cacheKey);
const workRoot = join(cacheBase, `work-${process.pid}`);
const sourceRoot = join(workRoot, "src");
const prefix = join(workRoot, "prefix");
const cachedBinary = join(bundleDir, "bin", "tmux");
const REQUIRED = ["bin/tmux", ...SOURCES.map((source) => `licenses/${source.name}.txt`)];

mkdirSync(downloadsRoot, { recursive: true });

// fail() exits via process.exit, which skips finally blocks; clean our scratch
// dirs on exit and sweep any left by dead processes.
sweepStaleDirs(cacheBase);
process.on("exit", () => {
	rmSync(workRoot, { recursive: true, force: true });
	rmSync(join(cacheBase, `staging-${process.pid}`), { recursive: true, force: true });
});
const started = Date.now();
let cacheHit = verifyManifest(bundleDir, cacheKey, REQUIRED);
if (!cacheHit) {
	if (existsSync(bundleDir)) console.log("Bundled tmux cache failed verification; rebuilding.");
	try {
		await build();
	} finally {
		rmSync(workRoot, { recursive: true, force: true });
	}
}
console.log(`Bundled tmux build step: ${cacheHit ? "cache hit" : "cold build"} in ${((Date.now() - started) / 1000).toFixed(1)}s (${cacheKey})`);

rmSync(outputRoot, { recursive: true, force: true });
mkdirSync(join(outputRoot, "bin"), { recursive: true });
mkdirSync(join(outputRoot, "licenses"), { recursive: true });
copyFileSync(cachedBinary, outputBinary);
chmodSync(outputBinary, 0o755);
for (const source of SOURCES) {
	copyFileSync(join(bundleDir, "licenses", `${source.name}.txt`), join(outputRoot, "licenses", `${source.name}.txt`));
}
writeFileSync(
	join(outputRoot, "VERSIONS.json"),
	JSON.stringify(
		{
			tmux: TMUX.version,
			libevent: LIBEVENT.version,
			ncurses: NCURSES.version,
			utf8proc: UTF8PROC.version,
			platform: process.platform,
			arch: process.arch,
		},
		null,
		2,
	) + "\n",
);

const version = run(outputBinary, ["-V"], { capture: true }).stdout.trim();
if (version !== `tmux ${TMUX.version}`) {
	fail(`bundled tmux reported ${JSON.stringify(version)}, expected ${JSON.stringify(`tmux ${TMUX.version}`)}`);
}
// The attach smoke test waits for its session to end (~10s). Its result depends only on the
// binary bytes and the host OS, so a pass is stamped in the cache dir and reused on later builds.
const smokeStamp = join(bundleDir, "smoke-ok");
const smokeKey = `${sha256File(outputBinary)} ${process.platform} ${release()}`;
let smokeCached = false;
try {
	smokeCached = readFileSync(smokeStamp, "utf8").trim() === smokeKey;
} catch {
	// no stamp yet
}
if (smokeCached) {
	console.log("Bundled tmux terminfo attach check: cached pass");
} else {
	verifyTerminfoAttach(outputBinary);
	writeFileSync(smokeStamp, `${smokeKey}\n`);
}
console.log(`Bundled ${version} at ${outputBinary}`);

async function build() {
	for (const source of SOURCES) await download(source);

	rmSync(workRoot, { recursive: true, force: true });
	mkdirSync(sourceRoot, { recursive: true });
	mkdirSync(prefix, { recursive: true });
	for (const source of SOURCES) {
		run("tar", ["-xzf", archivePath(source), "-C", sourceRoot]);
	}

	const commonEnv = {
		...process.env,
		...(process.platform === "darwin"
			? { MACOSX_DEPLOYMENT_TARGET: process.arch === "arm64" ? "11.0" : "10.15" }
			: {}),
		CPPFLAGS: `-I${join(prefix, "include")} -I${join(prefix, "include", "ncursesw")}`,
		LDFLAGS: `-L${join(prefix, "lib")}`,
		PKG_CONFIG_PATH: [join(prefix, "lib", "pkgconfig"), join(prefix, "lib64", "pkgconfig")].join(":"),
	};
	const jobs = String(Math.max(2, Math.min(cpus().length, 8)));

	const ncursesDir = join(sourceRoot, NCURSES.directory);
	run(
		"./configure",
		[
			`--prefix=${prefix}`,
			"--without-shared",
			"--with-normal",
			"--without-debug",
			"--without-ada",
			"--without-cxx",
			"--without-cxx-binding",
			"--without-tests",
			"--without-manpages",
			"--enable-widec",
			"--with-termlib",
			"--enable-pc-files",
			`--with-pkg-config-libdir=${join(prefix, "lib", "pkgconfig")}`,
		],
		{ cwd: ncursesDir, env: commonEnv },
	);
	run("make", ["-j", jobs], { cwd: ncursesDir, env: commonEnv });
	// Runtime.Attach always presents xterm-256color. Generate its fallback
	// with the matching pinned tic/infocmp we just built: macOS's older host
	// tools cannot compile the ncurses 6.5 entry. Rebuilding embeds the entry
	// into libtinfo so the shipped binary needs no host terminfo database.
	const ncursesBuildDir = join(ncursesDir, "ncurses");
	const fallback = run(
		"sh",
		[
			"-e",
			"./tinfo/MKfallback.sh",
			join(prefix, "share", "terminfo"),
			"../misc/terminfo.src",
			"../progs/tic",
			"../progs/infocmp",
			"xterm-256color",
		],
		{ cwd: ncursesBuildDir, env: commonEnv },
	);
	writeFileSync(join(ncursesBuildDir, "fallback.c"), fallback.stdout);
	run("make", ["-j", jobs], { cwd: ncursesDir, env: commonEnv });
	run("make", ["install.libs", "install.includes"], { cwd: ncursesDir, env: commonEnv });

	const libeventDir = join(sourceRoot, LIBEVENT.directory);
	run(
		"./configure",
		[
			`--prefix=${prefix}`,
			"--disable-shared",
			"--enable-static",
			"--disable-openssl",
			"--disable-samples",
			"--disable-tests",
			"--disable-regress",
			"--disable-benchmarks",
		],
		{ cwd: libeventDir, env: commonEnv },
	);
	run("make", ["-j", jobs], { cwd: libeventDir, env: commonEnv });
	run("make", ["install"], { cwd: libeventDir, env: commonEnv });

	const utf8procDir = join(sourceRoot, UTF8PROC.directory);
	run("make", ["libutf8proc.a", "libutf8proc.pc", `prefix=${prefix}`], { cwd: utf8procDir, env: commonEnv });
	copyFileSync(join(utf8procDir, "libutf8proc.a"), join(prefix, "lib", "libutf8proc.a"));
	copyFileSync(join(utf8procDir, "utf8proc.h"), join(prefix, "include", "utf8proc.h"));
	copyFileSync(join(utf8procDir, "libutf8proc.pc"), join(prefix, "lib", "pkgconfig", "libutf8proc.pc"));

	const tmuxDir = join(sourceRoot, TMUX.directory);
	// 3.8 refuses to configure without an explicit allocator choice.
	const tmuxConfigureArgs = ["--enable-utf8proc", "--disable-jemalloc"];
	const tmuxEnv = {
		...commonEnv,
		LIBTINFO_CFLAGS: `-I${join(prefix, "include", "ncursesw")} -I${join(prefix, "include")}`,
		LIBTINFO_LIBS: join(prefix, "lib", "libtinfow.a"),
		LIBUTF8PROC_CFLAGS: `-I${join(prefix, "include")}`,
		LIBUTF8PROC_LIBS: join(prefix, "lib", "libutf8proc.a"),
	};
	run("./configure", tmuxConfigureArgs, { cwd: tmuxDir, env: tmuxEnv });
	run("make", ["-j", jobs], { cwd: tmuxDir, env: tmuxEnv });

	const builtBinary = join(tmuxDir, "tmux");
	verifyPortableLinkage(builtBinary, prefix);
	// Assemble into a sibling temp dir and rename, so a crash never leaves a
	// half-written entry that looks complete.
	const staging = join(cacheBase, `staging-${process.pid}`);
	rmSync(staging, { recursive: true, force: true });
	mkdirSync(join(staging, "bin"), { recursive: true });
	mkdirSync(join(staging, "licenses"), { recursive: true });
	copyFileSync(builtBinary, join(staging, "bin", "tmux"));
	chmodSync(join(staging, "bin", "tmux"), 0o755);
	for (const source of SOURCES) {
		copyFileSync(join(sourceRoot, source.directory, source.license), join(staging, "licenses", `${source.name}.txt`));
	}
	const files = Object.fromEntries(REQUIRED.map((rel) => [rel, sha256File(join(staging, rel))]));
	writeFileSync(join(staging, "manifest.json"), createManifest(cacheKey, files));
	publishBundle(staging, bundleDir, cacheKey, REQUIRED);
}

async function download(source) {
	const archive = archivePath(source);
	if (existsSync(archive) && sha256(archive) === source.sha256) return;
	rmSync(archive, { force: true });
	const part = `${archive}.part-${process.pid}`;
	console.log(`Downloading ${source.name} ${source.version}...`);
	let lastError;
	for (let attempt = 1; attempt <= 3; attempt += 1) {
		rmSync(part, { force: true });
		try {
			const response = await fetch(source.url, { redirect: "follow" });
			if (!response.ok) throw new Error(`HTTP ${response.status}`);
			writeFileSync(part, Buffer.from(await response.arrayBuffer()));
			if (sha256(part) !== source.sha256) throw new Error("sha256 mismatch");
			renameSync(part, archive);
			return;
		} catch (error) {
			lastError = error;
			if (attempt < 3) await new Promise((resolveDelay) => setTimeout(resolveDelay, attempt * 1000));
		}
	}
	rmSync(part, { force: true });
	fail(`failed to download ${source.url}: ${lastError instanceof Error ? lastError.message : String(lastError)}`);
}

function archivePath(source) {
	return join(downloadsRoot, `${source.name}-${source.version}.tar.gz`);
}

function sha256(file) {
	return sha256File(file);
}

function toolchainFingerprint() {
	const cc = spawnSync("cc", ["--version"], { encoding: "utf8" });
	const clang = (cc.stdout || "").split("\n")[0].trim() || "unknown-cc";
	const target = process.platform === "darwin" ? (process.arch === "arm64" ? "11.0" : "10.15") : "n/a";
	return `${clang};deployment-target=${target}`;
}

function verifyPortableLinkage(binary, buildPrefix) {
	if (process.platform === "darwin") {
		const output = run("otool", ["-L", binary], { capture: true }).stdout;
		const external = output
			.split("\n")
			.slice(1)
			.map((line) => line.trim().split(" ")[0])
			.filter(Boolean)
			.filter((library) => !library.startsWith("/usr/lib/") && !library.startsWith("/System/Library/"));
		if (external.length > 0 || output.includes(buildPrefix)) {
			fail(`bundled tmux has non-system dylib dependencies:\n${output}`);
		}
		return;
	}
	const output = run("ldd", [binary], { capture: true, allowFailure: true });
	const linkage = `${output.stdout}\n${output.stderr}`;
	if (/libevent|libncurses|libtinfo|libutf8proc|not found/.test(linkage) || linkage.includes(buildPrefix)) {
		fail(`bundled tmux has unbundled runtime dependencies:\n${linkage}`);
	}
}

function verifyTerminfoAttach(binary) {
	const identity = `tallpine-tmux-smoke-${process.pid}`;
	const socket = join(tmpdir(), `${identity}.sock`);
	const session = "terminfo";
	const missingTerminfo = join(tmpdir(), identity, "missing-terminfo");
	const attachEnvironment = {
		...process.env,
		HOME: join(tmpdir(), identity, "missing-home"),
		TERM: "xterm-256color",
		TERMINFO: missingTerminfo,
		TERMINFO_DIRS: missingTerminfo,
	};
	run(binary, ["-S", socket, "-f", "/dev/null", "new-session", "-d", "-s", session, "sleep 10"]);
	try {
		const tmuxArgs = [binary, "-S", socket, "-u", "-T", "RGB", "attach-session", "-t", session];
		const result =
			process.platform === "darwin"
				? run("script", ["-q", "/dev/null", ...tmuxArgs], {
						capture: true,
						allowFailure: true,
						ignoreStdin: true,
						env: attachEnvironment,
					})
				: run("script", ["-q", "-e", "-c", tmuxArgs.map(shellQuote).join(" "), "/dev/null"], {
						capture: true,
						allowFailure: true,
						ignoreStdin: true,
						env: attachEnvironment,
					});
		const output = `${result.stdout}\n${result.stderr}`;
		if (result.status !== 0 || /terminfo database/i.test(output)) {
			fail(`bundled tmux could not attach with TERM=xterm-256color:\n${output}`);
		}
	} finally {
		run(binary, ["-S", socket, "kill-server"], { capture: true, allowFailure: true });
		rmSync(socket, { force: true });
	}
}

function shellQuote(value) {
	return `'${value.replaceAll("'", `'\\''`)}'`;
}

function run(command, args, options = {}) {
	const capture = options.capture || process.env.TALLPINE_VERBOSE_NATIVE_BUILD !== "1";
	const result = spawnSync(command, args, {
		cwd: options.cwd,
		env: options.env,
		encoding: capture ? "utf8" : undefined,
		maxBuffer: 64 * 1024 * 1024,
		stdio: capture ? [options.ignoreStdin ? "ignore" : "pipe", "pipe", "pipe"] : "inherit",
		windowsHide: true,
	});
	if (result.error) fail(`failed to start ${command}: ${result.error.message}`);
	if (!options.allowFailure && result.status !== 0) {
		if (capture) {
			if (result.stdout) process.stderr.write(result.stdout);
			if (result.stderr) process.stderr.write(result.stderr);
		}
		fail(`${command} exited with status ${result.status}`);
	}
	return { status: result.status, stdout: result.stdout ?? "", stderr: result.stderr ?? "" };
}

function fail(message) {
	console.error(message);
	process.exit(1);
}
