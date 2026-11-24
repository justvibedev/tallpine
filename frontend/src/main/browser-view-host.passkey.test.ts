import { describe, expect, it, vi } from "vitest";
import type { BrowserNavState } from "./browser-view-host";
import { setupHost } from "./browser-view-host-test-helpers";

vi.mock("electron", async (importOriginal) => {
	const actual = await importOriginal<typeof import("electron")>();
	return { ...actual, net: { fetch: vi.fn() } };
});

const navStates = (sent: Array<{ channel: string; payload: unknown }>) =>
	sent.filter((entry) => entry.channel === "browser:navState").map((entry) => entry.payload as BrowserNavState);

describe("browser passkey request state", () => {
	it("pushes passkeyPending for the active tab while its page awaits a passkey", async () => {
		const { invoke, send, sent } = setupHost();
		await invoke("browser:ensure", "sess-1");

		send("browser:webauthn:state", 99, true);
		expect(navStates(sent).at(-1)?.passkeyPending).toBe(true);

		send("browser:webauthn:state", 99, false);
		expect(navStates(sent).at(-1)?.passkeyPending).toBeUndefined();
	});

	it("clears the pending passkey when the tab navigates to a new document", async () => {
		const { invoke, send, sent, webContentsListeners } = setupHost();
		await invoke("browser:ensure", "sess-1");
		send("browser:webauthn:state", 99, true);

		webContentsListeners.get("did-navigate")?.({} as never, "https://accounts.example/next" as never);

		expect(navStates(sent).at(-1)?.passkeyPending).toBeUndefined();
	});

	it("ignores reports from unknown senders and non-boolean payloads", async () => {
		const { invoke, send, sent } = setupHost();
		await invoke("browser:ensure", "sess-1");
		const before = navStates(sent).length;

		send("browser:webauthn:state", 12345, true);
		send("browser:webauthn:state", 99, "yes");

		expect(navStates(sent).length).toBe(before);
	});

	it("exposes the pre-redirect page as the external URL while an IdP page awaits a passkey", async () => {
		const { invoke, send, sent, webContentsListeners } = setupHost();
		await invoke("browser:ensure", "sess-1");
		const idp = "https://accounts.example.com/challenge?TL=secret";
		webContentsListeners.get("did-navigate")?.({} as never, "https://app.example.com/dash" as never);
		await invoke("browser:navigate", { viewId: "1:sess-1", url: idp });
		webContentsListeners.get("did-navigate")?.({} as never, idp as never);
		send("browser:webauthn:state", 99, true);

		const state = navStates(sent).at(-1);
		expect(state?.passkeyPending).toBe(true);
		expect(state?.passkeyExternalUrl).toBe("https://app.example.com/dash");
	});
});
