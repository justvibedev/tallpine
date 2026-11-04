import { describe, expect, it } from "vitest";
import { formatBytes } from "./bytes";

describe("formatBytes", () => {
	it.each([
		[0, "0 B"], [-5, "0 B"], [Number.NaN, "0 B"], [512, "512 B"], [1536, "1.5 KB"], [10 * 1024, "10 KB"],
		[5 * 1024 ** 2, "5.0 MB"], [300 * 1024 ** 2, "300 MB"], [3 * 1024 ** 3, "3.0 GB"], [54 * 1024 ** 3, "54 GB"], [2 * 1024 ** 4, "2.0 TB"],
	])("%s -> %s", (n, out) => expect(formatBytes(n)).toBe(out));
});

it("wholeKb keeps KB whole", () => {
	expect(formatBytes(2048, { wholeKb: true })).toBe("2 KB");
	expect(formatBytes(1536, { wholeKb: true })).toBe("2 KB");
	expect(formatBytes(5 * 1024 ** 2, { wholeKb: true })).toBe("5.0 MB");
});
