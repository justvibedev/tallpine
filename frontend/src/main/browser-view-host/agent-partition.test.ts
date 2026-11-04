import { describe, expect, it, vi } from "vitest";
import { AgentLoginGrants, chooseAgentPartition } from "./agent-partition";
import { isTemporaryBrowserPartition } from "./entry-state";

const base = {
	sessionPartition: "persist:vo-browser-profile-1",
	agentPartition: undefined,
	granted: false,
};

describe("chooseAgentPartition", () => {
	it("uses a new throwaway partition for a named profile without a grant", () => {
		const chosen = chooseAgentPartition(base);
		expect(chosen.created).toBe(true);
		expect(chosen.partition).not.toBe(base.sessionPartition);
		expect(isTemporaryBrowserPartition(chosen.partition)).toBe(true);
	});

	it("reuses the session's throwaway partition", () => {
		expect(chooseAgentPartition({ ...base, agentPartition: "persist:tmp-x" })).toEqual({
			partition: "persist:tmp-x",
			created: false,
		});
	});

	it("uses the session partition when logins are granted", () => {
		expect(chooseAgentPartition({ ...base, granted: true })).toEqual({
			partition: base.sessionPartition,
			created: false,
		});
	});

	it("gives a temporary session's agent a throwaway partition of its own too", () => {
		const temporary = { ...base, sessionPartition: "persist:ao-browser-temporary-s" };
		const chosen = chooseAgentPartition(temporary);
		expect(chosen.created).toBe(true);
		expect(chosen.partition).not.toBe(temporary.sessionPartition);
	});
});

describe("AgentLoginGrants", () => {
	it("does nothing without a confirm hook", () => {
		const onGrant = vi.fn();
		new AgentLoginGrants().request("s", "p", "Work", onGrant);
		expect(onGrant).not.toHaveBeenCalled();
	});

	it("returns before the dialog is answered and grants once it is", async () => {
		let answer!: (granted: boolean) => void;
		const confirm = vi.fn(() => new Promise<boolean>((resolve) => (answer = resolve)));
		const onGrant = vi.fn();
		new AgentLoginGrants(confirm).request("s", "p", "Work", onGrant);
		expect(confirm).toHaveBeenCalledTimes(1);
		expect(onGrant).not.toHaveBeenCalled();
		answer(true);
		await vi.waitFor(() => expect(onGrant).toHaveBeenCalledTimes(1));
	});

	it("shows at most one dialog per session and profile", () => {
		const confirm = vi.fn(() => new Promise<boolean>(() => undefined));
		const grants = new AgentLoginGrants(confirm);
		grants.request("s", "p", "Work", vi.fn());
		grants.request("s", "p", "Work", vi.fn());
		expect(confirm).toHaveBeenCalledTimes(1);
		grants.request("s", "other", "Home", vi.fn());
		expect(confirm).toHaveBeenCalledTimes(2);
	});

	it("treats decline and a failing confirm as no grant", async () => {
		const onGrant = vi.fn();
		const declined = vi.fn(async () => false);
		new AgentLoginGrants(declined).request("s", "p", "Work", onGrant);
		new AgentLoginGrants(async () => {
			throw new Error("no window");
		}).request("s", "p", "Work", onGrant);
		await vi.waitFor(() => expect(declined).toHaveBeenCalled());
		await Promise.resolve();
		expect(onGrant).not.toHaveBeenCalled();
	});
});
