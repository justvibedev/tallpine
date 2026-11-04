// Embedded Info.plist for the macOS daemon binary. The daemon is a bare Mach-O
// under Tallpine.app/Contents/Resources/daemon/, launched by launchd, so TCC
// (System Settings > Privacy & Security) would name it after its file
// ("tallpine"). A plist linked into __TEXT,__info_plist gives it a bundle name
// and identifier without wrapping it in a bundle. Names come from
// src/shared/brand.ts, the single source for the Electron side.
import { readFileSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const brandPath = join(resolve(dirname(fileURLToPath(import.meta.url)), ".."), "src", "shared", "brand.ts");

export function readBrandConstant(name, source = readFileSync(brandPath, "utf8")) {
	const match = new RegExp(`export const ${name} = "([^"]+)";`).exec(source);
	if (!match) throw new Error(`brand.ts does not define ${name}`);
	return match[1];
}

const xmlEscape = (s) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");

export function daemonInfoPlist({ productName, bundleId, version }) {
	const entries = [
		["CFBundleName", productName],
		["CFBundleDisplayName", productName],
		["CFBundleIdentifier", `${bundleId}.daemon`],
		["CFBundleExecutable", "tallpine"],
		["CFBundlePackageType", "APPL"],
		["CFBundleVersion", version],
		["CFBundleShortVersionString", version],
		["LSUIElement", true],
	];
	const body = entries
		.map(([k, v]) => `\t<key>${k}</key>\n\t${v === true ? "<true/>" : `<string>${xmlEscape(v)}</string>`}`)
		.join("\n");
	return `<?xml version="1.0" encoding="UTF-8"?>\n<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">\n<plist version="1.0">\n<dict>\n${body}\n</dict>\n</plist>\n`;
}

// Writes the plist to plistPath and returns the Go -ldflags fragment that links
// it into the binary. Embedding needs the system linker (-linkmode=external).
export function daemonPlistLdflags(plistPath, version) {
	const plist = daemonInfoPlist({
		productName: readBrandConstant("PRODUCT_NAME"),
		bundleId: readBrandConstant("BUNDLE_ID"),
		version: version.replace(/[^0-9.]/g, "") || "0",
	});
	writeFileSync(plistPath, plist);
	if (/[\s"']/.test(plistPath)) throw new Error(`plist path must not contain spaces or quotes: ${plistPath}`);
	return `-linkmode=external -extldflags=-Wl,-sectcreate,__TEXT,__info_plist,${plistPath}`;
}
