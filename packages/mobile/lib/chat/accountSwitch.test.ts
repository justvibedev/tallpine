import { describe, expect, it } from "vitest";
import { accountSwitchedDetail } from "./accountSwitch";

describe("accountSwitchedDetail", () => {
	it("names the account the conversation continues on", () => {
		expect(accountSwitchedDetail("work")).toBe("Your next message continues on work");
	});
	it("falls back when the daemon sent no account", () => {
		expect(accountSwitchedDetail(undefined)).toBe("Your next message continues on the new account");
		expect(accountSwitchedDetail("")).toBe("Your next message continues on the new account");
	});
});
