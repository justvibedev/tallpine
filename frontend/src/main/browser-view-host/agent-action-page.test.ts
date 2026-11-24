import { describe, expect, it, vi } from "vitest";
import { pageAction } from "./agent-action-page";
import type { AgentActionEnv } from "./agent-action-env";
import type { BrowserEntry, BrowserSessionEntry } from "./types";

// eval in the in-app browser is gated on the same login grant as driving the
// person's own tabs (B857): never in their named-profile tab without a grant,
// never in a viewer of a remote agent browser.
function env(entry: Partial<BrowserEntry>, session: Partial<BrowserSessionEntry>) {
	const sendCommand = vi.fn(async () => ({ result: { type: "number", value: 2 } }));
	const tab = {
		tabId: "t1",
		ready: Promise.resolve(),
		view: { webContents: { getURL: () => "https://a.example/", debugger: { isAttached: () => true, attach: vi.fn(), detach: vi.fn(), on: vi.fn(), sendCommand } } },
		...entry,
	} as unknown as BrowserEntry;
	const browserSession = {
		tabs: new Map([["t1", tab]]),
		activeTabId: "t1",
		profileId: "p1",
		profilePartition: "persist:p1",
		...session,
	} as unknown as BrowserSessionEntry;
	const actionEnv = {
		ctx: { fns: {} },
		sessionId: "s1",
		session: browserSession,
		signal: undefined,
		action: "eval",
		args: { expression: "1 + 1" },
		runNative: vi.fn(),
	} as unknown as AgentActionEnv;
	return { actionEnv, sendCommand };
}

describe("in-app eval login grant", () => {
	it("refuses the person's own tab in a named profile until they grant their logins", async () => {
		const { actionEnv, sendCommand } = env({}, { agentLoginsGrantedProfileId: undefined });
		await expect(pageAction(actionEnv)).rejects.toMatchObject({ code: "BROWSER_EVAL_NOT_GRANTED" });
		expect(sendCommand).not.toHaveBeenCalled();
	});

	it("runs in the person's own tab once they granted their logins", async () => {
		const { actionEnv, sendCommand } = env({}, { agentLoginsGrantedProfileId: "p1" });
		await expect(pageAction(actionEnv)).resolves.toMatchObject({ json: "2", target: { tabId: "t1" } });
		expect(sendCommand).toHaveBeenCalledWith("Runtime.evaluate", expect.objectContaining({ expression: "1 + 1" }));
	});

	it("runs in an agent tab without a grant, and never in a remote viewer tab", async () => {
		await expect(pageAction(env({ agentDriven: true }, {}).actionEnv)).resolves.toMatchObject({ json: "2" });
		await expect(pageAction(env({ agentTab: "remote-1" }, { agentLoginsGrantedProfileId: "p1" }).actionEnv)).rejects.toMatchObject({
			code: "BROWSER_EVAL_NOT_GRANTED",
		});
	});

	it("does not gate the other page commands on the grant", async () => {
		const { actionEnv } = env({}, {});
		actionEnv.action = "reload";
		const debug = (actionEnv.session.tabs.get("t1")!.view.webContents.debugger as unknown as { sendCommand: ReturnType<typeof vi.fn> });
		vi.useFakeTimers();
		try {
			const pending = pageAction(actionEnv);
			await vi.advanceTimersByTimeAsync(10_000);
			await expect(pending).resolves.toMatchObject({ isLoading: true });
		} finally {
			vi.useRealTimers();
		}
		expect(debug.sendCommand).toHaveBeenCalledWith("Page.reload");
	});
});
