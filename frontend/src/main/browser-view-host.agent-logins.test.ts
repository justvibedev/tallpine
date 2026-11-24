import { describe, expect, it, vi } from "vitest";
import { type BrowserNavState } from "./browser-view-host";
import { type BrowserProfile } from "../shared/browser-profiles";
import { fakeBrowserProfileStore } from "./browser-view-host-fake-profile-store";
import { setupTabHost } from "./browser-view-host-test-helpers";

vi.mock("./render-check-proxy", () => ({ startRenderCheckProxy: vi.fn(async () => 4321) }));

vi.mock("electron", async (importOriginal) => {
	const actual = await importOriginal<typeof import("electron")>();
	return { ...actual, net: { fetch: vi.fn() } };
});

// An agent asked to read a page the person opened in a named-profile panel must
// land on that page, not on a blank throwaway tab beside it.
describe("agent commands on the person's open page", () => {
	const profile: BrowserProfile = {
		id: "22222222-2222-4222-8222-222222222222",
		name: "Work",
		createdAt: "2026-01-01T00:00:00.000Z",
		updatedAt: "2026-01-01T00:00:00.000Z",
	};
	const named = () => fakeBrowserProfileStore(profile, { "worker-1": profile.id });
	const partitions = (constructorOptions: Array<{ webPreferences: Electron.WebPreferences }>) =>
		constructorOptions.map(({ webPreferences }) => webPreferences.partition);

	// The person opened a page in the panel and asked the agent to read it.
	it("waits for the logins answer and reads the person's open page, with no extra tab", async () => {
		let answer!: (granted: boolean) => void;
		const confirmAgentLogins = vi.fn(() => new Promise<boolean>((resolve) => (answer = resolve)));
		const { host, invoke, constructorOptions, runtime } = setupTabHost(named(), false, undefined, undefined, undefined, {
			confirmAgentLogins,
		});
		const nav = (await invoke("browser:ensure", "worker-1")) as BrowserNavState;
		await invoke("browser:navigate", { viewId: nav.viewId, url: "https://app.example/experiments" });

		const read = host.execute("worker-1", "snapshot", {}) as Promise<{ target: { tabId: string } }>;
		await vi.waitFor(() => expect(confirmAgentLogins).toHaveBeenCalledTimes(1));
		expect(constructorOptions).toHaveLength(1);
		answer(true);
		const result = await read;

		expect(result.target.tabId).toBe("t1");
		expect(constructorOptions).toHaveLength(1);
		expect(vi.mocked(runtime.runAction).mock.calls.some(([, action]) => action === "snapshot")).toBe(true);
	});

	it("still moves off the person's page when they decline logins", async () => {
		const confirmAgentLogins = vi.fn(async () => false);
		const { host, invoke, constructorOptions } = setupTabHost(named(), false, undefined, undefined, undefined, {
			confirmAgentLogins,
		});
		const nav = (await invoke("browser:ensure", "worker-1")) as BrowserNavState;
		await invoke("browser:navigate", { viewId: nav.viewId, url: "https://app.example/experiments" });
		const result = (await host.execute("worker-1", "snapshot", {})) as { target: { tabId: string } };

		expect(confirmAgentLogins).toHaveBeenCalledTimes(1);
		expect(result.target.tabId).toBe("t2");
		expect(partitions(constructorOptions)[1]).toMatch(/^persist:ao-browser-temporary-/);
	});

	it("returns from a blank agent tab to the person's page after a late grant, closing the blank tab", async () => {
		let answer!: (granted: boolean) => void;
		const confirmAgentLogins = vi.fn(() => new Promise<boolean>((resolve) => (answer = resolve)));
		const { host, invoke, constructorOptions } = setupTabHost(named(), false, undefined, undefined, undefined, {
			confirmAgentLogins,
		});
		const nav = (await invoke("browser:ensure", "worker-1")) as BrowserNavState;
		// The first command finds a blank person's tab, so it does not wait.
		await host.execute("worker-1", "snapshot", {});
		expect(constructorOptions).toHaveLength(2);
		await invoke("browser:selectTab", { viewId: nav.viewId, tabId: "t1" });
		await invoke("browser:navigate", { viewId: nav.viewId, url: "https://app.example/experiments" });
		await invoke("browser:selectTab", { viewId: nav.viewId, tabId: "t2" });
		answer(true);
		await new Promise((resolve) => setTimeout(resolve, 0));

		const result = (await host.execute("worker-1", "snapshot", {})) as { target: { tabId: string } };
		const tabs = (await host.execute("worker-1", "tabs", {})) as { tabs: Array<{ id: string }> };

		expect(result.target.tabId).toBe("t1");
		expect(tabs.tabs.map((tab) => tab.id)).toEqual(["t1"]);
		expect(constructorOptions).toHaveLength(2);
	});
});
