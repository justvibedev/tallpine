import { describe, expect, it } from "vitest";
import { isLocalDaemonUrlOnPort } from "./daemon-url";

describe("isLocalDaemonUrlOnPort", () => {
	it("matches loopback and .localhost hosts on the daemon port only", () => {
		for (const url of ["http://127.0.0.1:3101/", "http://localhost:3101/a", "http://[::1]:3101/", "http://x.localhost:3101/"]) {
			expect(isLocalDaemonUrlOnPort(url, 3101)).toBe(true);
		}
		for (const url of ["http://127.0.0.1:3102/", "https://127.0.0.1:3101/", "http://example.com:3101/", "nope"]) {
			expect(isLocalDaemonUrlOnPort(url, 3101)).toBe(false);
		}
		expect(isLocalDaemonUrlOnPort("http://127.0.0.1:3101/", undefined)).toBe(false);
	});
});
