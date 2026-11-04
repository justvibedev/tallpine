import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

// pair.tsx has no screen tests, so pin the one rule that matters: a link alone must not start a pairing.
const source = readFileSync(new URL("../app/pair.tsx", import.meta.url), "utf8");

describe("pairing from a link", () => {
	it("asks the user before pairing and consumes each link once", () => {
		const effect = source.slice(source.indexOf("const linkedUrl = ExpoLinking.useURL()"));
		const alertAt = effect.indexOf("Alert.alert(");
		const pairAt = effect.indexOf("onScan({ data: linkedUrl })");
		expect(alertAt).toBeGreaterThan(-1);
		expect(pairAt).toBeGreaterThan(alertAt);
		expect(effect.slice(0, alertAt)).not.toContain("onScan(");
		expect(effect).toContain("consumedPairingLinks.has(linkedUrl)");
	});
});
