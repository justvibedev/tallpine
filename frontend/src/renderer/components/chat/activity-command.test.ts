import { describe, expect, it } from "vitest";
import { exploredFileCount } from "./activity-command";

describe("exploredFileCount", () => {
	it("counts explicit file arguments, quoted or not", () => {
		expect(exploredFileCount(`cat "./a b.ts" './c.ts' ./d.ts`)).toBe(3);
	});

	it("handles an escaped quote inside a quoted argument", () => {
		expect(exploredFileCount(`cat "./a\\"b.ts" ./c.ts`)).toBe(2);
	});

	// The old pattern matched a backslash two ways, so an unterminated quoted
	// run of backslashes took 2^n steps (60 chars: hours). Hostile agent text
	// must stay linear.
	it.each([`"`, `'`])("stays fast on an unterminated %s run of backslashes", (quote) => {
		const hostile = `cat ${quote}${"\\".repeat(5000)}`;
		const start = performance.now();
		exploredFileCount(hostile);
		expect(performance.now() - start).toBeLessThan(250);
	});
});
