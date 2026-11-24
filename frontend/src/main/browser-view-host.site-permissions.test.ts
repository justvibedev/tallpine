import { mkdtemp, readFile, rm } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { BrowserNavState } from "./browser-view-host";
import { setupHost, setupTabHost } from "./browser-view-host-test-helpers";
import { BrowserSitePermissionStore } from "./browser-site-permission-store";
import { sitePermissionScope } from "./browser-view-host/site-permissions";

vi.mock("electron", async (importOriginal) => {
	const actual = await importOriginal<typeof import("electron")>();
	return { ...actual, net: { fetch: vi.fn() } };
});

const PROFILE = "11111111-1111-4111-8111-111111111111";
const PAGE = "https://meet.example/room";
const ORIGIN = "https://meet.example";
const temporaryDirectories: string[] = [];

afterEach(async () => {
	await Promise.all(temporaryDirectories.splice(0).map((directory) => rm(directory, { recursive: true, force: true })));
});

async function openStore(): Promise<{ store: BrowserSitePermissionStore; stateDir: string }> {
	const stateDir = await mkdtemp(path.join(os.tmpdir(), "tallpine-site-permissions-host-"));
	temporaryDirectories.push(stateDir);
	return { store: await BrowserSitePermissionStore.open(stateDir), stateDir };
}

const navStates = (sent: Array<{ channel: string; payload: unknown }>) =>
	sent.filter((entry) => entry.channel === "browser:navState").map((entry) => entry.payload as BrowserNavState);

type RequestHandler = (contents: unknown, permission: string, callback: (granted: boolean) => void, details: object) => void;
type CheckHandler = (contents: unknown, permission: string, origin: string, details: object) => boolean;

async function personHost(extra: Parameters<typeof setupHost>[1] = {}) {
	const harness = setupHost(undefined, extra);
	await harness.invoke("browser:ensure", "sess-1");
	await harness.webContents.loadURL(PAGE);
	const request = harness.setPermissionRequestHandler.mock.calls.at(-1)![0] as RequestHandler;
	const check = harness.setPermissionCheckHandler.mock.calls.at(-1)![0] as CheckHandler;
	return { ...harness, request, check, viewId: "1:sess-1" };
}

const camera = { isMainFrame: true, requestingUrl: PAGE, mediaTypes: ["video"] };

