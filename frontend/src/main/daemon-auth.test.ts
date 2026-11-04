import { describe, expect, it, vi } from "vitest";
import {
	DAEMON_TOKEN_HEADER,
	DaemonTokenSource,
	daemonRequestGetsToken,
	daemonTokenPath,
	installDaemonAuthHeaders,
	isDaemonUrl,
	withDaemonToken,
	type DaemonAuthOptions,
} from "./daemon-auth";

const PORT = 3101;
const SHELL_ID = 7;
const options: DaemonAuthOptions = {
	port: () => PORT,
	token: () => "secret-token",
	isTrustedWebContents: (id) => id === SHELL_ID,
};

describe("daemonTokenPath", () => {
	it("sits next to running.json", () => {
		expect(daemonTokenPath("/home/u/.tallpine/running.json")).toBe("/home/u/.tallpine/daemon.token");
	});
});

describe("DaemonTokenSource", () => {
	it("retries until the daemon creates the file, then caches", () => {
		const readFile = vi.fn<(file: string) => string>()
			.mockImplementationOnce(() => {
				throw new Error("ENOENT");
			})
			.mockReturnValue("tok\n");
		const source = new DaemonTokenSource(() => "/x/daemon.token", readFile);
		expect(source.token()).toBeNull();
		expect(source.token()).toBe("tok");
		expect(source.token()).toBe("tok");
		expect(readFile).toHaveBeenCalledTimes(2);
		readFile.mockReturnValue("rotated");
		source.refresh();
		expect(source.token()).toBe("rotated");
	});
});

describe("isDaemonUrl", () => {
	it("matches only the daemon's loopback port over http and ws", () => {
		expect(isDaemonUrl("http://127.0.0.1:3101/api/v1/sessions", PORT)).toBe(true);
		expect(isDaemonUrl("ws://127.0.0.1:3101/mux", PORT)).toBe(true);
		expect(isDaemonUrl("http://localhost:3101/x", PORT)).toBe(true);
		expect(isDaemonUrl("http://[::1]:3101/x", PORT)).toBe(true);
		expect(isDaemonUrl("http://127.0.0.1:5173/x", PORT)).toBe(false);
		expect(isDaemonUrl("http://ao-preview.s1.localhost:3101/", PORT)).toBe(false);
		expect(isDaemonUrl("https://example.com:3101/", PORT)).toBe(false);
		expect(isDaemonUrl("http://127.0.0.1:3101/", undefined)).toBe(false);
	});
});

