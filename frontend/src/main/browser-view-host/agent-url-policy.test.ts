import { describe, expect, it } from "vitest";
import { assertAgentNavigationURL, isPrivateNetworkHost, normalizeAgentCommandURL } from "./agent-url-policy";
import { normalizeAgentBrowserURL } from "./url";

describe("agent navigation URL policy", () => {
	it.each([
		"https://example.com/a",
		"http://localhost:3000",
		"http://127.0.0.1:8080/preview",
		"http://[::1]:5173/",
		"http://172.32.0.1/",
		"http://172.15.255.255/",
		"http://11.0.0.1/",
		"http://169.253.1.1/",
		"http://100.63.255.255/",
		"http://100.128.0.1/",
		"http://[2001:db8::1]/",
	])("allows %s", (url) => {
		expect(assertAgentNavigationURL(url).href).toBe(new URL(url).href);
	});

	it.each([
		"file:///etc/passwd",
		"ftp://example.com/",
		"javascript:alert(1)",
		"data:text/html,hi",
	])("rejects non-http scheme %s", (url) => {
		expect(() => assertAgentNavigationURL(url)).toThrow("Navigation scheme is not permitted");
	});

	it.each([
		"http://169.254.169.254/latest/meta-data",
		"http://10.0.0.5/",
		"http://172.16.0.1/",
		"http://172.31.255.255/",
		"http://192.168.1.1/admin",
		"http://[fc00::1]/",
		"http://[fd12:3456::1]/",
		"http://[fe80::1]/",
		"http://[::ffff:169.254.169.254]/",
		"http://[::ffff:10.1.2.3]/",
		"http://0xa9fea9fe/",
		"http://100.64.0.1/",
		"http://100.101.102.103:8080/",
		"http://100.127.255.255/",
		"http://0.0.0.0/",
		"http://[64:ff9b::a00:1]/",
	])("rejects private-range target %s", (url) => {
		expect(() => assertAgentNavigationURL(url)).toThrow("private-network");
	});

	it("flags private hosts only for IP literals", () => {
		expect(isPrivateNetworkHost("192.168.0.1")).toBe(true);
		expect(isPrivateNetworkHost("intranet.example")).toBe(false);
	});

	it("applies to agent commands but not to the person's phone remote", () => {
		expect(() => normalizeAgentCommandURL("http://192.168.1.1/")).toThrow("private-network");
		expect(() => normalizeAgentCommandURL("/etc/passwd")).toThrow("local files");
		expect(normalizeAgentCommandURL("localhost:3000")).toBe("http://localhost:3000/");
		expect(normalizeAgentBrowserURL("http://192.168.1.20:3000/")).toBe("http://192.168.1.20:3000/");
	});
});
