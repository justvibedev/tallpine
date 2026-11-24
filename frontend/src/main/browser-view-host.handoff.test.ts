import { describe, expect, it, vi } from "vitest";
import { setupHost } from "./browser-view-host-test-helpers";

vi.mock("./render-check-proxy", () => ({ startRenderCheckProxy: vi.fn(async () => 4321) }));

vi.mock("electron", async (importOriginal) => {
	const actual = await importOriginal<typeof import("electron")>();
	return { ...actual, net: { fetch: vi.fn() } };
});

describe("browser handoff page (B858)", () => {
	it("handoffPage reads the session's front tab URL (B858)", async () => {
		const { host, sent } = setupHost();
		await host.execute("sess-1", "open", { url: "https://login.example/2fa" });
		const before = sent.filter((event) => event.channel === "browser:agentActivity").length;
		const page = await host.handoffPage("sess-1");
		expect(page.url()).toBe("https://login.example/2fa");
		// Waiting on the person is not agent activity: the panel stays the person's.
		expect(sent.filter((event) => event.channel === "browser:agentActivity")).toHaveLength(before);
	});
});
