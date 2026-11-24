import { describe, expect, it, vi } from "vitest";
import { applyChromeUserAgent, applyChromeUserAgentToContents, chromeClientHints, chromeUserAgent } from "./browser-user-agent";

const ELECTRON_UA =
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Tallpine/1.4.2 Chrome/140.0.7339.133 Electron/38.2.0 Safari/537.36";
const CHROME_UA =
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.7339.133 Safari/537.36";

describe("chromeUserAgent", () => {
	it("drops the app and Electron tokens", () => {
		expect(chromeUserAgent(ELECTRON_UA)).toBe(CHROME_UA);
	});
	it("leaves a plain Chrome UA alone", () => {
		expect(chromeUserAgent(CHROME_UA)).toBe(CHROME_UA);
	});
});

describe("applyChromeUserAgent", () => {
	it("sets the cleaned UA once per session", () => {
		const session = { getUserAgent: () => ELECTRON_UA, setUserAgent: vi.fn() };
		applyChromeUserAgent(session);
		applyChromeUserAgent(session);
		expect(session.setUserAgent).toHaveBeenCalledTimes(1);
		expect(session.setUserAgent).toHaveBeenCalledWith(CHROME_UA);
	});
	it("ignores sessions without the API", () => {
		expect(() => applyChromeUserAgent(undefined)).not.toThrow();
		expect(() => applyChromeUserAgent({} as never)).not.toThrow();
	});
});

describe("client hints", () => {
	it("builds Chrome brands matching the version and platform", () => {
		expect(chromeClientHints("152.0.7977.169", "darwin")).toEqual({
			"Sec-CH-UA": '"Not?A_Brand";v="24", "Chromium";v="152"',
			"Sec-CH-UA-Mobile": "?0",
			"Sec-CH-UA-Platform": '"macOS"',
		});
	});
	it("adds them to HTTPS requests through the session", () => {
		const onBeforeSendHeaders = vi.fn();
		applyChromeUserAgent({ getUserAgent: () => ELECTRON_UA, setUserAgent: vi.fn(), webRequest: { onBeforeSendHeaders } });
		expect(onBeforeSendHeaders.mock.calls[0][0]).toEqual({ urls: ["https://*/*"] });
		const callback = vi.fn();
		onBeforeSendHeaders.mock.calls[0][1]({ requestHeaders: { Accept: "*/*" } }, callback);
		const sent = callback.mock.calls[0][0].requestHeaders;
		expect(sent.Accept).toBe("*/*");
		expect(sent["Sec-CH-UA"]).toContain('"Chromium"');
		expect(sent["Sec-CH-UA-Mobile"]).toBe("?0");
	});
});

describe("existing WebContents and hint version", () => {
	it("sets the cleaned UA on the contents itself", () => {
		const contents = { getUserAgent: () => ELECTRON_UA, setUserAgent: vi.fn() };
		applyChromeUserAgentToContents(contents);
		expect(contents.setUserAgent).toHaveBeenCalledWith(CHROME_UA);
		expect(() => applyChromeUserAgentToContents(undefined)).not.toThrow();
	});
	it("derives the hint major from the UA", () => {
		const onBeforeSendHeaders = vi.fn();
		const ua = ELECTRON_UA.replace("140.0.7339.133", "99.0.1.2");
		applyChromeUserAgent({ getUserAgent: () => ua, setUserAgent: vi.fn(), webRequest: { onBeforeSendHeaders } });
		const callback = vi.fn();
		onBeforeSendHeaders.mock.calls[0][1]({ requestHeaders: {} }, callback);
		expect(callback.mock.calls[0][0].requestHeaders["Sec-CH-UA"]).toContain('"Chromium";v="99"');
	});
});
