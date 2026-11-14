import { describe, expect, it, vi } from "vitest";
import { type BrowserNavState } from "./browser-view-host";
import type { BrowserProfile } from "../shared/browser-profiles";
import { setupTabHost, fakeBrowserProfileStore } from "./browser-view-host-test-helpers";

vi.mock("./render-check-proxy", () => ({ startRenderCheckProxy: vi.fn(async () => 4321) }));

vi.mock("electron", async (importOriginal) => {
	const actual = await importOriginal<typeof import("electron")>();
	return { ...actual, net: { fetch: vi.fn() } };
});

// B691: an agent select of the person's own tab needs a login grant.
describe("agent tab-select of a person-owned tab", () => {
	const profile: BrowserProfile = {
		id: "22222222-2222-4222-8222-222222222222",
		name: "Work",
		createdAt: "2026-01-01T00:00:00.000Z",
		updatedAt: "2026-01-01T00:00:00.000Z",
	};
	const named = () => fakeBrowserProfileStore(profile, { "worker-1": profile.id });

	it("refuses a select of the person's tab until logins are granted, raising the dialog and creating no tab", async () => {
		const confirmAgentLogins = vi.fn(async () => false);
		const { host, invoke, constructorOptions, runtime } = setupTabHost(named(), false, undefined, undefined, undefined, {
			confirmAgentLogins,
		});
		const nav = (await invoke("browser:ensure", "worker-1")) as BrowserNavState;
		await invoke("browser:openTab", { viewId: nav.viewId, url: "https://app.example/" });
		await invoke("browser:selectTab", { viewId: nav.viewId, tabId: "t1" });
		const before = constructorOptions.length;
		vi.mocked(runtime.runAction).mockClear();

		await expect(host.execute("worker-1", "tab-select", { tabId: "t2" })).rejects.toMatchObject({
			code: "AGENT_LOGINS_NOT_GRANTED",
		});
		expect(confirmAgentLogins).toHaveBeenCalledWith("worker-1", "Work");
		expect(constructorOptions).toHaveLength(before);
		expect(runtime.runAction).not.toHaveBeenCalled();

		const listed = (await host.execute("worker-1", "tabs", {})) as { tabs: Array<{ id: string; agentDrivable: boolean }> };
		expect(listed.tabs.map((tab) => [tab.id, tab.agentDrivable])).toEqual([
			["t1", false],
			["t2", false],
		]);
	});

	it("attaches select to the person's tab once they grant logins, with no extra tab", async () => {
		const confirmAgentLogins = vi.fn(async () => true);
		const { host, invoke, constructorOptions, runtime } = setupTabHost(named(), false, undefined, undefined, undefined, {
			confirmAgentLogins,
		});
		const nav = (await invoke("browser:ensure", "worker-1")) as BrowserNavState;
		await invoke("browser:openTab", { viewId: nav.viewId, url: "https://app.example/" });
		await expect(host.execute("worker-1", "tab-select", { tabId: "t2" })).rejects.toMatchObject({
			code: "AGENT_LOGINS_NOT_GRANTED",
		});
		const before = constructorOptions.length;
		await vi.waitFor(async () => {
			await host.execute("worker-1", "tab-select", { tabId: "t2" });
		});
		expect(runtime.runAction).toHaveBeenCalledWith(
			"worker-1",
			"tab-select",
			{ tabId: "t2" },
			expect.anything(),
			undefined,
		);
		expect(constructorOptions).toHaveLength(before);
		const listed = (await host.execute("worker-1", "tabs", {})) as { tabs: Array<{ agentDrivable: boolean }> };
		expect(listed.tabs.every((tab) => tab.agentDrivable)).toBe(true);
	});

	it("refuses a select of the person's tab in a temporary session with a distinct code, raising no dialog", async () => {
		const confirmAgentLogins = vi.fn(async () => true);
		const { host, invoke, constructorOptions } = setupTabHost(undefined, false, undefined, undefined, undefined, {
			confirmAgentLogins,
		});
		await invoke("browser:ensure", "worker-1");
		const before = constructorOptions.length;
		await expect(host.execute("worker-1", "tab-select", { tabId: "t1" })).rejects.toMatchObject({
			code: "TAB_NOT_AGENT_DRIVABLE",
		});
		expect(confirmAgentLogins).not.toHaveBeenCalled();
		expect(constructorOptions).toHaveLength(before);
	});
});
