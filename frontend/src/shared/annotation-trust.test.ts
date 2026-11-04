import { describe, expect, it } from "vitest";
import { isUserInputEvent } from "./annotation-trust";

describe("isUserInputEvent", () => {
	it("rejects events dispatched by script", () => {
		expect(isUserInputEvent(new MouseEvent("click"))).toBe(false);
	});
});
