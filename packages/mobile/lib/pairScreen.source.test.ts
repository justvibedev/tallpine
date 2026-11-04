import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { pairSource } from "./screenSource";

// The scanner's lifecycle around a successful pairing, checked on the source
// because the screen needs a camera and a native stack to render.
const src = pairSource();

describe("pair screen", () => {
	it("stops the camera while a code is being paired", () => {
		expect(src).toMatch(/active=\{!busy && !stalled\}/);
		expect(src).toMatch(/onBarcodeScanned=\{busy \|\| stalled \? undefined : onScan\}/);
	});

	it("leaves only through leave(), which waits for the name sheet to settle", () => {
		const leave = src.slice(src.indexOf("async function leave()"), src.indexOf("// Manual connect"));
		expect(leave).toContain("await waitForSheetToSettle(");
		expect(leave.indexOf("waitForSheetToSettle")).toBeLessThan(leave.indexOf("backOr(router)"));
		// Every other backOr in the screen is the explicit back button.
		const rest = src.replace(leave, "");
		expect(rest.match(/backOr\(router\)/g)).toEqual(["backOr(router)"]);
	});

	it("does not leave a spinner up when sessions fail to load after pairing", () => {
		const complete = src.slice(src.indexOf("async function completePairing"), src.indexOf("async function onScan"));
		expect(complete).toContain('step("load", "failed")');
		expect(complete).toContain("setStalled(true)");
		expect(complete).toContain("setBusy(false)");
	});
});