describe("daemonRequestGetsToken", () => {
	const app = { url: "app://renderer/index.html", parent: null };
	const renderPage = { url: "http://127.0.0.1:3101/api/v1/sessions/s1/renders/r1", parent: app };
	const artifactPage = { url: "http://127.0.0.1:3101/api/v1/sessions/s1/artifact-files/page.html", parent: app };

	it("attaches for the shell's REST, SSE, and /mux requests", () => {
		for (const [url, resourceType] of [
			["http://127.0.0.1:3101/api/v1/sessions", "xhr"],
			["http://127.0.0.1:3101/api/v1/events", "other"],
			["ws://127.0.0.1:3101/mux", "webSocket"],
		] as const) {
			expect(daemonRequestGetsToken({ url, resourceType, webContentsId: SHELL_ID, frame: app }, options)).toBe(true);
		}
	});

	it("attaches for main-process requests that have no webContents", () => {
		expect(daemonRequestGetsToken({ url: "http://127.0.0.1:3101/api/v1/identity", resourceType: "other" }, options)).toBe(true);
	});

	it("never attaches for other webContents or other hosts", () => {
		expect(daemonRequestGetsToken({ url: "http://127.0.0.1:3101/api/v1/sessions", resourceType: "xhr", webContentsId: 99, frame: app }, options)).toBe(false);
		expect(daemonRequestGetsToken({ url: "http://127.0.0.1:8080/api", resourceType: "xhr", webContentsId: SHELL_ID, frame: app }, options)).toBe(false);
	});

	it("keeps agent pages off the daemon API but lets them load their own files", () => {
		expect(daemonRequestGetsToken({ url: renderPage.url, resourceType: "subFrame", webContentsId: SHELL_ID, frame: app }, options)).toBe(true);
		expect(daemonRequestGetsToken({ url: "http://127.0.0.1:3101/api/v1/sessions", resourceType: "xhr", webContentsId: SHELL_ID, frame: renderPage }, options)).toBe(false);
		expect(daemonRequestGetsToken({ url: "ws://127.0.0.1:3101/mux", resourceType: "webSocket", webContentsId: SHELL_ID, frame: artifactPage }, options)).toBe(false);
		expect(
			daemonRequestGetsToken({ url: "http://127.0.0.1:3101/api/v1/sessions/s1/artifact-files/img.png", resourceType: "image", webContentsId: SHELL_ID, frame: artifactPage }, options),
		).toBe(true);
		expect(
			daemonRequestGetsToken({ url: "http://127.0.0.1:3101/api/v1/sessions/s2/artifact-files/img.png", resourceType: "image", webContentsId: SHELL_ID, frame: artifactPage }, options),
		).toBe(false);
	});

	it("refuses when the frame chain cannot be read", () => {
		const gone = {
			get url(): string {
				throw new Error("frame destroyed");
			},
			parent: null,
		};
		expect(daemonRequestGetsToken({ url: "http://127.0.0.1:3101/api/v1/sessions", resourceType: "xhr", webContentsId: SHELL_ID, frame: gone }, options)).toBe(false);
	});
});

describe("installDaemonAuthHeaders", () => {
	it("injects the header through onBeforeSendHeaders", () => {
		let listener: Parameters<Parameters<typeof installDaemonAuthHeaders>[0]["webRequest"]["onBeforeSendHeaders"]>[0] | undefined;
		installDaemonAuthHeaders({ webRequest: { onBeforeSendHeaders: (l) => (listener = l) } }, options);
		const run = (url: string, webContentsId?: number) => {
			let sent: Record<string, string> = {};
			listener?.({ url, resourceType: "xhr", webContentsId, frame: null, requestHeaders: { Accept: "*/*" } }, (r) => (sent = r.requestHeaders));
			return sent;
		};
		expect(run("http://127.0.0.1:3101/api/v1/sessions", SHELL_ID)).toEqual({ Accept: "*/*", [DAEMON_TOKEN_HEADER]: "secret-token" });
		expect(run("https://example.com/", SHELL_ID)).toEqual({ Accept: "*/*" });
	});
});

describe("withDaemonToken", () => {
	it("adds the header for daemon URLs only and keeps caller headers", async () => {
		const inner = vi.fn<typeof fetch>(async () => new Response("{}"));
		const wrapped = withDaemonToken(inner, options);
		await wrapped("http://127.0.0.1:3101/shutdown", { method: "POST", headers: { "content-type": "application/json" } });
		const headers = new Headers(inner.mock.calls[0]?.[1]?.headers);
		expect(headers.get(DAEMON_TOKEN_HEADER)).toBe("secret-token");
		expect(headers.get("content-type")).toBe("application/json");
		expect(inner.mock.calls[0]?.[1]?.method).toBe("POST");

		await wrapped("https://api.example.com/x");
		expect(inner.mock.calls[1]?.[1]).toBeUndefined();

		await wrapped(new URL("http://127.0.0.1:3101/api/v1/identity"), { headers: { [DAEMON_TOKEN_HEADER]: "explicit" } });
		expect(new Headers(inner.mock.calls[2]?.[1]?.headers).get(DAEMON_TOKEN_HEADER)).toBe("explicit");
	});

	it("passes through when no token file exists yet", async () => {
		const inner = vi.fn<typeof fetch>(async () => new Response("{}"));
		await withDaemonToken(inner, { port: () => PORT, token: () => null })("http://127.0.0.1:3101/api/v1/sessions");
		expect(inner.mock.calls[0]?.[1]).toBeUndefined();
	});
});
