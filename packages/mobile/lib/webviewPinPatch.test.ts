import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

// The gateway's self-signed cert is only trustworthy when it matches the pin, so
// react-native-webview must consult the same pin store on both platforms.
const patch = readFileSync(new URL("../patches/react-native-webview+14.0.1.patch", import.meta.url), "utf8");

describe("react-native-webview pin patch", () => {
	it("checks the pin before cancelling an Android SSL error", () => {
		expect(patch).toContain("tallpineMatchesPin(error)");
		expect(patch).toContain("ee.justdev.tallpine.pinning.PinStore");
		expect(patch.indexOf("tallpineMatchesPin(error)")).toBeLessThan(patch.indexOf("// Cancel request after obtaining top-level URL"));
	});
	it("checks the pin on the iOS server-trust challenge", () => {
		expect(patch).toContain("trust:matchesPinForHost:port:");
		expect(patch).toContain("NSURLAuthenticationMethodServerTrust");
	});
	it("never accepts a certificate without a pin match", () => {
		expect(patch).not.toMatch(/\+\s*handler\.proceed\(\);\s*\n\+\s*return;\s*\n\+\s*}\s*\n\+\s*\n\+\s*\/\/ Cancel/);
		expect(patch).toContain("if (tallpineMatchesPin(error))");
	});

	// Release builds run R8; the patched WebView client finds PinStore by name.
	it("keeps PinStore out of R8's reach", () => {
		const rules = readFileSync(new URL("../modules/tallpine-pinning/android/consumer-rules.pro", import.meta.url), "utf8");
		const gradle = readFileSync(new URL("../modules/tallpine-pinning/android/build.gradle", import.meta.url), "utf8");
		expect(rules).toContain("-keep class ee.justdev.tallpine.pinning.PinStore");
		expect(gradle).toContain("consumerProguardFiles 'consumer-rules.pro'");
	});
});
