import type { ForgeConfig } from "@electron-forge/shared-types";
import { AutoUnpackNativesPlugin } from "@electron-forge/plugin-auto-unpack-natives";
import { VitePlugin } from "@electron-forge/plugin-vite";
import { FusesPlugin } from "@electron-forge/plugin-fuses";
import { rebuild } from "@electron/rebuild";
import electronPackage from "electron/package.json";
import MakerNSIS from "./makers/maker-nsis";
import MakerDMG, { isSigningConfigured, sealDmg, verifyDmg, verifyMacArtifact } from "./makers/maker-dmg";
import { machoHasX86_64Slice } from "./makers/macho-archs";
import { macSignIgnore } from "./makers/mac-sign-selection";
import { RELEASE_FUSES, assertPackagedFuses } from "./makers/release-fuses";
import { assertNoEscapingSymlinks } from "./makers/bundle-symlinks";
import { assertMacSigning } from "./makers/release-signing";
import MakerAppImage from "./makers/maker-appimage";
import { existsSync, readdirSync, rmSync } from "node:fs";
import { spawnSync } from "node:child_process";
import { tmpdir } from "node:os";
import path from "node:path";
import { BUNDLED_TMUX_VERSION } from "./scripts/tmux-version.mjs";
import { resolveBuildInfo } from "./scripts/build-info.mjs";
import { writeInstallerManifest } from "./scripts/installer-manifest.mjs";
import { BUNDLE_ID, PRODUCT_NAME, EXECUTABLE_NAME as BRAND_EXECUTABLE_NAME } from "./src/shared/brand";

// The packaged binary name (no extension). Single source of truth: the packager
// names the exe/ELF from this, and the NSIS + deb makers must point their
// shortcut/launcher at the SAME name. Drift here means a broken Start menu
// shortcut on Windows (#2414) or "could not find the Electron app binary" on deb.
const EXECUTABLE_NAME = BRAND_EXECUTABLE_NAME;
const PACKAGED_EXTERNAL_DEPENDENCIES = [
	"/node_modules/better-sqlite3",
	"/node_modules/bindings",
	"/node_modules/file-uri-to-path",
];

// better-sqlite3 runs from its JS and the compiled addon alone. Its C++
// sources, the vendored SQLite amalgamation, build objects, the static
// library and a second copy of the addon under bin/ were ~27 MB of app.asar.
const BETTER_SQLITE3 = "/node_modules/better-sqlite3";
const BETTER_SQLITE3_RUNTIME = new Set(["/package.json", "/lib", "/build", "/build/Release", "/build/Release/better_sqlite3.node"]);

function ignoreFromVitePackage(file: string): boolean {
	if (!file) return false;
	if (file.startsWith("/.vite")) return false;
	if (file === "/node_modules") return false;
	if (file.startsWith(`${BETTER_SQLITE3}/`)) {
		const runtimePath = file.slice(BETTER_SQLITE3.length);
		return !BETTER_SQLITE3_RUNTIME.has(runtimePath) && !runtimePath.startsWith("/lib/");
	}
	return !PACKAGED_EXTERNAL_DEPENDENCIES.some(
		(dependency) => file === dependency || file.startsWith(`${dependency}/`),
	);
}

async function prepareNativeDependencies(platform: NodeJS.Platform, arch: string): Promise<void> {
	// Rebuild in the source tree, where prebuild-install and its helper packages
	// are available. The Vite package intentionally carries only the resulting
	// native runtime, not the install-time download toolchain.
	await rebuild({
		buildPath: process.cwd(),
		electronVersion: electronPackage.version,
		platform,
		arch,
		onlyModules: ["better-sqlite3"],
		force: true,
	});
}

// Notarytool keychain profile for local notarized releases.
export function resolveNotaryProfile(env: NodeJS.ProcessEnv = process.env): string | undefined {
	return env.TALLPINE_NOTARY_PROFILE || undefined;
}

export function extraResourcesForPlatform(platform: NodeJS.Platform): string[] {
	return [
		"daemon",
		"agent-browser",
		"installer-manifest.json",
		"resources/acp-runtime",
		...(platform === "darwin" ? ["wake-helper", "cloudflared"] : []),
		...(platform === "darwin" || platform === "linux" ? ["tmux"] : []),
		"assets/icon.png",
		"assets/icon.ico",
		"assets/trayIconTemplate.png",
		"assets/trayIconTemplate@2x.png",
	];
}

