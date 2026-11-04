import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const index = readFileSync(new URL("../app/(tabs)/index.tsx", import.meta.url), "utf8");
const unpaired = readFileSync(new URL("./UnpairedState.tsx", import.meta.url), "utf8");

describe("Workers route", () => {
	it("is the tabs group's default screen", () => {
		const layout = readFileSync(new URL("../app/(tabs)/_layout.tsx", import.meta.url), "utf8");
		expect(layout).toContain('initialRouteName: "index"');
	});
	it("never shows a bare spinner while loading", () => {
		expect(index).not.toContain("<ActivityIndicator");
		expect(index).toContain("<WorkersPlaceholder");
	});
	it("explains the unpaired state with a pairing action", () => {
		expect(unpaired).toContain("No computer paired");
		expect(unpaired).toContain("Scan the code in Tallpine → Settings → Connections.");
		expect(unpaired).toContain('router.push("/pair")');
	});
});
