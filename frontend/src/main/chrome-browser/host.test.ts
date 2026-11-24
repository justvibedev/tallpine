import { describe, expect, it, vi } from "vitest";
import { setupChromeHost as setup } from "./chrome-test-helpers";

describe("ChromeBrowserHost", () => {
	it("open creates the session's tab in a new window and navigates it through the native runtime", async () => {
		const { host, calls, connection } = setup();
		const result = await host.execute("s1", "open", { url: "example.com" });
		expect(connection().sent.find((entry) => entry.method === "Target.createTarget")?.params).toEqual({ url: "about:blank", newWindow: true });
		expect(calls.map((call) => [call.key, call.action, call.args])).toEqual([
			["chrome:s1", "tab-select", { tabId: "T1" }],
			["chrome:s1", "open", { url: "https://example.com/" }],
		]);
		expect(result).toMatchObject({ url: "https://example.com/", title: "Example", target: { tabId: "T1" } });
		expect(host.state("s1")).toMatchObject({ running: true, activeTab: { url: "about:blank" } });
	});

	it("snapshot and click run on the session's tab; the native tab is selected once", async () => {
		const { host, calls } = setup();
		const snapshot = await host.execute("s1", "snapshot", { interactive: true });
		expect(snapshot).toMatchObject({ text: '- button "Save" [ref=e1]', target: { tabId: "T1" }, untrustedExternalContent: true });
		await host.execute("s1", "click", { ref: "e1" });
		expect(calls.map((call) => call.action)).toEqual(["tab-select", "snapshot", "click"]);
		expect(calls.at(-1)?.args).toEqual({ ref: "e1" });
		await expect(host.execute("s1", "click", {})).rejects.toMatchObject({ code: "REFERENCE_REQUIRED" });
	});

	it("act matches the instruction against a snapshot and clicks the ref", async () => {
		const { host, calls } = setup();
		const result = await host.execute("s1", "act", { instruction: "click Save" });
		expect(result).toMatchObject({ outcome: "matched", resolvedRef: "e1", inputDispatched: true, retried: false });
		expect(calls.at(-1)).toMatchObject({ action: "click", args: { ref: "e1" } });
	});

	it("screenshot captures the session's tab", async () => {
		const { host, runtime } = setup();
		const result = await host.execute("s1", "screenshot", { annotate: true });
		expect(runtime.screenshot).toHaveBeenCalledWith("chrome:s1", expect.anything(), undefined, { annotate: true });
		expect(result).toMatchObject({ data: "iVBOR", width: 2, target: { tabId: "T1" } });
	});

	it("tabs, tab-new, tab-select and tab-close see only the session's tabs", async () => {
		const { host, connection, calls } = setup();
		await host.execute("s1", "open", { url: "https://a.example/" });
		await host.execute("s2", "open", { url: "https://b.example/" });
		connection().openPage("HAND", "https://bank.example/");
		const created = (await host.execute("s1", "tab-new", { url: "https://c.example/" })) as { id: string };
		const tabs = (await host.execute("s1", "tabs")) as { tabs: { id: string; active: boolean }[]; activeTabId: string };
		expect(tabs.tabs.map((tab) => tab.id)).toEqual(["T1", created.id]);
		expect(tabs.activeTabId).toBe(created.id);
		const before = calls.length;
		await expect(host.execute("s1", "tab-select", { tabId: "T2" })).rejects.toMatchObject({ code: "BROWSER_TARGET_UNAVAILABLE" });
		await expect(host.execute("s1", "tab-select", { tabId: "HAND" })).rejects.toMatchObject({ code: "BROWSER_TARGET_UNAVAILABLE" });
		// A refused select never moves the native daemon.
		expect(calls.length).toBe(before);
		await host.execute("s1", "tab-select", { tabId: "T1" });
		const closed = (await host.execute("s1", "tab-close", { tabId: "T1" })) as { closedTabId: string; tabs: { id: string }[]; activeTabId: string };
		expect(closed.closedTabId).toBe("T1");
		expect(closed.tabs.map((tab) => tab.id)).toEqual([created.id]);
		expect(closed.activeTabId).toBe(created.id);
	});

	it("__destroy-session closes the session's tabs and native runtime only", async () => {
		const { host, closed, connection } = setup();
		await host.execute("s1", "open", { url: "https://a.example/" });
		await host.execute("s2", "open", { url: "https://b.example/" });
		await host.execute("s1", "__destroy-session");
		expect(closed).toEqual(["chrome:s1"]);
		expect(connection().sent.filter((entry) => entry.method === "Target.closeTarget").map((entry) => entry.params.targetId)).toEqual(["T1"]);
		expect(host.state("s1").activeTab).toBeNull();
		expect(host.state("s2").activeTab).not.toBeNull();
	});

	it("closing the window closes every session's tabs and stops following Chrome (a8)", async () => {
		const { host, closed, connection } = setup();
		await host.execute("s1", "open", { url: "https://a.example/" });
		await host.execute("s2", "open", { url: "https://b.example/" });
		connection().openPage("HAND", "https://bank.example/");
		await host.dispose();
		expect(closed.sort()).toEqual(["chrome:s1", "chrome:s2"]);
		const closedTabs = connection().sent.filter((entry) => entry.method === "Target.closeTarget").map((entry) => entry.params.targetId);
		expect(closedTabs.sort()).toEqual(["T1", "T2"]);
		// Only the debugger sessions' listeners could remain, and they are released too.
		expect(connection().listenerCount).toBe(0);
	});

	it("fails an in-flight command BROWSER_CHROME_CLOSED and relaunches on the next one", async () => {
		const { host, runtime, quit, process, closed } = setup();
		await host.execute("s1", "open", { url: "https://a.example/" });
		runtime.runAction.mockImplementationOnce(async () => {
			quit();
			throw Object.assign(new Error("agent-browser lost its page"), { code: "AGENT_BROWSER_COMMAND_FAILED" });
		});
		await expect(host.execute("s1", "click", { ref: "e1" })).rejects.toMatchObject({ code: "BROWSER_CHROME_CLOSED" });
		expect(host.state("s1").running).toBe(false);
		await host.execute("s1", "snapshot");
		expect(process.ensure).toHaveBeenCalledTimes(3);
		expect(closed).toContain("chrome:s1");
		expect(host.state("s1")).toMatchObject({ running: true, activeTab: { url: "about:blank" } });
	});

	it("records launch failures for the panel card", async () => {
		const { host, process, states } = setup();
		process.ensure.mockRejectedValueOnce(Object.assign(new Error("Google Chrome is not installed."), { code: "BROWSER_CHROME_NOT_FOUND" }));
		await expect(host.execute("s1", "snapshot")).rejects.toMatchObject({ code: "BROWSER_CHROME_NOT_FOUND" });
		expect(host.state("s1").error).toEqual({ code: "BROWSER_CHROME_NOT_FOUND", message: "Google Chrome is not installed." });
		expect(states.at(-1)).toMatchObject({ sessionId: "s1", error: { code: "BROWSER_CHROME_NOT_FOUND" } });
	});

	it("refuses an action it does not know with a clear code", async () => {
		const { host } = setup();
		await expect(host.execute("s1", "no-such-action")).rejects.toMatchObject({ code: "BROWSER_CHROME_ACTION_UNSUPPORTED" });
	});

	it("showWindow activates the session's tab", async () => {
		const { host, connection } = setup();
		expect(await host.showWindow("s1")).toEqual({ shown: false });
		await host.execute("s1", "open", { url: "https://a.example/" });
		expect(await host.showWindow("s1")).toEqual({ shown: true });
		expect(connection().sent.at(-1)).toEqual({ method: "Target.activateTarget", params: { targetId: "T1" } });
	});

	it("handoffPage brings the session's tab to the front and reads its URL (B858)", async () => {
		const { host, connection } = setup();
		await expect(host.handoffPage("s1")).rejects.toMatchObject({ code: "INVALID_ARGUMENT" });
		await host.execute("s1", "open", { url: "https://a.example/" });
		const page = await host.handoffPage("s1");
		expect(connection().sent.at(-1)).toEqual({ method: "Target.activateTarget", params: { targetId: "T1" } });
		expect(page.url()).toBe(host.state("s1").activeTab?.url);
		await host.destroySession("s1");
		expect(page.url()).toBeUndefined();
	});

	it("sign-in to sites tells every watching card, refuses agent commands, then clears that error (B854)", async () => {
		const { host, process, states } = setup();
		expect(host.watch("idle")).toMatchObject({ running: true, signingIn: false });
		await host.execute("s1", "open", { url: "example.com" });
		expect(await host.signIn("s1")).toMatchObject({ running: false, signingIn: true });
		expect(process.signIn).toHaveBeenCalledTimes(1);
		expect(states).toContainEqual(expect.objectContaining({ sessionId: "idle", signingIn: true }));
		await expect(host.execute("s1", "snapshot", {})).rejects.toMatchObject({ code: "BROWSER_CHROME_SIGNING_IN" });
		expect(host.state("s1").error?.code).toBe("BROWSER_CHROME_SIGNING_IN");
		expect(await host.finishSignIn("s1")).toMatchObject({ running: true, signingIn: false });
		expect(host.state("s1").error).toBeUndefined();
		expect(states.at(-1)).toMatchObject({ signingIn: false });
		await host.execute("s1", "open", { url: "example.com" });
	});

	it("records a sign-in launch failure for the card", async () => {
		const { host, process } = setup();
		process.signIn.mockRejectedValueOnce(Object.assign(new Error("not installed"), { code: "BROWSER_CHROME_NOT_FOUND" }));
		expect(await host.signIn("s1")).toMatchObject({ error: { code: "BROWSER_CHROME_NOT_FOUND" } });
	});

	it("open on Google's refused sign-in page tells the agent to ask for a manual sign-in (B854)", async () => {
		const { host, runtime } = setup();
		runtime.runAction.mockImplementationOnce(async () => ({ targetId: "T1" })).mockImplementationOnce(async () => ({
			url: "https://accounts.google.com/v3/signin/rejected?continue=x",
			title: "Sign in",
		}));
		expect(await host.execute("s1", "open", { url: "https://accounts.google.com/" })).toMatchObject({ signInHint: expect.stringContaining("tallpine browser handoff") });
		expect(await host.execute("s1", "open", { url: "example.com" })).not.toHaveProperty("signInHint");
	});

	it("any command that lands on a Google sign-in page carries the hint, not only open (B854)", async () => {
		const { host, connection } = setup((action) => {
			if (action === "click") {
				connection().emit({
					method: "Target.targetInfoChanged",
					params: { targetInfo: { targetId: "T1", type: "page", url: "https://accounts.google.com/v3/signin/identifier?continue=x", title: "Sign in" } },
				});
			}
			return { ok: true };
		});
		await host.execute("s1", "open", { url: "https://accounts.google.com/" });
		expect(await host.execute("s1", "press", { key: "Tab" })).not.toHaveProperty("signInHint");
		expect(await host.execute("s1", "click", { ref: "e1" })).toMatchObject({ signInHint: expect.stringContaining("tallpine browser handoff") });
		expect(await host.execute("s1", "act", { instruction: "click Save" })).toMatchObject({ signInHint: expect.stringContaining("tallpine browser handoff") });
	});

	describe("Google sign-in goes to plain Chrome (B854)", () => {
		const identifier = "https://accounts.google.com/v3/signin/identifier?continue=https%3A%2F%2Fanalytics.google.com%2Fanalytics%2Fweb%2F&flowName=x";
		const rejected = "https://accounts.google.com/v3/signin/rejected?continue=https%3A%2F%2Fanalytics.google.com%2Fanalytics%2Fweb%2F";
		const navigate = (connection: { emit: (event: { method: string; params: Record<string, unknown> }) => void }, targetId: string, url: string) =>
			connection.emit({ method: "Target.targetInfoChanged", params: { targetInfo: { targetId, type: "page", url, title: "Sign in" } } });

		it("the hint covers every Google sign-in page, not only the refusal", async () => {
			const { host, connection } = setup((action) => {
				if (action === "click") navigate(connection(), "T1", identifier);
				return { ok: true };
			});
			await host.execute("s1", "open", { url: "example.com" });
			expect(await host.execute("s1", "click", { ref: "e1" })).toMatchObject({ signInHint: expect.stringContaining("Never ask the person to sign in in this window") });
		});

		it("an agent tab reaching Google's refusal reopens the profile in plain Chrome at the page the sign-in continues to", async () => {
			const { host, process, connection } = setup();
			await host.execute("s1", "open", { url: "example.com" });
			const controlled = connection();
			connection().openPage("P9", "https://example.org/");
			navigate(controlled, "P9", rejected);
			expect(process.signIn).not.toHaveBeenCalled();
			navigate(controlled, "T1", identifier);
			expect(process.signIn).not.toHaveBeenCalled();
			navigate(controlled, "T1", rejected);
			await vi.waitFor(() => expect(process.signIn).toHaveBeenCalledWith("https://analytics.google.com/analytics/web/"));
			navigate(controlled, "T1", rejected);
			expect(process.signIn).toHaveBeenCalledTimes(1);
			expect(host.state("s1")).toMatchObject({ signingIn: true });
		});

		it("a handoff on Google sign-in opens plain Chrome and ends when the person is done there", async () => {
			const { host, process, connection } = setup();
			await host.execute("s1", "open", { url: "example.com" });
			navigate(connection(), "T1", identifier);
			const page = await host.handoffPage("s1");
			expect(process.signIn).toHaveBeenCalledWith("https://analytics.google.com/analytics/web/");
			expect(page.signIn).toBe(true);
			let ended = false;
			void page.ended?.then(() => { ended = true; });
			await Promise.resolve();
			expect(ended).toBe(false);
			await host.finishSignIn("s1");
			await vi.waitFor(() => expect(ended).toBe(true));
			await page.release?.();
			expect(process.finishSignIn).toHaveBeenCalledTimes(1);
		});

		it("a sign-in handoff that ends another way still closes plain Chrome so the agent's Chrome returns", async () => {
			const { host, process, connection } = setup();
			await host.execute("s1", "open", { url: "example.com" });
			navigate(connection(), "T1", identifier);
			const page = await host.handoffPage("s1");
			await page.release?.();
			expect(process.finishSignIn).toHaveBeenCalledTimes(1);
			expect(host.state("s1")).toMatchObject({ signingIn: false, running: true });
		});

		it("a handoff elsewhere keeps the agent's Chrome", async () => {
			const { host, process } = setup();
			await host.execute("s1", "open", { url: "example.com" });
			const page = await host.handoffPage("s1");
			expect(page.signIn).toBeUndefined();
			expect(process.signIn).not.toHaveBeenCalled();
		});

		it("Sign in to sites on a Google sign-in tab opens plain Chrome where that sign-in continues", async () => {
			const { host, process, connection } = setup();
			await host.execute("s1", "open", { url: "example.com" });
			navigate(connection(), "T1", identifier);
			await host.signIn("s1");
			expect(process.signIn).toHaveBeenCalledWith("https://analytics.google.com/analytics/web/");
		});
	});
});
