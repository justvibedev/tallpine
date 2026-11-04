import { describe, expect, it } from "vitest";
import { isLocalDaemonUrl } from "./daemon-tcp";

const base = "http://127.0.0.1:3101";

describe("isLocalDaemonUrl", () => {
	it("matches the API host and preview hosts on the daemon port", () => {
		expect(isLocalDaemonUrl("http://127.0.0.1:3101/api/v1/x", base)).toBe(true);
		expect(isLocalDaemonUrl("http://app.s1.localhost:3101/", base)).toBe(true);
	});
	it("leaves other ports, hosts and https alone", () => {
		expect(isLocalDaemonUrl("http://127.0.0.1:5173/", base)).toBe(false);
		expect(isLocalDaemonUrl("https://example.com/", base)).toBe(false);
		expect(isLocalDaemonUrl("http://example.com:3101/", base)).toBe(false);
		expect(isLocalDaemonUrl("nonsense", base)).toBe(false);
	});
});
