import { describe, expect, it } from "vitest";
import { assertDroppedFileSize, MAX_DROPPED_FILE_BYTES } from "./dropped-file";

describe("assertDroppedFileSize", () => {
	it("accepts files up to the cap", () => {
		expect(() => assertDroppedFileSize(new Uint8Array(16))).not.toThrow();
	});
	it("rejects oversized and non-binary payloads", () => {
		expect(() => assertDroppedFileSize(new Uint8Array(MAX_DROPPED_FILE_BYTES + 1))).toThrow("too large");
		expect(() => assertDroppedFileSize("abc")).toThrow("invalid");
	});
});
