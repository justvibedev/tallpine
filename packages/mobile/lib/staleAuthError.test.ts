import { describe, expect, it } from "vitest";
import { shouldClearAuthError } from "./staleAuthError";

describe("shouldClearAuthError", () => {
	it("clears a leftover 401/403 once a configured config resolves (re-pair)", () => {
		expect(shouldClearAuthError(true, true, 401)).toBe(true);
		expect(shouldClearAuthError(true, true, 403)).toBe(true);
	});
	it("keeps the error when nothing configured resolved", () => {
		expect(shouldClearAuthError(false, true, 401)).toBe(false);
	});
	it("keeps the error when the same config re-resolves (upgrade race), so the re-pair prompt stays", () => {
		expect(shouldClearAuthError(true, false, 401)).toBe(false);
	});
	it("leaves non-auth errors alone", () => {
		expect(shouldClearAuthError(true, true, 426)).toBe(false);
		expect(shouldClearAuthError(true, true, null)).toBe(false);
	});
});
