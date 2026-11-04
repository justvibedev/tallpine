import { closeSync, openSync, readSync, statSync } from "node:fs";

// @electron/osx-sign walks the whole bundle and runs `codesign --timestamp` on
// every file isbinaryfile calls binary: images, fonts, the asar, locale packs.
// None of those can carry a code signature of their own (they are sealed by the
// enclosing bundle's resource seal), and each call is a round trip to Apple's
// timestamp server. macSignIgnore is osx-sign's `ignore` hook: it drops
// everything that is not a Mach-O image or a bundle. It never skips the
// .app/.framework bundles themselves, so every enclosing seal is still
// rewritten. Packaged tmux, cloudflared and Node arrive unsigned, so there is
// no valid prior signature to reuse; every Mach-O is signed.

const MH_MAGICS = new Set([0xfeedface, 0xfeedfacf, 0xcefaedfe, 0xcffaedfe]);
const FAT_MAGICS = new Set([0xcafebabe, 0xcafebabf]);
// Matches macho-archs.ts: real universal binaries carry 1-2 slices, and the
// bound rejects Java class files, which share FAT_MAGIC.
const MAX_FAT_ARCHES = 16;

// Magic-number sniff only (no cputype check): any Mach-O osx-sign used to sign
// must still be signed, including a slice we do not otherwise understand.
export function isMachOFile(filePath: string): boolean {
	let fd: number;
	try {
		fd = openSync(filePath, "r");
	} catch {
		return false;
	}
	try {
		const header = Buffer.alloc(8);
		if (readSync(fd, header, 0, 8, 0) < 8) return false;
		const magic = header.readUInt32BE(0);
		if (MH_MAGICS.has(magic)) return true;
		if (FAT_MAGICS.has(magic)) {
			const count = header.readUInt32BE(4);
			return count >= 1 && count <= MAX_FAT_ARCHES;
		}
		return false;
	} catch {
		return false;
	} finally {
		closeSync(fd);
	}
}

export function macSignIgnore(filePath: string): boolean {
	let stat;
	try {
		stat = statSync(filePath);
	} catch {
		return false;
	}
	if (stat.isDirectory()) return false;
	return !isMachOFile(filePath);
}