const ACP_RUNTIME_NODE_PATH = "/Contents/Resources/acp-runtime/node/bin/node";
// V8 uses MAP_JIT on arm64 and mprotect on x64. electron-osx-sign's default
// allows only the former, so its re-signing makes the bundled Node crash on
// Intel (#3879). Which entitlements a file needs is a property of its bytes,
// not of the signing machine: @electron/osx-sign hands optionsForFile only the
// path, and one signing host may sign both the arm64 and the x64 artifact —
// the canonical release signer does exactly that. So the selector below keys
// off the Mach-O header of the file being signed (thin x64, or a universal
// carrying an x64 slice), never off process.arch. The public CI build stays
// unsigned, so this hook runs for locally signed builds and stands as the
// reference implementation the canonical signer mirrors; see
// frontend/docs/desktop-release.md.
export const MAC_APP_ENTITLEMENTS = path.resolve("assets/entitlements.mac.plist");
const ACP_RUNTIME_NODE_ENTITLEMENTS = [
	"com.apple.security.cs.allow-jit",
	"com.apple.security.cs.allow-unsigned-executable-memory",
];

export function macSignOptionsForFile(filePath: string): { entitlements?: string[] | string } {
	// Cheap gate first: optionsForFile is invoked for every Mach-O in the
	// bundle, and only the nested Node path can need an override.
	// Every other file gets the explicit minimal plist instead of
	// @electron/osx-sign's broad defaults (camera, mic, location, usb, ...).
	if (!filePath.endsWith(ACP_RUNTIME_NODE_PATH)) return { entitlements: MAC_APP_ENTITLEMENTS };
	// Fail closed: an unreadable or unparseable binary throws out of
	// optionsForFile and aborts the signing pass. Never fall back to
	// process.arch or to "no entitlements" — silently signing the Intel Node
	// without allow-unsigned-executable-memory is exactly the #3879 crash.
	return machoHasX86_64Slice(filePath) ? { entitlements: ACP_RUNTIME_NODE_ENTITLEMENTS } : { entitlements: MAC_APP_ENTITLEMENTS };
}

// @electron/packager applies extendInfo BEFORE it rewrites the app plist, and
// then sets CFBundleDisplayName to the executable name (lowercase "tallpine"),
// which is what Spotlight and Finder show. extendInfo cannot win, so patch the
// key in afterCopyExtraResources: after the plist rewrite, before the ASAR
// integrity digest and signing. Only the main app is affected; the helper
// bundles are already named from the product name.
export function setMacDisplayName(buildPath: string, platform: string, displayName: string = PRODUCT_NAME): void {
	if (platform !== "darwin" && platform !== "mas") return;
	const app = readdirSync(buildPath).find((entry) => entry.endsWith(".app"));
	if (!app) throw new Error(`packaged macOS app bundle missing from ${buildPath}`);
	const plist = path.join(buildPath, app, "Contents", "Info.plist");
	const result = spawnSync("plutil", ["-replace", "CFBundleDisplayName", "-string", displayName, plist], { encoding: "utf8" });
	if (result.status !== 0) throw new Error(`could not set CFBundleDisplayName in ${plist}: ${result.error?.message || result.stderr}`);
}

// Unsigned local builds come out of electron-packager carrying the stock
// Electron signature identifier (com.github.Electron), which does not match the
// bundle id. Re-sign ad hoc so the identifier follows Info.plist. Builds with a
// real signing identity are already signed by osxSign and are left alone.
export function adHocResignMacApp(appBundle: string, env: NodeJS.ProcessEnv = process.env): boolean {
	if (isSigningConfigured(env)) return false;
	const result = spawnSync("codesign", ["--force", "--deep", "-s", "-", appBundle], { encoding: "utf8" });
	if (result.status !== 0) {
		throw new Error(`ad-hoc re-sign failed for ${appBundle}: ${result.error?.message || result.stderr}`);
	}
	return true;
}

// The commit this build came from, recorded in Info.plist so release tooling
// (scripts/release-local) and `defaults read` can tell which commit an
// installed app runs. scripts/release-local pins these with TALLPINE_BUILD_*.
const buildInfo = resolveBuildInfo();

