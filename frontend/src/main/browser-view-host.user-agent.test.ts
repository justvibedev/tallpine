import { describe, expect, it, vi } from "vitest";
import { setupTabHost } from "./browser-view-host-test-helpers";

vi.mock("electron", async (importOriginal) => {
	const actual = await importOriginal<typeof import("electron")>();
	return { ...actual, net: { fetch: vi.fn() } };
});

const CHROME_UA =
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.7339.133 Safari/537.36";

describe("browser tabs present a Chrome user agent", () => {
	it("cleans the UA on the tab contents and its session, and adds hints, before any load", async () => {
		const { host, views } = setupTabHost();
		await host.execute("sess-1", "tabs");
		const view = views[0];
		expect(view.webContents.setUserAgent).toHaveBeenCalledWith(CHROME_UA);
		expect(view.webContents.session.setUserAgent).toHaveBeenCalledWith(CHROME_UA);
		expect(view.webContents.session.webRequest.onBeforeSendHeaders).toHaveBeenCalledTimes(1);
	});

	it("also cleans an adopted popup's contents", async () => {
		const { host, views } = setupTabHost();
		await host.execute("sess-1", "tabs");
		const popup = views[0].webContents.openWindow("https://popup.example.test/") as unknown as {
			setUserAgent: ReturnType<typeof vi.fn>;
		};
		await vi.waitFor(() => expect(popup.setUserAgent).toHaveBeenCalledWith(CHROME_UA));
	});
});
