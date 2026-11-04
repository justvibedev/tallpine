import { mkdtemp, readFile, writeFile, chmod } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { DAEMON_SERVICE_NAME } from "../shared/daemon-attach";
import { createPasswordExchange } from "./remote-token";
import { probeRemote } from "./remote-request";
import { RemoteRegistry } from "./remote-registry";
import { registerRemotesIpc } from "./remotes-main";

const TOKEN = `vot_${"a".repeat(43)}`;

// A stand-in for a host running the new daemon: the displayed code is
// redeemable once at /pair/exchange (or the old password as the bearer), and
// every other route needs the issued token.
function fakeHost(opts: { code?: string; legacy?: string } = {}) {
	let code = opts.code ?? "";
	const calls: string[] = [];
	const fetchImpl = (async (input: string | URL, init?: RequestInit) => {
		const url = new URL(String(input));
		const auth = new Headers(init?.headers).get("authorization");
		calls.push(`${init?.method ?? "GET"} ${url.pathname}`);
		if (url.pathname === "/pair/exchange") {
			const body = JSON.parse(String(init?.body)) as { code?: string };
			const legacyOk = opts.legacy !== undefined && auth === `Bearer ${opts.legacy}`;
			if (legacyOk || (code && body.code === code)) {
				if (!legacyOk) code = ""; // single use
				return new Response(JSON.stringify({ token: TOKEN }), { status: 200 });
			}
			return new Response("{}", { status: 401 });
		}
		if (url.pathname === "/healthz") {
			if (auth !== `Bearer ${TOKEN}`) return new Response("{}", { status: 401 });
			return new Response(JSON.stringify({ status: "ok", service: DAEMON_SERVICE_NAME, pid: 4242 }), { status: 200 });
		}
		return new Response("{}", { status: 404 });
	}) as typeof fetch;
	return { fetchImpl, calls };
}

describe("createPasswordExchange", () => {
	it("trades the displayed code for a device token", async () => {
		const host = fakeHost({ code: "Code1234" });
		const exchange = createPasswordExchange(host.fetchImpl, async () => "Studio Mac");
		expect(await exchange({ url: "http://192.0.2.5:3011", password: "Code1234" }, "desktop-h1")).toBe(TOKEN);
	});

	it("leaves a device token alone and never calls the host", async () => {
		const host = fakeHost();
		const exchange = createPasswordExchange(host.fetchImpl, async () => "x");
		expect(await exchange({ url: "http://192.0.2.5:3011", password: TOKEN }, "i")).toBe(TOKEN);
		expect(host.calls).toEqual([]);
	});

	it("keeps the password when the host refuses it or never answers", async () => {
		const refused = createPasswordExchange(fakeHost({ code: "other" }).fetchImpl, async () => "x");
		expect(await refused({ url: "http://192.0.2.5:3011", password: "wrong" }, "i")).toBe("wrong");
		const down = createPasswordExchange((async () => { throw new Error("down"); }) as typeof fetch, async () => "x");
		expect(await down({ url: "http://192.0.2.5:3011", password: "pw" }, "i")).toBe("pw");
	});
});

type Handler = (event: unknown, ...args: unknown[]) => Promise<unknown>;

async function savedFile(entries: object[]): Promise<string> {
	const dir = await mkdtemp(join(tmpdir(), "vo-remote-token-"));
	const path = join(dir, "remotes.json");
	await writeFile(path, JSON.stringify({ remotes: entries }), "utf8");
	await chmod(path, 0o600);
	return path;
}

function setup(file: string, host: ReturnType<typeof fakeHost>) {
	const handlers = new Map<string, Handler>();
	registerRemotesIpc({ handle: (c: string, h: Handler) => void handlers.set(c, h) }, {
		file,
		registry: new RemoteRegistry(async () => { throw new Error("no proxy"); }),
		localIdentity: async () => "h_this_mac",
		identity: async () => "h_remote",
		probe: (entry) => probeRemote(entry, host.fetchImpl),
		exchange: createPasswordExchange(host.fetchImpl, async () => "Studio Mac"),
	});
	return (channel: string, ...args: unknown[]) => handlers.get(channel)!({}, ...args);
}

describe("desktop remote hosts with per-device tokens", () => {
	it("a host added with the displayed code authenticates and stores only the token", async () => {
		const file = await savedFile([]);
		const invoke = setup(file, fakeHost({ code: "Code1234" }));
		expect(await invoke("remotes:add", { label: "box", url: "http://192.0.2.5:3011", password: "Code1234" })).toBe("online");
		const saved = await readFile(file, "utf8");
		expect(saved).toContain(TOKEN);
		expect(saved).not.toContain("Code1234");
	});

	it("a host saved with the old password is upgraded on connect", async () => {
		const file = await savedFile([{ hostId: "h_remote", label: "box", url: "http://192.0.2.5:3011", password: "oldpass1" }]);
		const invoke = setup(file, fakeHost({ legacy: "oldpass1" }));
		await expect(invoke("remotes:connect", "http://192.0.2.5:3011")).rejects.toThrow(); // no proxy in this test
		const saved = await readFile(file, "utf8");
		expect(saved).toContain(TOKEN);
		expect(saved).not.toContain("oldpass1");
	});

	it("a password edited to a fresh code is exchanged too", async () => {
		const file = await savedFile([{ hostId: "h_remote", label: "box", url: "http://192.0.2.5:3011", password: "stale" }]);
		const invoke = setup(file, fakeHost({ code: "Code5678" }));
		expect(await invoke("remotes:update", "http://192.0.2.5:3011", { password: "Code5678" })).toBe("online");
		expect(await readFile(file, "utf8")).toContain(TOKEN);
	});
});
