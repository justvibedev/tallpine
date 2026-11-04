// @vitest-environment node
import { mkdtempSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { BUNDLE_ID, PRODUCT_NAME } from "../src/shared/brand";
import { daemonInfoPlist, daemonPlistLdflags, readBrandConstant } from "./daemon-info-plist.mjs";

describe("daemon Info.plist", () => {
	it("reads the brand constants the Electron side exports", () => {
		expect(readBrandConstant("PRODUCT_NAME")).toBe(PRODUCT_NAME);
		expect(readBrandConstant("BUNDLE_ID")).toBe(BUNDLE_ID);
		expect(() => readBrandConstant("NOPE")).toThrow(/does not define/);
	});

	it("names the daemon after the product with a distinct identifier", () => {
		const plist = daemonInfoPlist({ productName: PRODUCT_NAME, bundleId: BUNDLE_ID, version: "1.2.3" });
		expect(plist).toContain(`<key>CFBundleName</key>\n\t<string>${PRODUCT_NAME}</string>`);
		expect(plist).toContain(`<key>CFBundleDisplayName</key>\n\t<string>${PRODUCT_NAME}</string>`);
		expect(plist).toContain(`<string>${BUNDLE_ID}.daemon</string>`);
	});

	it("links the written plist into __TEXT,__info_plist via the external linker", () => {
		const path = join(mkdtempSync(join(tmpdir(), "daemon-plist-")), "Info.plist");
		const flags = daemonPlistLdflags(path, "1.2.3-beta");
		expect(flags).toBe(`-linkmode=external -extldflags=-Wl,-sectcreate,__TEXT,__info_plist,${path}`);
		expect(readFileSync(path, "utf8")).toContain("<string>1.2.3</string>");
	});
});
