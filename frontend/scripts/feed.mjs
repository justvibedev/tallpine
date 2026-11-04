// Generates the electron-updater feed (latest-mac.yml, plus <pre>-mac.yml for
// a prerelease) and the zip's blockmap sidecar for a release. Restored from
// upstream and trimmed to macOS, the only platform Tallpine updates.
// scripts/release-local publish runs it on the directory it uploads.
//
//   node scripts/feed.mjs <dir> <version>
import { readdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { writeBlockmap } from "./blockmap.mjs";

// Electron 44 requires macOS 13 (Ventura, Darwin 22); the packaged app declares
// LSMinimumSystemVersion 13.0. electron-updater compares this against
// os.release(), so it is the Darwin kernel version, not the marketing version.
export const MAC_MINIMUM_DARWIN_VERSION = "22.0.0";

// selectMacZips picks the versioned darwin zips, arm64 first (the updater's
// MacUpdater.filterFilesForArch keys on the literal "arm64").
export function selectMacZips(filenames, version) {
	const zips = filenames.filter((f) => f.endsWith(".zip") && f.includes("darwin") && f.includes(version));
	return [...zips.filter((f) => f.includes("arm64")), ...zips.filter((f) => !f.includes("arm64"))];
}

// feedFilenames: latest-mac.yml always; a prerelease (1.2.0-beta.1) also gets
// beta-mac.yml, the channel file electron-updater asks a prerelease for.
export function feedFilenames(version) {
	const pre = /^[^-]+-([A-Za-z]+)/.exec(version)?.[1];
	return pre ? ["latest-mac.yml", `${pre}-mac.yml`] : ["latest-mac.yml"];
}

// buildYml serializes the feed. files is [{ url, sha512, size }]; the
// deprecated top-level path/sha512 point at files[0].
export function buildYml(version, files, releaseDate) {
	const lines = [`version: ${version}`, "files:"];
	for (const f of files) {
		lines.push(`  - url: ${f.url}`);
		lines.push(`    sha512: ${f.sha512}`);
		lines.push(`    size: ${f.size}`);
	}
	lines.push(`path: ${files[0].url}`);
	lines.push(`sha512: ${files[0].sha512}`);
	lines.push(`releaseDate: '${releaseDate}'`);
	lines.push(`minimumSystemVersion: ${MAC_MINIMUM_DARWIN_VERSION}`);
	return `${lines.join("\n")}\n`;
}

export async function generateMacFeed(dir, version, releaseDate) {
	const names = selectMacZips(readdirSync(dir), version);
	if (names.length === 0) throw new Error(`no darwin zip for ${version} in ${dir}`);
	const files = [];
	for (const name of names) {
		const { sha512, size } = await writeBlockmap(join(dir, name));
		files.push({ url: name, sha512, size });
	}
	const yml = buildYml(version, files, releaseDate);
	const written = feedFilenames(version);
	for (const name of written) writeFileSync(join(dir, name), yml);
	return written;
}

if (import.meta.url === `file://${process.argv[1]}`) {
	const [, , dir, version] = process.argv;
	if (!dir || !version) {
		process.stderr.write("usage: node feed.mjs <dir> <version>\n");
		process.exit(2);
	}
	generateMacFeed(dir, version, new Date().toISOString()).catch((err) => {
		process.stderr.write(`${err.stack || err}\n`);
		process.exit(1);
	});
}