describe("browser site permissions", () => {
	it("asks a person's tab with an Allow / Block bar and remembers Allow for the origin", async () => {
		const { store } = await openStore();
		const { request, check, invoke, sent, viewId } = await personHost({ browserSitePermissionStore: store });
		const callback = vi.fn();

		request(null, "media", vi.fn(), camera); // no tab: denied, no prompt
		request({ id: 99 }, "media", callback, camera);
		expect(callback).not.toHaveBeenCalled();
		const prompt = navStates(sent).at(-1)?.sitePermissionPrompt;
		expect(prompt).toMatchObject({ origin: ORIGIN, kinds: ["camera"] });

		await invoke("browser:sitePermission:respond", { viewId, promptId: prompt!.id, allow: true });
		expect(callback).toHaveBeenCalledWith(true);
		expect(navStates(sent).at(-1)?.sitePermissionPrompt).toBeUndefined();
		expect(navStates(sent).at(-1)?.sitePermissions).toEqual([{ kind: "camera", decision: "allow" }]);

		// Remembered: answered at once, and visible to permission checks.
		const again = vi.fn();
		request({ id: 99 }, "media", again, camera);
		expect(again).toHaveBeenCalledWith(true);
		expect(check({ id: 99 }, "media", ORIGIN, { isMainFrame: true, requestingUrl: PAGE, mediaType: "video" })).toBe(true);
		// The microphone was never answered.
		expect(check({ id: 99 }, "media", ORIGIN, { isMainFrame: true, requestingUrl: PAGE, mediaType: "audio" })).toBe(false);
	});

	it("remembers Block and denies the next request without asking", async () => {
		const { store } = await openStore();
		const { request, invoke, sent, viewId } = await personHost({ browserSitePermissionStore: store });
		const callback = vi.fn();
		const notifications = { isMainFrame: true, requestingUrl: PAGE };
		request({ id: 99 }, "notifications", callback, notifications);
		const prompt = navStates(sent).at(-1)!.sitePermissionPrompt!;
		await invoke("browser:sitePermission:respond", { viewId, promptId: prompt.id, allow: false });
		expect(callback).toHaveBeenCalledWith(false);

		const before = navStates(sent).length;
		const again = vi.fn();
		request({ id: 99 }, "notifications", again, notifications);
		expect(again).toHaveBeenCalledWith(false);
		expect(navStates(sent).slice(before).some((state) => state.sitePermissionPrompt)).toBe(false);
	});

	it("persists a profile's answers under the state dir and drops them on reset", async () => {
		const { store, stateDir } = await openStore();
		await store.set(PROFILE, ORIGIN, ["microphone"], "allow");
		const file = JSON.parse(await readFile(path.join(stateDir, "browser-site-permissions.json"), "utf8"));
		expect(file.profiles[PROFILE][ORIGIN]).toEqual({ microphone: "allow" });

		// A person's tab in the temporary partition: the Site settings reset
		// clears what the tab remembered.
		const { request, invoke, sent, viewId } = await personHost({ browserSitePermissionStore: store });
		request({ id: 99 }, "clipboard-read", vi.fn(), { isMainFrame: true, requestingUrl: PAGE });
		const prompt = navStates(sent).at(-1)!.sitePermissionPrompt!;
		await invoke("browser:sitePermission:respond", { viewId, promptId: prompt.id, allow: true });
		expect(navStates(sent).at(-1)?.sitePermissions).toEqual([{ kind: "clipboard-read", decision: "allow" }]);

		await invoke("browser:sitePermission:reset", { viewId, kind: "clipboard-read" });
		expect(navStates(sent).at(-1)?.sitePermissions).toBeUndefined();
		const callback = vi.fn();
		request({ id: 99 }, "clipboard-read", callback, { isMainFrame: true, requestingUrl: PAGE });
		expect(callback).not.toHaveBeenCalled(); // asks again
	});

	it("keeps agent tabs deny-all even when the origin is allowed", async () => {
		const { store } = await openStore();
		const harness = setupHost(undefined, { browserSitePermissionStore: store });
		// The agent opens the session, so its first tab is agent-driven.
		await harness.host.execute("sess-1", "tabs");
		await harness.webContents.loadURL(PAGE);
		const request = harness.setPermissionRequestHandler.mock.calls.at(-1)![0] as RequestHandler;
		const check = harness.setPermissionCheckHandler.mock.calls.at(-1)![0] as CheckHandler;

		const callback = vi.fn();
		request({ id: 99 }, "media", callback, camera);
		request({ id: 99 }, "fullscreen", callback, { isMainFrame: true, requestingUrl: PAGE });
		expect(callback.mock.calls).toEqual([[false], [false]]);
		expect(check({ id: 99 }, "fullscreen", ORIGIN, { isMainFrame: true, requestingUrl: PAGE })).toBe(false);
		expect(navStates(harness.sent).some((state) => state.sitePermissionPrompt)).toBe(false);
	});

	it("denies subframes and asks only for known kinds", async () => {
		const { store } = await openStore();
		const { request } = await personHost({ browserSitePermissionStore: store });
		const callback = vi.fn();
		request({ id: 99 }, "media", callback, { ...camera, isMainFrame: false });
		request({ id: 99 }, "geolocation", callback, { isMainFrame: true, requestingUrl: PAGE });
		request({ id: 99 }, "openExternal", callback, { isMainFrame: true, requestingUrl: PAGE, externalURL: "mailto:a@b.c" });
		expect(callback.mock.calls).toEqual([[false], [false], [false]]);
	});

	it("answers a waiting request with no when the page navigates away", async () => {
		const { store } = await openStore();
		const { request, sent, webContentsListeners } = await personHost({ browserSitePermissionStore: store });
		const callback = vi.fn();
		request({ id: 99 }, "media", callback, camera);
		webContentsListeners.get("did-navigate")?.({} as never, "https://other.example/" as never);
		expect(callback).toHaveBeenCalledWith(false);
		expect(navStates(sent).at(-1)?.sitePermissionPrompt).toBeUndefined();
	});

	it("ignores answers from a renderer that does not own the view", async () => {
		const { store } = await openStore();
		const { request, invokeFromTab, sent, viewId } = await personHost({ browserSitePermissionStore: store });
		const callback = vi.fn();
		request({ id: 99 }, "media", callback, camera);
		const prompt = navStates(sent).at(-1)!.sitePermissionPrompt!;
		await invokeFromTab("browser:sitePermission:respond", 77, { viewId, promptId: prompt.id, allow: true });
		expect(callback).not.toHaveBeenCalled();
		expect(navStates(sent).at(-1)?.sitePermissionPrompt?.id).toBe(prompt.id);
	});

	it("lets a person's page go fullscreen inside its view, never the window", async () => {
		const { store } = await openStore();
		const { request } = await personHost({ browserSitePermissionStore: store });
		const callback = vi.fn();
		request({ id: 99 }, "fullscreen", callback, { isMainFrame: false, requestingUrl: "https://player.example/embed" });
		expect(callback).toHaveBeenCalledWith(true);

		const { invoke, constructorOptions } = setupTabHost();
		await invoke("browser:ensure", "sess-1");
		expect(constructorOptions.at(-1)?.webPreferences).toMatchObject({ disableHtmlFullscreenWindowResize: true });
	});

	it("opens mailto: and tel: links in another app only after the person confirms", async () => {
		const { store } = await openStore();
		const openExternal = vi.fn(async () => undefined);
		const { invoke, sent, viewId, webContentsListeners } = await personHost({
			browserSitePermissionStore: store,
			shell: { openExternal },
		});
		const event = { preventDefault: vi.fn() };
		webContentsListeners.get("will-navigate")?.(event as never, "mailto:team@example.com" as never);
		expect(event.preventDefault).toHaveBeenCalled();
		expect(openExternal).not.toHaveBeenCalled();
		const prompt = navStates(sent).at(-1)!.sitePermissionPrompt!;
		expect(prompt).toMatchObject({ origin: ORIGIN, kinds: [], externalUrl: "mailto:team@example.com" });
		expect(navStates(sent).at(-1)?.error).toBeUndefined();

		await invoke("browser:sitePermission:respond", { viewId, promptId: prompt.id, allow: true });
		expect(openExternal).toHaveBeenCalledWith("mailto:team@example.com");
		// A confirm is one-time: nothing is remembered for the site.
		expect(navStates(sent).at(-1)?.sitePermissions).toBeUndefined();

		webContentsListeners.get("will-navigate")?.({ preventDefault: vi.fn() } as never, "tel:+15551234" as never);
		const tel = navStates(sent).at(-1)!.sitePermissionPrompt!;
		await invoke("browser:sitePermission:respond", { viewId, promptId: tel.id, allow: false });
		expect(openExternal).toHaveBeenCalledTimes(1);
	});

	it("lets only a person's own tab in the session profile ask", () => {
		const session = { profileId: PROFILE, profilePartition: "persist:profile" };
		const person = { partition: "persist:profile" };
		expect(sitePermissionScope(person, session)).toBe(PROFILE);
		expect(sitePermissionScope(person, { profileId: null, profilePartition: "persist:profile" })).toBe("persist:profile");
		// An agent tab the person granted their logins shares the partition.
		expect(sitePermissionScope({ ...person, agentDriven: true }, session)).toBeUndefined();
		expect(sitePermissionScope({ ...person, agentTab: "" }, session)).toBeUndefined();
		expect(sitePermissionScope({ ...person, remote: {} as never }, session)).toBeUndefined();
		// The throwaway agent partition, or a tab left from a profile switch.
		expect(sitePermissionScope({ partition: "persist:agent" }, session)).toBeUndefined();
		expect(sitePermissionScope(person, undefined)).toBeUndefined();
	});
});