const notaryProfile = resolveNotaryProfile();

const config: ForgeConfig = {
	packagerConfig: {
		asar: true,
		// The Vite plugin normally packages only .vite. better-sqlite3 must stay
		// external so Electron can load its native binary, so include its minimal
		// runtime dependency tree explicitly; AutoUnpackNativesPlugin then places
		// the .node binary outside app.asar.
		ignore: ignoreFromVitePackage,
		appBundleId: BUNDLE_ID,
		appVersion: buildInfo.version,
		name: PRODUCT_NAME,
		executableName: EXECUTABLE_NAME,
		afterCopyExtraResources: [
			async ({ buildPath, platform }) => {
				setMacDisplayName(buildPath, platform);
			},
		],
		appCategoryType: "public.app-category.developer-tools",
		extendInfo: {
			VOBuildCommit: buildInfo.commit,
			VOBuildDate: buildInfo.date,
			VOBuildDirty: buildInfo.dirty,
			TallpineBuildKind: buildInfo.kind,
		},
		// App icon. electron-packager appends the per-platform extension
		// (.icns on macOS, .ico on Windows); Linux menu icons come from the
		// deb/rpm makers below, and the runtime window icon from src/main.ts.
		icon: "assets/icon",
		extraResource: extraResourcesForPlatform(process.platform),
		// Notarization. Two paths:
		//  - CI: an App Store Connect API key. APPLE_API_KEY is a PATH to the .p8
		//    (the workflow decodes APPLE_API_KEY_BASE64 to a temp file), plus the
		//    key id + issuer uuid. Matches the proven local runbook creds.
		//  - Local: TALLPINE_NOTARY_PROFILE, a notarytool
		//    keychain profile created with `xcrun notarytool store-credentials`.
		//    scripts/release-local exports it from ~/.tallpine/release.env and, when
		//    set, fails the build unless the app is notarized and stapled.
		//    See docs/development.md.
		// Both are valid NotaryToolCredentials, so no cast is needed.
		osxSign: process.env.APPLE_SIGNING_IDENTITY
			? {
					identity: process.env.APPLE_SIGNING_IDENTITY,
					optionsForFile: macSignOptionsForFile,
					ignore: macSignIgnore,
				}
			: process.env.CSC_LINK
				? { optionsForFile: macSignOptionsForFile, ignore: macSignIgnore }
				: undefined,
		osxNotarize: notaryProfile
			? { keychainProfile: notaryProfile }
			: process.env.APPLE_API_KEY
				? {
						appleApiKey: process.env.APPLE_API_KEY,
						appleApiKeyId: process.env.APPLE_API_KEY_ID!,
						appleApiIssuer: process.env.APPLE_API_ISSUER!,
					}
				: undefined,
	},
	hooks: {
		prePackage: async (_forgeConfig, platform, arch) => {
			await prepareNativeDependencies(platform as NodeJS.Platform, arch);
			writeInstallerManifest(path.resolve(".."), path.resolve("installer-manifest.json"));
			if (platform === "darwin") {
                const wakeBuild = spawnSync(process.execPath, [path.resolve("scripts/build-wake-helper.mjs"), "--arch", arch], { stdio: "inherit" });
                if (wakeBuild.error) throw wakeBuild.error;
                if (wakeBuild.status !== 0) throw new Error("macOS wake helper build failed");
			}
		},
		packageAfterPrune: async (_forgeConfig, buildPath) => {
			const nativeModule = path.join(
				buildPath,
				"node_modules",
				"better-sqlite3",
				"build",
				"Release",
				"better_sqlite3.node",
			);
			if (!existsSync(nativeModule)) {
				throw new Error("Packaged app is missing the better-sqlite3 native runtime");
			}
		},
		// Assert the native resource survived Electron Packager's copy/asar/sign
		// pipeline. A source build succeeding is not enough: a missing extraResource
		// would otherwise publish an app that silently fell back to machine tmux.
		postPackage: async (_forgeConfig, packageResult) => {
			// Read the fuses back from the packaged binary: the plugin flipping them
			// is not proof the shipped bytes carry them.
			for (const outputPath of packageResult.outputPaths) {
				if (packageResult.platform === "darwin") {
					const bundle = readdirSync(outputPath).find((entry) => entry.endsWith(".app"));
					if (!bundle) throw new Error(`packaged macOS app bundle missing from ${outputPath}`);
					// Before signing: codesign --strict rejects links leaving the bundle.
					assertNoEscapingSymlinks(path.join(outputPath, bundle));
					for (const warning of assertMacSigning(path.join(outputPath, bundle), isSigningConfigured())) console.warn(warning);
					await assertPackagedFuses(path.join(outputPath, bundle));
				} else {
					const exe = path.join(outputPath, packageResult.platform === "win32" ? `${EXECUTABLE_NAME}.exe` : EXECUTABLE_NAME);
					await assertPackagedFuses(exe);
				}
			}
			if (packageResult.platform !== "darwin" && packageResult.platform !== "linux") return;
			for (const outputPath of packageResult.outputPaths) {
				let resourcesPath = path.join(outputPath, "resources");
				if (packageResult.platform === "darwin") {
					const appBundle = readdirSync(outputPath).find((entry) => entry.endsWith(".app"));
					if (!appBundle) throw new Error(`packaged macOS app bundle missing from ${outputPath}`);
					adHocResignMacApp(path.join(outputPath, appBundle));
					resourcesPath = path.join(outputPath, appBundle, "Contents", "Resources");
					if (!existsSync(path.join(resourcesPath, "wake-helper", "tallpine-wake"))) throw new Error(`packaged macOS wake helper missing from ${resourcesPath}`);
					// Run it: remote access depends on this signed copy starting, not just existing.
					const cloudflared = path.join(resourcesPath, "cloudflared", "cloudflared");
					if (!existsSync(cloudflared)) throw new Error(`packaged cloudflared missing from ${cloudflared}`);
					const cloudflaredVersion = spawnSync(cloudflared, ["--version"], { encoding: "utf8", timeout: 10_000 });
					if (cloudflaredVersion.status !== 0) throw new Error(`packaged cloudflared failed to run at ${cloudflared}: ${cloudflaredVersion.error?.message || cloudflaredVersion.stderr}`);
				}
				const binary = path.join(resourcesPath, "tmux", "bin", "tmux");
				if (!existsSync(binary)) throw new Error(`packaged tmux missing from ${binary}`);
				const version = spawnSync(binary, ["-V"], { encoding: "utf8" });
				if (version.status !== 0 || version.stdout.trim() !== `tmux ${BUNDLED_TMUX_VERSION}`) {
					throw new Error(`packaged tmux failed verification at ${binary}: ${version.stderr || version.stdout}`);
				}
				const socket = path.join(tmpdir(), `tallpine-tmux-smoke-${process.pid}.sock`);
				const smoke = spawnSync(binary, ["-S", socket, "-f", "/dev/null", "new-session", "-d", "true"], {
					encoding: "utf8",
					timeout: 5_000,
				});
				try {
					if (smoke.error || smoke.status !== 0) {
						const detail = smoke.error?.message || smoke.stderr || smoke.stdout;
						throw new Error(`packaged tmux could not create a session at ${binary}: ${detail}`);
					}
				} finally {
					spawnSync(binary, ["-S", socket, "kill-server"], { stdio: "ignore" });
					rmSync(socket, { force: true });
				}
			}
		},
		// The dmg container is NOT signed, notarized or stapled by any maker
		// (neither Forge's maker-dmg nor app-builder-lib's dmg target does it), and
		// the .app's own stapled ticket does not propagate through an unsealed
		// container. So seal it here, after the maker has produced it, reusing the
		// same credentials packagerConfig already consumes (#3267 decision 3).
		// The .app inside was already signed + notarized + stapled by
		// packagerConfig above, before any maker ran; nothing here touches it.
		//
		// Then PROVE the seal. sealDmg exiting 0 only says three commands ran on
		// this machine; it does not say Gatekeeper accepts the published bytes with
		// a stapled ticket, or that a bundled nested executable (the Intel ACP
		// Node, #3879) actually runs. verify-mac-artifact.sh is the canonical gate
		// for both (#3288 workstreams 1 and 2; #3879 for the nested-Node check),
		// and #3267 decision 3 step 4 asks for exactly this check on the dmg. Run
		// only when sealDmg actually sealed: an unsigned local or desktop-testing
		// build has nothing to verify and must keep producing its dmg.
		//
		// The zip needs the SAME nested-Node check but no separate sealing step:
		// its inner .app was already signed/notarized/stapled by packagerConfig
		// above, so verifying it only needs the same "was this build actually
		// signed" gate sealDmg uses (isSigningConfigured), not a seal call of its
		// own. Without this, the dmg-only check left the maker-zip artifact — the
		// macOS zip artifact, and per-arch
		// the artifact CI ships for x64 — completely unverified: a regression of
		// exactly the crash #3879 fixed could ship without any automated gate
		// catching it.
		postMake: async (_forgeConfig, makeResults) => {
			for (const result of makeResults) {
				if (result.platform !== "darwin") continue;
				for (const artifact of result.artifacts) {
					if (artifact.endsWith(".dmg")) {
						if (await sealDmg(artifact)) await verifyDmg(artifact);
					} else if (artifact.endsWith(".zip") && isSigningConfigured()) {
						await verifyMacArtifact(artifact);
					}
				}
			}
			return makeResults;
		},
	},
	rebuildConfig: {},
	makers: [
		// Windows installer: NSIS via electron-builder (see makers/maker-nsis.ts).
		// Replaces Squirrel.Windows, which only does per-user installs with no
		// custom install dir or proper uninstaller (issue #401).
		new MakerNSIS(
			{
				appId: BUNDLE_ID,
				// The name shown in the installer, the updater, the shortcuts, and
				// Windows Apps. It matches the app's display name in main.ts.
				productName: PRODUCT_NAME,
				// Match the packaged binary name so the Start menu shortcut targets
				// the real "tallpine.exe" (not "Tallpine.exe").
				executableName: EXECUTABLE_NAME,
				icon: "assets/icon.ico",
				// Keep the installer filename unchanged. electron-builder names it
				// after productName by default.
				nsis: { artifactName: "Tallpine Setup ${version}.${ext}" },
			},
			["win32"],
		),
		// macOS zip artifact, alongside the dmg below.
		{ name: "@electron-forge/maker-zip", platforms: ["darwin"], config: {} },
		// macOS FIRST-INSTALL artifact, additive to the zip above: a dmg has no
		// user-driven extraction step, so a third-party unzip tool can no longer
		// break the signature seal on the way in (see makers/maker-dmg.ts, #3267).
		new MakerDMG(
			{
				appId: BUNDLE_ID,
				productName: PRODUCT_NAME,
			},
			["darwin"],
		),
		// Linux fetch-and-run artifact for `tallpine start`: a single self-contained
		// AppImage the Go bootstrapper downloads and runs directly (see
		// makers/maker-appimage.ts). The deb/rpm makers below stay for users who
		// prefer a system package.
		new MakerAppImage(
			{
				appId: BUNDLE_ID,
				productName: PRODUCT_NAME,
				icon: "assets/icon.png",
					},
			["linux"],
		),
		{
			name: "@electron-forge/maker-deb",
			config: {
				options: {
					// Must match packagerConfig.executableName, or the deb maker
					// looks for the package name and fails with "could not find
					// the Electron app binary". (Both are "tallpine".)
					bin: EXECUTABLE_NAME,
					icon: "assets/icon.png",
					maintainer: PRODUCT_NAME,
					homepage: "https://github.com/justvibedev/tallpine",
				},
			},
		},
		{
			name: "@electron-forge/maker-rpm",
			config: {
				options: {
					icon: "assets/icon.png",
					// rpmbuild rejects a spec with an empty License field.
					license: "MIT",
					homepage: "https://github.com/justvibedev/tallpine",
				},
			},
		},
	],
	plugins: [
		new AutoUnpackNativesPlugin({}),
		new VitePlugin({
			build: [
				{ entry: "src/main.ts", config: "vite.main.config.ts", target: "main" },
				{ entry: "src/preload.ts", config: "vite.preload.config.ts", target: "preload" },
				{ entry: "src/annotate-preload.ts", config: "vite.preload.config.ts", target: "preload" },
			],
			renderer: [{ name: "main_window", config: "vite.renderer.config.ts" }],
		}),
		// Harden the packaged Electron binary. Flipped at package time, so dev
		// (electron-forge start) and Playwright e2e, which launch the unfused
		// node_modules/electron binary, are unaffected.
		new FusesPlugin(RELEASE_FUSES),
	],
};

export default config;
