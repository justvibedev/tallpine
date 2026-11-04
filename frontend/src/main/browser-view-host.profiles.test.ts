import {
	describe,
	expect,
	it,
	vi,
} from "vitest";
import { type BrowserNavState } from "./browser-view-host";
import { browserProfilePartition, type BrowserProfile } from "../shared/browser-profiles";
import type { BrowserHistoryStore } from "./browser-history-store";
import {
	setupTabHost,
	fakeBrowserProfileStore,
} from "./browser-view-host-test-helpers";

vi.mock("./render-check-proxy", () => ({ startRenderCheckProxy: vi.fn(async () => 4321) }));

vi.mock("electron", async (importOriginal) => {
	const actual = await importOriginal<typeof import("electron")>();
	return { ...actual, net: { fetch: vi.fn() } };
});


describe("browser profile partitions and replacement", () => {
	const profile: BrowserProfile = {
		id: "22222222-2222-4222-8222-222222222222",
		name: "Work",
		createdAt: "2026-01-01T00:00:00.000Z",
		updatedAt: "2026-01-01T00:00:00.000Z",
	};

	it("keeps temporary workers isolated while sharing a worker's tabs", async () => {
		const { constructorOptions, invoke } = setupTabHost();
		const first = (await invoke("browser:ensure", "worker-1")) as BrowserNavState;
		await invoke("browser:ensure", "worker-2");
		await invoke("browser:openTab", { viewId: first.viewId });

		const firstPartition = constructorOptions[0]!.webPreferences.partition;
		const secondPartition = constructorOptions[1]!.webPreferences.partition;
		const firstTabPartition = constructorOptions[2]!.webPreferences.partition;
		expect(firstPartition).toMatch(/^persist:ao-browser-temporary-/);
		expect(secondPartition).toMatch(/^persist:ao-browser-temporary-/);
		expect(firstPartition).not.toBe(secondPartition);
		expect(firstTabPartition).toBe(firstPartition);
		expect(constructorOptions.every(({ webPreferences }) => webPreferences.plugins === true)).toBe(true);
	});

	it("clears temporary persisted partitions when their browser session is destroyed", async () => {
		const { clearBrowserProfileData, constructorOptions, host, invoke } = setupTabHost();
		const nav = (await invoke("browser:ensure", "worker-1")) as BrowserNavState;
		const partition = constructorOptions[0]!.webPreferences.partition!;

		host.destroy(nav.viewId);

		await vi.waitFor(() => expect(clearBrowserProfileData).toHaveBeenCalledWith(partition));
		expect(partition).toMatch(/^persist:ao-browser-temporary-/);
	});

	it("waits for temporary persisted partition cleanup before host disposal finishes", async () => {
		let releaseCleanup!: () => void;
		const cleanupFinished = vi.fn();
		const clearBrowserProfileData = vi.fn(
			async (_partition: string) =>
				new Promise<undefined>((resolve) => {
					releaseCleanup = () => {
						cleanupFinished();
						resolve(undefined);
					};
				}),
		);
		const { constructorOptions, host, invoke, runtime } = setupTabHost(
			undefined,
			false,
			undefined,
			undefined,
			clearBrowserProfileData,
		);
		await invoke("browser:ensure", "worker-1");
		const partition = constructorOptions[0]!.webPreferences.partition!;

		const disposal = host.dispose();
		let disposed = false;
		void disposal.then(() => {
			disposed = true;
		});
		await new Promise<void>((resolve) => setImmediate(resolve));

		expect(clearBrowserProfileData).toHaveBeenCalledWith(partition);
		expect(disposed).toBe(false);
		expect(runtime.dispose).not.toHaveBeenCalled();
		expect(cleanupFinished).not.toHaveBeenCalled();

		releaseCleanup();
		await disposal;

		expect(disposed).toBe(true);
		expect(runtime.dispose).toHaveBeenCalled();
	});

	describe("agent-driven tabs", () => {
		const named = () => fakeBrowserProfileStore(profile, { "worker-1": profile.id });
		const partitions = (constructorOptions: Array<{ webPreferences: Electron.WebPreferences }>) =>
			constructorOptions.map(({ webPreferences }) => webPreferences.partition);

		it("opens agent tabs in a throwaway partition without imported logins by default", async () => {
			const { host, invoke, constructorOptions, clearBrowserProfileData } = setupTabHost(named());
			const nav = (await invoke("browser:ensure", "worker-1")) as BrowserNavState;
			await host.execute("worker-1", "tab-new", { url: "https://example.com/" });

			const [human, agent] = partitions(constructorOptions);
			expect(human).toBe(browserProfilePartition(profile.id));
			expect(agent).toMatch(/^persist:ao-browser-temporary-/);
			host.destroy(nav.viewId);
			await vi.waitFor(() => expect(clearBrowserProfileData).toHaveBeenCalledWith(agent));
			expect(clearBrowserProfileData).not.toHaveBeenCalledWith(human);
		});

		it("keeps the person's own new tabs in the profile, without asking about logins", async () => {
			const confirmAgentLogins = vi.fn(async () => true);
			const { invoke, constructorOptions, runtime } = setupTabHost(
				named(),
				false,
				undefined,
				undefined,
				undefined,
				{ confirmAgentLogins },
			);
			const nav = (await invoke("browser:ensure", "worker-1")) as BrowserNavState;
			await invoke("browser:openTab", { viewId: nav.viewId, url: "https://example.com/" });

			expect(runtime.runAction).toHaveBeenCalledWith(
				"worker-1",
				"tab-new",
				expect.anything(),
				expect.anything(),
			);
			expect(partitions(constructorOptions)).toEqual(Array(2).fill(browserProfilePartition(profile.id)));
			expect(confirmAgentLogins).not.toHaveBeenCalled();
		});

		it("opens an agent tab-new on a session with no tabs in the throwaway partition", async () => {
			const { host, invoke, constructorOptions } = setupTabHost(named());
			const nav = (await invoke("browser:ensure", "worker-1")) as BrowserNavState;
			await invoke("browser:closeTab", { viewId: nav.viewId, tabId: "t1" });
			const before = constructorOptions.length;
			await host.execute("worker-1", "tab-new", { url: "https://example.com/" });

			expect(constructorOptions).toHaveLength(before + 1);
			expect(partitions(constructorOptions)[before]).toMatch(/^persist:ao-browser-temporary-/);
		});

		it("reuses one agent tab instead of opening a new one each time the person selects their own", async () => {
			const { host, invoke, constructorOptions } = setupTabHost(named());
			const nav = (await invoke("browser:ensure", "worker-1")) as BrowserNavState;
			await host.execute("worker-1", "open", { url: "https://example.com/" });
			await invoke("browser:selectTab", { viewId: nav.viewId, tabId: "t1" });
			const second = await host.execute("worker-1", "open", { url: "https://example.org/" });

			expect(constructorOptions).toHaveLength(2);
			expect((second as { target: { tabId: string } }).target.tabId).toBe("t2");
		});

		it("redirects a queued command when the person selects their own tab meanwhile", async () => {
			const { host, invoke, runtime } = setupTabHost(named());
			const nav = (await invoke("browser:ensure", "worker-1")) as BrowserNavState;
			await host.execute("worker-1", "open", { url: "https://example.com/" });
			let release!: () => void;
			const held = new Promise<void>((resolve) => (release = resolve));
			vi.mocked(runtime.runAction).mockImplementationOnce(async () => {
				await held;
				return {};
			});
			const first = host.execute("worker-1", "open", { url: "https://example.org/" });
			const queued = host.execute("worker-1", "snapshot", {});
			// The selection's UI state changes at once; its native sync queues behind the command.
			const selected = invoke("browser:selectTab", { viewId: nav.viewId, tabId: "t1" });
			release();
			await first;
			const result = (await queued) as { target: { tabId: string } };
			await selected;

			expect(result.target.tabId).toBe("t2");
		});

		it("sends the agent partition through the resolver proxy, loopback direct, and leaves the person's profile alone", async () => {
			const { host, invoke, views } = setupTabHost(named());
			await invoke("browser:ensure", "worker-1");
			await host.execute("worker-1", "tab-new", { url: "https://example.com/" });
			expect(views[1]!.webContents.session.setProxy).toHaveBeenCalledWith({
				proxyRules: "socks5://127.0.0.1:4321",
				proxyBypassRules: "<-loopback>;localhost;*.localhost;127.0.0.1/8;[::1]",
			});
			expect(views[0]!.webContents.session.setProxy).not.toHaveBeenCalled();
			// Its own listener, apart from render checks' (render-check-proxy.test.ts).
			const { startRenderCheckProxy } = await import("./render-check-proxy");
			expect(startRenderCheckProxy).toHaveBeenCalledWith("agent");
			expect(views[1]!.webContents.setWebRTCIPHandlingPolicy).toHaveBeenCalledWith("disable_non_proxied_udp");
			expect(views[0]!.webContents.setWebRTCIPHandlingPolicy).not.toHaveBeenCalled();
			// A popup of the agent tab is a new WebContents and needs the policy too.
			views[1]!.webContents.openWindow("https://example.test/agent-popup", false);
			expect(views[2]!.webContents.setWebRTCIPHandlingPolicy).toHaveBeenCalledWith("disable_non_proxied_udp");
		});

		it("loads nothing in an agent tab when the resolver proxy cannot be set", async () => {
			const { host, invoke, views } = setupTabHost(named());
			await invoke("browser:ensure", "worker-1");
			const { startRenderCheckProxy } = await import("./render-check-proxy");
			vi.mocked(startRenderCheckProxy).mockRejectedValueOnce(new Error("no proxy"));
			await host.execute("worker-1", "tab-new", { url: "https://example.com/" }).catch(() => undefined);
			expect(views[1]!.webContents.loadURL).not.toHaveBeenCalled();
		});

		it("keeps popups of an agent tab in the throwaway partition, and popups of the person's tab in the profile", async () => {
			const { host, invoke, views, constructorOptions } = setupTabHost(named());
			await invoke("browser:ensure", "worker-1");
			await host.execute("worker-1", "tab-new", { url: "https://example.com/" });
			// views[1] is the agent tab, views[0] the person's.
			views[1]!.webContents.openWindow("https://example.test/agent-popup", false);
			views[0]!.webContents.openWindow("https://example.test/human-popup", false);

			const [, , agentPopup, humanPopup] = partitions(constructorOptions);
			expect(agentPopup).toMatch(/^persist:ao-browser-temporary-/);
			expect(agentPopup).toBe(partitions(constructorOptions)[1]);
			expect(humanPopup).toBe(browserProfilePartition(profile.id));
		});

		it("keeps the person's tab-new rules (private IPs allowed) but refuses an agent create of the same URL", async () => {
			const { host, invoke, constructorOptions, runtime } = setupTabHost(named());
			const nav = (await invoke("browser:ensure", "worker-1")) as BrowserNavState;
			await invoke("browser:openTab", { viewId: nav.viewId, url: "http://192.168.1.20:3000/" });
			expect(constructorOptions).toHaveLength(2);

			// An agent-originated create reaching the provider directly (bypassing the
			// tallpine browser URL normalization) is refused as well.
			vi.mocked(runtime.runAction).mockImplementationOnce(async (_s, _a, _args, provider) => {
				await provider.createTarget("http://192.168.1.20:3000/");
				return {};
			});
			await expect(host.execute("worker-1", "tab-select", { tabId: "t1" })).rejects.toThrow(/private-network/);
			expect(constructorOptions).toHaveLength(2);
		});

		it("captures the agent tab when the person selects their own tab while a screenshot is queued", async () => {
			const { host, invoke, runtime } = setupTabHost(named());
			const nav = (await invoke("browser:ensure", "worker-1")) as BrowserNavState;
			await host.execute("worker-1", "open", { url: "https://example.com/" });
			// A person-side native operation holds the queue (it is not an agent command,
			// so the screenshot's pre-check is not delayed behind it).
			let release!: () => void;
			const held = new Promise<void>((resolve) => (release = resolve));
			vi.mocked(runtime.runAction).mockImplementationOnce(async () => {
				await held;
				return {};
			});
			const opening = invoke("browser:openTab", { viewId: nav.viewId });
			// The selection queues first and only takes effect after the screenshot has
			// passed its pre-check (on the agent tab) and queued behind it.
			const selected = invoke("browser:selectTab", { viewId: nav.viewId, tabId: "t1" });
			const shot = host.execute("worker-1", "screenshot", {});
			await new Promise((resolve) => setTimeout(resolve, 0));
			release();
			await opening;
			await selected;
			const result = (await shot) as { target: { tabId: string } };

			expect(result.target.tabId).toBe("t2");
		});

		it("moves the agent onto a profile tab after the grant, so its next open loads logged in", async () => {
			const confirmAgentLogins = vi.fn(async () => true);
			const { host, invoke, constructorOptions } = setupTabHost(named(), false, undefined, undefined, undefined, {
				confirmAgentLogins,
			});
			await invoke("browser:ensure", "worker-1");
			await host.execute("worker-1", "open", { url: "https://example.com/" });
			expect(partitions(constructorOptions)[1]).toMatch(/^persist:ao-browser-temporary-/);

			const opened = (await host.execute("worker-1", "open", { url: "https://app.example/" })) as {
				target: { tabId: string };
			};
			expect(partitions(constructorOptions).at(-1)).toBe(browserProfilePartition(profile.id));
			expect(opened.target.tabId).toBe("t3");
			// Still one agent tab in the profile on later commands.
			await host.execute("worker-1", "snapshot", {});
			expect(constructorOptions).toHaveLength(3);
		});

		it("moves agent commands off the person's own tab onto an agent tab", async () => {
			const { host, invoke, constructorOptions } = setupTabHost(named());
			await invoke("browser:ensure", "worker-1");
			await host.execute("worker-1", "open", { url: "https://example.com/" });

			expect(constructorOptions).toHaveLength(2);
			expect(partitions(constructorOptions)[1]).toMatch(/^persist:ao-browser-temporary-/);
		});

		it("moves an agent open in a temporary session off the person's initial tab onto a proxied one", async () => {
			const { host, invoke, constructorOptions, views } = setupTabHost();
			await invoke("browser:ensure", "worker-1");
			await host.execute("worker-1", "open", { url: "https://example.com/" });

			expect(constructorOptions).toHaveLength(2);
			expect(views[1]!.webContents.getURL()).toBe("https://example.com/");
			expect(views[0]!.webContents.getURL()).not.toBe("https://example.com/");
			expect(views[1]!.webContents.session.setProxy).toHaveBeenCalled();
			expect(views[0]!.webContents.session.setProxy).not.toHaveBeenCalled();
		});

		it("does not wait for the dialog: the agent command resolves in the throwaway partition, then a grant applies", async () => {
			let answer!: (granted: boolean) => void;
			const confirmAgentLogins = vi.fn(() => new Promise<boolean>((resolve) => (answer = resolve)));
			const { host, invoke, constructorOptions } = setupTabHost(
				named(),
				false,
				undefined,
				undefined,
				undefined,
				{ confirmAgentLogins },
			);
			await invoke("browser:ensure", "worker-1");
			await host.execute("worker-1", "tab-new", { url: "https://example.com/" });
			await host.execute("worker-1", "open", { url: "https://example.org/" });

			expect(confirmAgentLogins).toHaveBeenCalledTimes(1);
			expect(partitions(constructorOptions)[1]).toMatch(/^persist:ao-browser-temporary-/);
			answer(true);
			await vi.waitFor(async () => {
				await host.execute("worker-1", "tab-new", { url: "https://example.net/" });
				expect(partitions(constructorOptions).at(-1)).toBe(browserProfilePartition(profile.id));
			});
		});

		it("uses the named profile for agent tabs once the person grants it, asking once", async () => {
			const confirmAgentLogins = vi.fn(async () => true);
			const { host, invoke, constructorOptions } = setupTabHost(
				named(),
				false,
				undefined,
				undefined,
				undefined,
				{ confirmAgentLogins },
			);
			await invoke("browser:ensure", "worker-1");
			await host.execute("worker-1", "tab-new", { url: "https://example.com/" });
			await host.execute("worker-1", "tab-new", { url: "https://example.org/" });

			await vi.waitFor(async () => {
				await host.execute("worker-1", "tab-new", { url: "https://example.com/c" });
				expect(partitions(constructorOptions).at(-1)).toBe(browserProfilePartition(profile.id));
			});
			expect(confirmAgentLogins).toHaveBeenCalledTimes(1);
			expect(confirmAgentLogins).toHaveBeenCalledWith("worker-1", "Work");
		});

		it("proxies the agent's tabs in a temporary session too, leaving the person's tabs alone", async () => {
			const { host, invoke, constructorOptions, views } = setupTabHost();
			await invoke("browser:ensure", "worker-1");
			await host.execute("worker-1", "tab-new", { url: "https://example.com/" });

			const [human, agent] = partitions(constructorOptions);
			expect(agent).not.toBe(human);
			expect(views[1]!.webContents.session.setProxy).toHaveBeenCalled();
			expect(views[1]!.webContents.setWebRTCIPHandlingPolicy).toHaveBeenCalledWith("disable_non_proxied_udp");
			expect(views[0]!.webContents.session.setProxy).not.toHaveBeenCalled();
			expect(views[0]!.webContents.setWebRTCIPHandlingPolicy).not.toHaveBeenCalled();
		});
	});

	it("does not clear named profile partitions when their browser session is destroyed", async () => {
		const clearBrowserProfileData = vi.fn(async (_partition: string) => undefined);
		const { host, invoke } = setupTabHost(
			fakeBrowserProfileStore(profile, { "worker-1": profile.id }),
			false,
			undefined,
			undefined,
			clearBrowserProfileData,
		);
		const nav = (await invoke("browser:ensure", "worker-1")) as BrowserNavState;

		host.destroy(nav.viewId);

		expect(clearBrowserProfileData).not.toHaveBeenCalled();
	});

	it("falls back to the global default profile for a session with no binding of its own", async () => {
		const store = fakeBrowserProfileStore(profile, {}, profile.id);
		const { host, invoke } = setupTabHost(store);
		const ensured = (await invoke("browser:ensure", "worker-1")) as BrowserNavState;

		expect(host.getProfileState(ensured.viewId)).toMatchObject({
			profileId: profile.id,
			profileName: "Work",
			temporary: false,
		});
	});

	it("keeps a session explicitly switched to Temporary out of the default profile, even across reconstruction", async () => {
		const bindings: Record<string, string | null> = {};
		const store = fakeBrowserProfileStore(profile, bindings, profile.id);
		const firstHost = setupTabHost(store);
		const ensured = (await firstHost.invoke("browser:ensure", "worker-1")) as BrowserNavState;
		expect(firstHost.host.getProfileState(ensured.viewId)).toMatchObject({ profileId: profile.id, temporary: false });

		await firstHost.host.switchProfile(ensured.viewId, null);
		expect(bindings["worker-1"]).toBeNull();
		firstHost.host.destroyAll();

		const reconstructed = setupTabHost(store);
		const reensured = (await reconstructed.invoke("browser:ensure", "worker-1")) as BrowserNavState;
		expect(reconstructed.host.getProfileState(reensured.viewId)).toMatchObject({ profileId: null, temporary: true });
	});

	it("detaches every idle browser to its own temporary profile while preserving tabs", async () => {
		const bindings = { "worker-1": profile.id, "worker-2": profile.id };
		const { host, invoke, constructorOptions } = setupTabHost(fakeBrowserProfileStore(profile, bindings));
		const first = (await invoke("browser:ensure", "worker-1")) as BrowserNavState;
		const second = (await invoke("browser:ensure", "worker-2")) as BrowserNavState;
		await invoke("browser:navigate", { viewId: first.viewId, url: "https://example.com/one" });
		expect(host.getProfileUsage(profile.id)).toEqual([
			{ viewId: first.viewId, sessionId: "worker-1", blocker: null },
			{ viewId: second.viewId, sessionId: "worker-2", blocker: null },
		]);
		await host.detachProfile(profile.id);
		expect(host.isProfileLive(profile.id)).toBe(false);
		expect(host.getProfileUsage(profile.id)).toEqual([]);
		expect(bindings).toEqual({ "worker-1": null, "worker-2": null });
		expect(host.getProfileState(first.viewId)).toMatchObject({ temporary: true });
		expect(host.getProfileState(second.viewId)).toMatchObject({ temporary: true });
		const partitions = constructorOptions.slice(2).map((item) => item.webPreferences.partition);
		expect(new Set(partitions).size).toBe(2);
		expect(partitions.every((partition) => partition?.startsWith("persist:ao-browser-temporary-"))).toBe(true);
		expect(await invoke("browser:getTabs", first.viewId)).toMatchObject({ tabs: [expect.objectContaining({ url: "https://example.com/one" })] });
	});

	it("keeps failed browsers on the named profile and waits for successful switches", async () => {
		const { host, invoke, runtime } = setupTabHost(fakeBrowserProfileStore(profile, { "worker-1": profile.id, "worker-2": profile.id }));
		const first = (await invoke("browser:ensure", "worker-1")) as BrowserNavState;
		const second = (await invoke("browser:ensure", "worker-2")) as BrowserNavState;
		vi.mocked(runtime.closeSession).mockRejectedValueOnce(new Error("Runtime unavailable"));
		await expect(host.detachProfile(profile.id)).rejects.toMatchObject({ code: "BROWSER_PROFILE_SWITCH_FAILED", message: expect.stringContaining("profile was kept") });
		expect(host.getProfileState(first.viewId)?.profileId).toBe(profile.id);
		expect(host.getProfileState(second.viewId)?.temporary).toBe(true);
		expect(host.getProfileUsage(profile.id)).toEqual([{ viewId: first.viewId, sessionId: "worker-1", blocker: null }]);
	});

	it("blocks all detachment before switching any idle browser when an agent is active", async () => {
		const { host, invoke, runtime } = setupTabHost(fakeBrowserProfileStore(profile, { "worker-1": profile.id, "worker-2": profile.id }));
		const first = (await invoke("browser:ensure", "worker-1")) as BrowserNavState;
		await invoke("browser:ensure", "worker-2");
		let release!: () => void;
		const pending = new Promise<void>((resolve) => { release = resolve; });
		vi.mocked(runtime.runAction).mockImplementationOnce(async () => { await pending; return {}; });
		const command = host.execute("worker-2", "open", { url: "http://localhost:3000/" });
		await new Promise<void>((resolve) => setImmediate(resolve));
		try {
			expect(host.getProfileUsage(profile.id)).toContainEqual(expect.objectContaining({ sessionId: "worker-2", blocker: "agent-active" }));
			await expect(host.detachProfile(profile.id)).rejects.toMatchObject({ code: "BROWSER_PROFILE_ACTIVE", message: expect.stringContaining("worker-2") });
			expect(host.getProfileState(first.viewId)?.profileId).toBe(profile.id);
		} finally { release(); await command; }
		await host.detachProfile(profile.id);
		expect(host.isProfileLive(profile.id)).toBe(false);
	});

	it("uses a stable named partition and restores the durable binding on host reconstruction", async () => {
		const bindings = { "worker-1": profile.id, "worker-2": profile.id };
		const store = fakeBrowserProfileStore(profile, bindings);
		const firstHost = setupTabHost(store);
		const first = (await firstHost.invoke("browser:ensure", "worker-1")) as BrowserNavState;
		await firstHost.invoke("browser:ensure", "worker-2");

		expect(firstHost.constructorOptions[0]!.webPreferences.partition).toBe(browserProfilePartition(profile.id));
		expect(firstHost.constructorOptions[1]!.webPreferences.partition).toBe(browserProfilePartition(profile.id));
		expect(firstHost.host.getProfileState(first.viewId)).toMatchObject({
			profileId: profile.id,
			profileName: "Work",
			temporary: false,
		});
		firstHost.host.destroyAll();

		const reconstructed = setupTabHost(store);
		await reconstructed.invoke("browser:ensure", "worker-1");
		expect(reconstructed.constructorOptions[0]!.webPreferences.partition).toBe(browserProfilePartition(profile.id));
	});

	it("routes shared-profile request failures only to their owning worker and retains the watcher", async () => {
		const store = fakeBrowserProfileStore(profile, { "worker-1": profile.id, "worker-2": profile.id });
		const { host, invoke, views } = setupTabHost(store);
		const first = (await invoke("browser:ensure", "worker-1")) as BrowserNavState;
		const second = (await invoke("browser:ensure", "worker-2")) as BrowserNavState;
		const webRequest = views[0]!.webContents.session.webRequest;

		expect(views[1]!.webContents.session).toBe(views[0]!.webContents.session);
		expect(webRequest.onCompleted).toHaveBeenCalledTimes(1);
		const onCompleted = webRequest.onCompleted.mock.calls[0]?.[1] as
			| ((details: {
					webContentsId: number;
					resourceType: string;
					method: string;
					url: string;
					statusCode: number;
			  }) => void)
			| undefined;
		onCompleted?.({
			webContentsId: views[0]!.webContents.id,
			resourceType: "xhr",
			method: "GET",
			url: "https://first.example.test/api",
			statusCode: 500,
		});

		const firstErrors = (await host.execute("worker-1", "errors")) as { messages: unknown[] };
		const secondErrors = (await host.execute("worker-2", "errors")) as { messages: unknown[] };
		expect(firstErrors.messages).toHaveLength(1);
		expect(secondErrors.messages).toHaveLength(0);

		host.destroy(first.viewId);
		expect(webRequest.onCompleted).not.toHaveBeenCalledWith(null);
		onCompleted?.({
			webContentsId: views[1]!.webContents.id,
			resourceType: "xhr",
			method: "GET",
			url: "https://second.example.test/api",
			statusCode: 503,
		});
		const survivingErrors = (await host.execute("worker-2", "errors")) as { messages: unknown[] };
		expect(survivingErrors.messages).toHaveLength(1);

		host.destroy(second.viewId);
		expect(webRequest.onCompleted).toHaveBeenLastCalledWith(null);
		expect(webRequest.onErrorOccurred).toHaveBeenLastCalledWith(null);
	});

	it("records and suggests history only for an explicitly selected named profile", async () => {
		const history = {
			record: vi.fn(async () => undefined),
			suggest: vi.fn(async () => [{ url: "https://github.com/openai", title: "OpenAI" }]),
		} as unknown as BrowserHistoryStore;
		const named = setupTabHost(fakeBrowserProfileStore(profile, { "worker-1": profile.id }), false, undefined, history);
		const namedNav = (await named.invoke("browser:ensure", "worker-1")) as BrowserNavState;
		await named.invoke("browser:navigate", { viewId: namedNav.viewId, url: "https://github.com/openai" });
		(named.views[0]!.listeners.get("did-navigate") as ((event: unknown, url: string) => void))(
			{},
			"https://github.com/openai",
		);
		expect(history.record).toHaveBeenCalledWith(
			profile.id,
			"https://github.com/openai",
			"Title https://github.com/openai",
			true,
		);
		await expect(named.invoke("browser:history:suggest", { viewId: namedNav.viewId, query: "openai" })).resolves.toEqual([
			{ url: "https://github.com/openai", title: "OpenAI" },
		]);
		await expect(named.invoke("browser:history:favicon", { viewId: namedNav.viewId, url: "https://github.com/openai" })).resolves.toBeUndefined();
		expect(named.views[0]!.webContents.session.fetch).toHaveBeenCalledWith(
			"https://github.com/favicon.ico",
			{ signal: expect.objectContaining({ aborted: false }) },
		);

		const temporary = setupTabHost(undefined, false, undefined, history);
		const temporaryNav = (await temporary.invoke("browser:ensure", "worker-2")) as BrowserNavState;
		await temporary.invoke("browser:navigate", { viewId: temporaryNav.viewId, url: "https://example.com" });
		(temporary.views[0]!.listeners.get("did-navigate") as ((event: unknown, url: string) => void))(
			{},
			"https://example.com",
		);
		expect(await temporary.invoke("browser:history:suggest", { viewId: temporaryNav.viewId, query: "example" })).toEqual([]);
		expect(history.record).toHaveBeenCalledTimes(1);
	});

	it("bounds and times out history favicon downloads", async () => {
		const named = setupTabHost(fakeBrowserProfileStore(profile, { "worker-1": profile.id }));
		const namedNav = (await named.invoke("browser:ensure", "worker-1")) as BrowserNavState;
		const fetch = named.views[0]!.webContents.session.fetch;
		const ico = new Uint8Array([0, 0, 1, 0, 1, 0]);
		let icoDelivered = false;
		fetch.mockResolvedValueOnce({
			ok: true,
			headers: new Headers({ "content-length": String(ico.byteLength), "content-type": "image/x-icon" }),
			body: {
				getReader: () => ({
					read: vi.fn(async () => {
						if (icoDelivered) return { done: true, value: undefined };
						icoDelivered = true;
						return { done: false, value: ico };
					}),
					cancel: vi.fn(async () => undefined),
					releaseLock: vi.fn(),
				}),
			},
		} as unknown as Response);
		await expect(named.invoke("browser:history:favicon", {
			viewId: namedNav.viewId,
			url: "https://uses-ico.example/icon",
		})).resolves.toBe("data:image/x-icon;base64,AAABAAEA");

		const responseCancel = vi.fn(async () => undefined);
		fetch.mockResolvedValueOnce({
			ok: true,
			headers: new Headers({ "content-length": String(256 * 1024 + 1) }),
			body: { cancel: responseCancel },
		} as unknown as Response);
		await expect(named.invoke("browser:history:favicon", {
			viewId: namedNav.viewId,
			url: "https://declared-too-large.example/icon",
		})).resolves.toBeUndefined();
		expect(responseCancel).toHaveBeenCalledOnce();

		const readerCancel = vi.fn(async () => undefined);
		const releaseLock = vi.fn();
		fetch.mockResolvedValueOnce({
			ok: true,
			headers: new Headers(),
			body: {
				getReader: () => ({
					read: vi.fn(async () => ({ done: false, value: new Uint8Array(256 * 1024 + 1) })),
					cancel: readerCancel,
					releaseLock,
				}),
			},
		} as unknown as Response);
		await expect(named.invoke("browser:history:favicon", {
			viewId: namedNav.viewId,
			url: "https://stream-too-large.example/icon",
		})).resolves.toBeUndefined();
		expect(readerCancel).toHaveBeenCalledOnce();
		expect(releaseLock).toHaveBeenCalledOnce();

		vi.useFakeTimers();
		try {
			let requestSignal: AbortSignal | undefined;
			fetch.mockImplementationOnce((_url: string, init: RequestInit) => new Promise((_resolve, reject) => {
				requestSignal = init.signal as AbortSignal;
				requestSignal.addEventListener("abort", () => reject(new DOMException("Aborted", "AbortError")), { once: true });
			}));
			const favicon = named.invoke("browser:history:favicon", {
				viewId: namedNav.viewId,
				url: "https://never-responds.example/icon",
			});
			await vi.advanceTimersByTimeAsync(5_000);
			await expect(favicon).resolves.toBeUndefined();
			expect(requestSignal?.aborted).toBe(true);
		} finally {
			vi.useRealTimers();
		}
	});

	it("cleans the old runtime before rebuilding tabs and preserves hardening on new views", async () => {
		const bindings = { "worker-1": profile.id };
		const store = fakeBrowserProfileStore(profile, bindings);
		const { constructorOptions, debuggerCommands, emit, host, invoke, runtime, sent, views } = setupTabHost(store);
		const nav = (await invoke("browser:ensure", "worker-1")) as BrowserNavState;
		await invoke("browser:navigate", { viewId: nav.viewId, url: "http://localhost:3000/" });
		// The agent command moves to its own agent tab; then the person opens another.
		await host.execute("worker-1", "network-start", { durationSeconds: 30 });
		await invoke("browser:openTab", { viewId: nav.viewId, url: "https://example.com/" });
		emit("browser:setBounds", {
			viewId: nav.viewId,
			revision: 1,
			rect: { x: 24, y: 32, width: 640, height: 420 },
			visible: true,
		});
		await invoke("browser:devtools", { viewId: nav.viewId, operation: "open" });
		await invoke("browser:annotation:setMode", { viewId: nav.viewId, enabled: true });

		const before = constructorOptions.length;
		const switched = await host.switchProfile(nav.viewId, null);
		expect(switched).toMatchObject({ profileId: null, temporary: true });
		// An explicit switch to Temporary is a durable choice, not the absence of one.
		expect(bindings["worker-1"]).toBeNull();
		expect(runtime.closeSession).toHaveBeenCalledWith("worker-1");
		expect(views.some((view) => view.webContents.closeDevTools.mock.calls.length > 0)).toBe(true);
		expect(views[0]!.webContents.close).toHaveBeenCalled();
		expect(views[1]!.webContents.close).toHaveBeenCalled();
		expect(views[2]!.webContents.close).toHaveBeenCalled();
		expect(debuggerCommands.some(({ method }) => method === "Network.enable")).toBe(true);
		expect(sent).toContainEqual({
			channel: "browser:annotation:canceled",
			payload: { viewId: nav.viewId, reason: "navigation" },
		});
		expect(constructorOptions.slice(before).every(({ webPreferences }) => webPreferences.partition?.startsWith("persist:ao-browser-temporary-") === true)).toBe(true);
		// The person's two tabs share the profile; the agent's tab (between them) has its own.
		expect(constructorOptions[before]!.webPreferences.partition).toBe(constructorOptions[before + 2]!.webPreferences.partition);
		expect(constructorOptions[before + 1]!.webPreferences.partition).not.toBe(constructorOptions[before]!.webPreferences.partition);
		for (const view of views.slice(before)) {
			expect(view.webContents.session.setPermissionCheckHandler).toHaveBeenCalledWith(expect.any(Function));
			expect(view.webContents.session.setPermissionRequestHandler).toHaveBeenCalledWith(expect.any(Function));
		}
		expect(
			views.slice(before).some((view) =>
				view.setBounds.mock.calls.some(
					([bounds]) => JSON.stringify(bounds) === JSON.stringify({ x: 24, y: 32, width: 640, height: 420 }),
				),
			),
		).toBe(true);

		// The Temporary session's agent works in its own tab (t2), not the person's active t3.
		const activeURL = await host.execute("worker-1", "get", { property: "url" });
		expect(activeURL).toMatchObject({ value: "about:blank" });
		expect(runtime.runAction).toHaveBeenCalledWith(
			"worker-1",
			"tab-select",
			{ tabId: "t2" },
			expect.anything(),
			undefined,
		);

		const sentBeforeStaleEvent = sent.length;
		const oldActiveDidNavigate = views[2]!.listeners.get("did-navigate") as
			| ((event: unknown, url: string) => void)
			| undefined;
		oldActiveDidNavigate?.({}, "https://stale.example/");
		expect(
			sent.slice(sentBeforeStaleEvent).filter(({ channel }) =>
				channel === "browser:navState" || channel === "browser:tabsState",
			),
		).toEqual([]);
	});

	it("defers a bound worker until an in-flight profile data operation finishes", async () => {
		const store = fakeBrowserProfileStore(profile, { "worker-1": profile.id });
		let operationInProgress = true;
		let release!: () => void;
		const held = new Promise<void>((resolve) => {
			release = resolve;
		});
		store.isProfileOperationInProgress = vi.fn(() => operationInProgress);
		store.waitForProfileOperation = vi.fn(async () => {
			await held;
			operationInProgress = false;
		});
		const { constructorOptions, invoke } = setupTabHost(store);

		const ensuring = invoke("browser:ensure", "worker-1");
		await new Promise<void>((resolve) => setImmediate(resolve));
		expect(constructorOptions).toHaveLength(0);
		release();
		await ensuring;
		expect(constructorOptions[0]!.webPreferences.partition).toBe(browserProfilePartition(profile.id));
	});

	it("stops an in-flight page load to switch profiles and reloads it in the new profile", async () => {
		const store = fakeBrowserProfileStore(profile, { "worker-1": profile.id });
		const { host, invoke, views } = setupTabHost(store);
		const nav = (await invoke("browser:ensure", "worker-1")) as BrowserNavState;
		let abort!: () => void;
		views[0]!.webContents.loadURL.mockImplementationOnce(
			() =>
				new Promise<void>((_resolve, reject) => {
					abort = () => reject(Object.assign(new Error("ERR_ABORTED (-3)"), { errorCode: -3 }));
				}),
		);
		const stop = vi.fn(() => abort());
		(views[0]!.webContents as unknown as { stop: () => void }).stop = stop;

		const navigation = invoke("browser:navigate", { viewId: nav.viewId, url: "https://example.com/" });
		await new Promise<void>((resolve) => setImmediate(resolve));
		expect(host.getProfileSwitchInfo(nav.viewId)).toMatchObject({ agentActive: false });

		await expect(host.switchProfile(nav.viewId, null)).resolves.toMatchObject({ profileId: null, temporary: true });
		await navigation;
		expect(stop).toHaveBeenCalled();
		expect(views[1]!.webContents.loadURL).toHaveBeenCalledWith("https://example.com/");
	});

	it("keeps the newest pending navigation when an older load of the same URL aborts", async () => {
		const store = fakeBrowserProfileStore(profile, { "worker-1": profile.id });
		const { host, invoke, views } = setupTabHost(store);
		const nav = (await invoke("browser:ensure", "worker-1")) as BrowserNavState;
		await invoke("browser:navigate", { viewId: nav.viewId, url: "https://first.example/" });
		const aborted = () => Object.assign(new Error("ERR_ABORTED (-3)"), { errorCode: -3 });
		let abortOlder!: () => void;
		let abortNewer!: () => void;
		views[0]!.webContents.loadURL
			.mockImplementationOnce(() => new Promise<void>((_resolve, reject) => {
				abortOlder = () => reject(aborted());
			}))
			.mockImplementationOnce(() => new Promise<void>((_resolve, reject) => {
				abortNewer = () => reject(aborted());
			}));
		(views[0]!.webContents as unknown as { stop: () => void }).stop = () => abortNewer();

		const older = invoke("browser:navigate", { viewId: nav.viewId, url: "https://second.example/" });
		await new Promise<void>((resolve) => setImmediate(resolve));
		const newer = invoke("browser:navigate", { viewId: nav.viewId, url: "https://second.example/" });
		await new Promise<void>((resolve) => setImmediate(resolve));
		abortOlder();
		await older;

		await expect(host.switchProfile(nav.viewId, null)).resolves.toMatchObject({ temporary: true });
		await newer;
		expect(views[1]!.webContents.loadURL).toHaveBeenCalledWith("https://second.example/");
	});

	it("starts an unbound worker in Temporary when no default profile is set, even with saved profiles", async () => {
		const bindings: Record<string, string | null> = {};
		const store = fakeBrowserProfileStore(profile, bindings);
		const { host, invoke } = setupTabHost(store);
		const nav = (await invoke("browser:ensure", "worker-1")) as BrowserNavState;
		expect(host.getProfileState(nav.viewId)).toMatchObject({ profileId: null, temporary: true });
	});

	it("bounds tab readiness so a stuck initial load cannot wedge the session queue", async () => {
		const store = fakeBrowserProfileStore(profile, { "worker-1": profile.id });
		let holdLoads = true;
		const { host, invoke } = setupTabHost(
			store,
			false,
			async () => {
				if (holdLoads) await new Promise(() => {});
			},
			undefined,
			undefined,
			{ entryReadyTimeoutMs: 20 },
		);
		const nav = (await invoke("browser:ensure", "worker-1")) as BrowserNavState;

		// A native command awaiting tab readiness must fail typed instead of
		// hanging forever — and keep failing fast, never wedging the queue.
		await expect(host.execute("worker-1", "snapshot", {})).rejects.toMatchObject({
			code: "BROWSER_DEVTOOLS_UNAVAILABLE",
		});
		await expect(host.execute("worker-1", "snapshot", {})).rejects.toMatchObject({
			code: "BROWSER_DEVTOOLS_UNAVAILABLE",
		});

		// Recovery path: once loads settle again, a fresh tab serves commands.
		holdLoads = false;
		host.destroy(nav.viewId);
		await invoke("browser:ensure", "worker-1");
		await expect(host.execute("worker-1", "snapshot", {})).resolves.toBeTruthy();
	});

	it("restores readiness when a timed-out initial load later succeeds", async () => {
		const store = fakeBrowserProfileStore(profile, { "worker-1": null });
		let release: () => void = () => {};
		const gate = new Promise<void>((r) => {
			release = r;
		});
		let gatedFirstLoad = true;
		const { host } = setupTabHost(
			store,
			false,
			async (_viewIndex, url) => {
				if (gatedFirstLoad && url === "about:blank") {
					gatedFirstLoad = false;
					await gate;
				}
			},
			undefined,
			undefined,
			{ entryReadyTimeoutMs: 20 },
		);
		// The agent's command starts the session, so the stuck load is its own tab's.

		// The initial about:blank load is stuck past the readiness deadline, so a
		// command fails typed instead of hanging.
		await expect(host.execute("worker-1", "snapshot", {})).rejects.toMatchObject({
			code: "BROWSER_DEVTOOLS_UNAVAILABLE",
		});

		// The same load then completes: readiness must be restored so the SAME tab
		// serves commands again, without recreating it.
		release();
		await vi.waitFor(async () => {
			await expect(
				host.execute("worker-1", "snapshot", {}),
			).resolves.toBeTruthy();
		});
	});

	it("releases profile usage when worker tab startup fails", async () => {
		const store = fakeBrowserProfileStore(profile, { "worker-1": profile.id });
		const failing = setupTabHost(store, true);

		await expect(failing.invoke("browser:ensure", "worker-1")).rejects.toThrow(
			"browser view startup failed",
		);
		expect(failing.host.isProfileLive(profile.id)).toBe(false);
		expect(failing.host.getProfileState("1:worker-1")).toBeNull();
	});

	it("releases live usage through destroy, destroyAll, and dispose", async () => {
		const store = fakeBrowserProfileStore(profile, { "worker-1": profile.id });
		const first = setupTabHost(store);
		const nav = (await first.invoke("browser:ensure", "worker-1")) as BrowserNavState;
		expect(first.host.isProfileLive(profile.id)).toBe(true);
		first.host.destroy(nav.viewId);
		expect(first.host.isProfileLive(profile.id)).toBe(false);

		const second = setupTabHost(store);
		await second.invoke("browser:ensure", "worker-1");
		second.host.destroyAll();
		expect(second.host.isProfileLive(profile.id)).toBe(false);

		const third = setupTabHost(store);
		await third.invoke("browser:ensure", "worker-1");
		await third.host.dispose();
		expect(third.host.isProfileLive(profile.id)).toBe(false);
	});

	it("clears an old temporary partition only after a switch to a profile succeeds", async () => {
		const bindings: Record<string, string> = {};
		const store = fakeBrowserProfileStore(profile, bindings);
		const clearBrowserProfileData = vi.fn(async (_partition: string) => undefined);
		const { constructorOptions, host, invoke } = setupTabHost(
			store,
			false,
			undefined,
			undefined,
			clearBrowserProfileData,
		);
		const nav = (await invoke("browser:ensure", "worker-1")) as BrowserNavState;
		await host.switchProfile(nav.viewId, null);
		const temporaryPartition = constructorOptions.at(-1)!.webPreferences.partition!;

		const switched = await host.switchProfile(nav.viewId, profile.id);

		expect(switched).toMatchObject({ profileId: profile.id, temporary: false });
		expect(bindings["worker-1"]).toBe(profile.id);
		expect(clearBrowserProfileData).toHaveBeenCalledWith(temporaryPartition);
		expect(constructorOptions.at(-1)!.webPreferences.partition).toBe(browserProfilePartition(profile.id));
	});

	it("does not clear a temporary partition when a failed profile switch rolls back to it", async () => {
		const bindings: Record<string, string | null> = {};
		const store = fakeBrowserProfileStore(profile, bindings);
		const clearBrowserProfileData = vi.fn(async (_partition: string) => undefined);
		let failReplacementStartup = false;
		const { constructorOptions, host, invoke } = setupTabHost(
			store,
			false,
			async (viewIndex, url) => {
				if (viewIndex > 0 && url === "about:blank" && failReplacementStartup) {
					failReplacementStartup = false;
					throw new Error("replacement startup failed");
				}
			},
			undefined,
			clearBrowserProfileData,
		);
		const nav = (await invoke("browser:ensure", "worker-1")) as BrowserNavState;
		await host.switchProfile(nav.viewId, null);
		const temporaryPartition = constructorOptions.at(-1)!.webPreferences.partition!;
		await invoke("browser:navigate", { viewId: nav.viewId, url: "https://example.com/" });
		failReplacementStartup = true;

		await expect(host.switchProfile(nav.viewId, profile.id)).rejects.toThrow("replacement startup failed");

		// The explicit Temporary choice made before the failed switch is still recorded.
		expect(bindings["worker-1"]).toBeNull();
		expect(host.getProfileState(nav.viewId)).toMatchObject({ profileId: null, temporary: true });
		expect(constructorOptions.at(-1)!.webPreferences.partition).toBe(temporaryPartition);
		expect(clearBrowserProfileData).not.toHaveBeenCalled();
	});

	// Regression, reproduced live: after a profile pick the dropdown returns
	// focus to "Browser controls", whose tooltip raises the overlay while the
	// replacement tabs are still reloading. The macOS surface refresh then hit
	// the empty activeTabId and crashed the main process ("A JavaScript error
	// occurred in the main process: Active browser tab is unavailable").
	it("keeps an active tab for overlay refreshes and renderer calls while a profile switch reloads tabs", async () => {
		const store = fakeBrowserProfileStore(profile, { "worker-1": profile.id });
		let releaseReload!: () => void;
		let replacementReloadStarted!: () => void;
		const reloadHeld = new Promise<void>((resolve) => {
			releaseReload = resolve;
		});
		const replacementStarted = new Promise<void>((resolve) => {
			replacementReloadStarted = resolve;
		});
		const fixture = setupTabHost(store, false, async (viewIndex, url) => {
			if (viewIndex > 0 && url === "https://example.com/") {
				replacementReloadStarted();
				await reloadHeld;
			}
		});
		const nav = (await fixture.invoke("browser:ensure", "worker-1")) as BrowserNavState;
		fixture.emit("browser:setBounds", {
			viewId: nav.viewId,
			revision: 1,
			rect: { x: 24, y: 32, width: 640, height: 420 },
			visible: true,
		});
		await fixture.invoke("browser:navigate", { viewId: nav.viewId, url: "https://example.com/" });

		const switching = fixture.host.switchProfile(nav.viewId, null);
		await replacementStarted;
		expect(() => fixture.host.refreshLastFocusedPanelSurface()).not.toThrow();
		await expect(fixture.invoke("browser:ensure", "worker-1")).resolves.toMatchObject({ viewId: nav.viewId });
		await new Promise((resolve) => setTimeout(resolve, 0));
		releaseReload();

		await expect(switching).resolves.toMatchObject({ profileId: null, temporary: true });
		expect(() => fixture.host.refreshLastFocusedPanelSurface()).not.toThrow();
	});

	it("does not recreate tabs after a worker is destroyed during profile replacement", async () => {
		const bindings = { "worker-1": profile.id };
		const store = fakeBrowserProfileStore(profile, bindings);
		let releaseReload!: () => void;
		let replacementReloadStarted!: () => void;
		const reloadHeld = new Promise<void>((resolve) => {
			releaseReload = resolve;
		});
		const replacementStarted = new Promise<void>((resolve) => {
			replacementReloadStarted = resolve;
		});
		const fixture = setupTabHost(store, false, async (viewIndex, url) => {
			if (viewIndex > 0 && url === "https://example.com/") {
				replacementReloadStarted();
				await reloadHeld;
			}
		});
		const nav = (await fixture.invoke("browser:ensure", "worker-1")) as BrowserNavState;
		await fixture.invoke("browser:navigate", { viewId: nav.viewId, url: "https://example.com/" });

		const switching = fixture.host.switchProfile(nav.viewId, null);
		await replacementStarted;
		expect(fixture.views).toHaveLength(2);
		fixture.host.destroy(nav.viewId);
		releaseReload();

		await expect(switching).rejects.toMatchObject({ code: "BROWSER_TARGET_UNAVAILABLE" });
		expect(fixture.views).toHaveLength(2);
		expect(fixture.views[1]!.webContents.close).toHaveBeenCalled();
		expect(bindings["worker-1"]).toBe(profile.id);
	});

	it("lets agent tab-new and tab-close admitted before a switch finish during the wait", async () => {
		const store = fakeBrowserProfileStore(profile, { "worker-1": profile.id });
		const { host, invoke, runtime } = setupTabHost(store);
		const nav = (await invoke("browser:ensure", "worker-1")) as BrowserNavState;
		const runAction = vi.mocked(runtime.runAction);
		const original = runAction.getMockImplementation()!;
		const holdOnce = (action: string) => {
			let release!: () => void;
			const held = new Promise<void>((resolve) => {
				release = resolve;
			});
			let holding = true;
			runAction.mockImplementation(async (...args: Parameters<typeof original>) => {
				if (holding && args[1] === action) {
					holding = false;
					await held;
				}
				return original(...args);
			});
			return () => release();
		};

		const releaseNew = holdOnce("tab-new");
		const opening = host.execute("worker-1", "tab-new");
		await new Promise<void>((resolve) => setImmediate(resolve));
		const firstSwitch = host.switchProfile(nav.viewId, null);
		await new Promise<void>((resolve) => setImmediate(resolve));
		releaseNew();
		await expect(opening).resolves.toBeDefined();
		await expect(firstSwitch).resolves.toMatchObject({ temporary: true });
		expect(await invoke("browser:getTabs", nav.viewId)).toMatchObject({
			tabs: [expect.anything(), expect.anything()],
		});

		const releaseClose = holdOnce("tab-close");
		const closing = host.execute("worker-1", "tab-close", { tabId: "t2" });
		await new Promise<void>((resolve) => setImmediate(resolve));
		const secondSwitch = host.switchProfile(nav.viewId, profile.id);
		await new Promise<void>((resolve) => setImmediate(resolve));
		releaseClose();
		await expect(closing).resolves.toBeDefined();
		await expect(secondSwitch).resolves.toMatchObject({ profileId: profile.id });
		expect(await invoke("browser:getTabs", nav.viewId)).toMatchObject({ tabs: [expect.anything()] });
	});

	it("gives up on time when an agent-browser command outlasts the switch deadline", async () => {
		const store = fakeBrowserProfileStore(profile, { "worker-1": profile.id });
		const { host, invoke, runtime } = setupTabHost(store);
		const nav = (await invoke("browser:ensure", "worker-1")) as BrowserNavState;
		let release!: () => void;
		const pending = new Promise<void>((resolve) => {
			release = resolve;
		});
		vi.mocked(runtime.runAction).mockImplementationOnce(async () => {
			await pending;
			return {};
		});
		vi.useFakeTimers();
		try {
			const command = host.execute("worker-1", "open", { url: "http://localhost:3000/" });
			await vi.advanceTimersByTimeAsync(0);
			const switching = host.switchProfile(nav.viewId, null);
			const outcome = switching.then(
				() => "switched",
				(error: { code?: string }) => error.code,
			);
			await vi.advanceTimersByTimeAsync(10_000);
			await expect(outcome).resolves.toBe("BROWSER_PROFILE_ACTIVE");
			// profileSwitching is cleared, so the still-running command alone
			// keeps the switch blocked and nothing else is refused as "switching".
			expect(host.getProfileSwitchInfo(nav.viewId)).toMatchObject({ agentActive: true });
			release();
			await command;
			expect(host.getProfileSwitchInfo(nav.viewId)).toMatchObject({ agentActive: false });
			const retry = host.switchProfile(nav.viewId, null);
			await vi.advanceTimersByTimeAsync(0);
			await expect(retry).resolves.toMatchObject({ profileId: null, temporary: true });
		} finally {
			vi.useRealTimers();
		}
	});

	it("waits for an in-flight agent-browser command, refuses new ones, then switches", async () => {
		const store = fakeBrowserProfileStore(profile, { "worker-1": profile.id });
		const { host, invoke, runtime } = setupTabHost(store);
		const nav = (await invoke("browser:ensure", "worker-1")) as BrowserNavState;
		let release!: () => void;
		const pending = new Promise<void>((resolve) => {
			release = resolve;
		});
		vi.mocked(runtime.runAction).mockImplementationOnce(async () => {
			await pending;
			return {};
		});

		const command = host.execute("worker-1", "open", { url: "http://localhost:3000/" });
		await new Promise<void>((resolve) => setImmediate(resolve));
		expect(host.getProfileSwitchInfo(nav.viewId)).toMatchObject({ agentActive: true });
		const switching = host.switchProfile(nav.viewId, null);
		await new Promise<void>((resolve) => setImmediate(resolve));
		await expect(host.execute("worker-1", "get", { property: "url" })).rejects.toMatchObject({
			code: "BROWSER_PROFILE_SWITCHING",
		});
		release();
		await command;
		await expect(switching).resolves.toMatchObject({ profileId: null, temporary: true });
	});
});

