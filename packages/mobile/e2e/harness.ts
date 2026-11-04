import { spawn, spawnSync, type ChildProcess } from "node:child_process";
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import http from "node:http";
import net from "node:net";
import { networkInterfaces, tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { encodePairingCode } from "../lib/pairingCode";
import type { Endpoint } from "../lib/endpoints";
import { killProcessesUnder } from "./procs";

const here = fileURLToPath(new URL(".", import.meta.url));
const repoRoot = resolve(here, "../../..");
const fixtures = join(here, "fixtures");

/** A non-loopback IPv4 address of this machine. The daemon treats loopback as
 * proxied (not plain), so "plain http from the network" can only be exercised
 * through this address. Throws when there is none: the e2e fails, never skips. */
export function lanAddress(): string {
	for (const list of Object.values(networkInterfaces())) {
		for (const a of list ?? []) if (a.family === "IPv4" && !a.internal) return a.address;
	}
	throw new Error("phone e2e needs a non-loopback IPv4 address (connect to a network) to test plain-http refusal");
}

export type ScratchDaemonOptions = {
	/** Persist the retired requireEncryptedTransport key, as an older build wrote it. */
	requireEncryptedTransport?: boolean;
};

export type ScratchDaemon = {
	/** https base URL of the pinned LAN listener the phone talks to. */
	baseUrl: string;
	/** The LAN endpoint as the pairing code advertises it (https + pin). */
	endpoint: Endpoint;
	/** A pairing code the phone can redeem (single use). */
	pairingCode: string;
	hostId: string;
	pin: string;
	/** The daemon's gateway key pair (gateway-cert.pem, gateway-key.pem), whose key the pin names. */
	tlsDir: string;
	/** A chat-mode worker session served by the stub ACP agent. */
	sessionId: string;
	/** Mints another single-use pairing code. */
	mintPairingCode(): Promise<string>;
	/** Raw API call over the daemon's control socket (the desktop's path). */
	control(method: string, path: string, body?: unknown): Promise<{ status: number; body: any }>;
	stop(): Promise<void>;
};

/** An explicit environment, not a copy of ours: nothing from the developer's
 * shell (TALLPINE_*, tokens) may reach the scratch daemon. */
function scrubbedEnv(vars: Record<string, string>): NodeJS.ProcessEnv {
	return vars as NodeJS.ProcessEnv;
}

let built: string | undefined;

/** Builds the daemon once per process into the temp dir. Fails loudly: a
 * missing toolchain must fail the job, not skip the e2e. */
export function buildDaemon(): string {
	// e2e/global.ts builds once for the whole run and removes the binary after.
	if (process.env.TALLPINE_E2E_DAEMON_BIN) return process.env.TALLPINE_E2E_DAEMON_BIN;
	if (built) return built;
	const dir = mkdtempSync(join(tmpdir(), "tpe-bin-"));
	const out = join(dir, "tallpine");
	const res = spawnSync("nice", ["-n", "19", join(repoRoot, "scripts/go"), "build", "-o", out, "./cmd/tallpine"], {
		cwd: join(repoRoot, "backend"),
		env: scrubbedEnv({ HOME: dir, TMPDIR: tmpdir(), PATH: process.env.PATH ?? "", GOMAXPROCS: "4" }),
		encoding: "utf8",
	});
	if (res.status !== 0) {
		rmSync(dir, { recursive: true, force: true });
		throw new Error(`daemon build failed (${res.status}): ${res.stderr || res.error}`);
	}
	built = out;
	return out;
}

function freePort(): Promise<number> {
	return new Promise((ok, fail) => {
		const srv = net.createServer();
		srv.once("error", fail);
		srv.listen(0, "127.0.0.1", () => {
			const { port } = srv.address() as net.AddressInfo;
			srv.close(() => ok(port));
		});
	});
}

function git(cwd: string, ...args: string[]): void {
	const res = spawnSync("git", ["-c", "user.name=e2e", "-c", "user.email=e2e@example.invalid", ...args], { cwd, encoding: "utf8" });
	if (res.status !== 0) throw new Error(`git ${args.join(" ")}: ${res.stderr}`);
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

/**
 * Boots a real daemon on a scratch data dir (never ~/.tallpine), enables the
 * mobile bridge, registers a project and starts one chat session, and returns
 * what a phone needs to pair with it. Reused by later mobile PRs.
 */
export async function startScratchDaemon(options: ScratchDaemonOptions = {}): Promise<ScratchDaemon> {
	const bin = buildDaemon();
	const root = mkdtempSync(join(process.env.TALLPINE_E2E_SCRATCH ?? tmpdir(), "tpe-"));
	const data = join(root, "data");
	const repo = join(root, "repo");
	const origin = join(root, "origin.git");
	for (const d of [join(data, "mobile"), repo, origin]) mkdirSync(d, { recursive: true });
	git(origin, "init", "-q", "--bare", "-b", "main");
	git(repo, "init", "-q", "-b", "main");
	git(repo, "commit", "-q", "--allow-empty", "-m", "init");
	git(repo, "remote", "add", "origin", origin);
	git(repo, "push", "-q", "origin", "main");
	git(repo, "remote", "set-head", "origin", "main");

	chmodSync(join(fixtures, "claude"), 0o755);
	chmodSync(join(fixtures, "fake-claude-acp.mjs"), 0o755);
	const lanPort = await freePort();
	writeFileSync(
		join(data, "mobile", "config.json"),
		JSON.stringify({ featureEnabled: true, enabled: true, lastPort: lanPort, ...(options.requireEncryptedTransport ? { requireEncryptedTransport: true } : {}) }),
	);

	const runFile = join(root, "running.json");
	let log = "";
	const child: ChildProcess = spawn(bin, ["daemon"], {
		env: scrubbedEnv({
			HOME: root,
			TMPDIR: tmpdir(),
			PATH: `${fixtures}:${process.env.PATH ?? ""}`,
			TALLPINE_DATA_DIR: data,
			TALLPINE_RUN_FILE: runFile,
			TALLPINE_PORT: String(await freePort()),
			TALLPINE_CLAUDE_ACP_COMMAND: join(fixtures, "fake-claude-acp.mjs"),
		}),
		stdio: ["ignore", "pipe", "pipe"],
		detached: true, // own process group, so stop() can take helpers down with it
	});
	child.stdout?.on("data", (c) => (log += c));
	child.stderr?.on("data", (c) => (log += c));
	let exited = false;
	child.on("exit", () => (exited = true));

	const stop = async () => {
		const signalGroup = (sig: NodeJS.Signals) => { try { process.kill(-child.pid!, sig); } catch { /* gone */ } };
		if (!exited) {
			signalGroup("SIGTERM");
			for (let i = 0; i < 100 && !exited; i++) await sleep(100);
		}
		signalGroup("SIGKILL"); // anything the daemon left behind
		// Chat hosts and their ACP shims call setsid, so the group kill misses them.
		await killProcessesUnder(root);
		// The daemon's helpers write under HOME (.dsh) for a moment after it exits.
		const clean = () => rmSync(root, { recursive: true, force: true, maxRetries: 10, retryDelay: 100 });
		clean();
		await sleep(500);
		clean();
	};
	const fail = async (why: string): Promise<never> => {
		const tail = log.split("\n").slice(-25).join("\n");
		await stop();
		throw new Error(`${why}\n--- daemon log tail ---\n${tail}`);
	};

	const sock = join(root, "sock", "daemon.sock");
	const tokenFile = join(root, "daemon.token");
	for (let i = 0; i < 300 && !(existsSync(sock) && existsSync(tokenFile)); i++) {
		if (exited) return fail("daemon exited during boot");
		await sleep(100);
	}
	if (!existsSync(sock) || !existsSync(tokenFile)) return fail("daemon control socket never appeared");
	const token = readFileSync(tokenFile, "utf8").trim();

	const control: ScratchDaemon["control"] = (method, path, body) =>
		new Promise((ok, bad) => {
			const req = http.request(
				{ socketPath: sock, path, method, headers: { "X-VO-Token": token, "Content-Type": "application/json" } },
				(res) => {
					let text = "";
					res.on("data", (c) => (text += c));
					res.on("end", () => {
						let parsed: any = text;
						try { parsed = JSON.parse(text); } catch { /* keep text */ }
						ok({ status: res.statusCode ?? 0, body: parsed });
					});
				},
			);
			req.on("error", bad);
			req.end(body === undefined ? undefined : JSON.stringify(body));
		});

	try {
		const project = await control("POST", "/api/v1/projects", { path: repo, name: "e2e" });
		if (project.status !== 201) return fail(`add project: ${project.status} ${JSON.stringify(project.body)}`);
		const spawned = await control("POST", "/api/v1/sessions", {
			projectId: project.body.project.id,
			prompt: "hello from the e2e",
			harness: "claude-code",
			mode: "chat",
			kind: "worker",
		});
		if (spawned.status !== 201) return fail(`spawn chat session: ${spawned.status} ${JSON.stringify(spawned.body)}`);
		const sessionId: string = spawned.body.session.id;

		const enable = async () => {
			const res = await control("POST", "/api/v1/mobile/enable");
			if (res.status !== 200 || !res.body.password) return fail(`mobile enable: ${res.status} ${JSON.stringify(res.body)}`);
			return res.body as { password: string; hostId: string; tlsPin: string; port: number };
		};
		const first = await enable();
		if (!first.tlsPin) return fail("daemon reported no TLS pin");
		const endpoint: Endpoint = { kind: "lan", host: "127.0.0.1", port: first.port, secure: true, pin: first.tlsPin };
		const offer = (password: string) =>
			encodePairingCode({ v: 2, hostId: first.hostId, name: "e2e daemon", platform: "darwin", endpoints: [endpoint], token: password });
		// Regenerate rotates the credential: each code is single use.
		const mint = async () => offer((await enable()).password);

		return {
			baseUrl: `https://127.0.0.1:${first.port}`,
			endpoint,
			pairingCode: offer(first.password),
			hostId: first.hostId,
			pin: first.tlsPin,
			tlsDir: join(data, "mobile"),
			sessionId,
			mintPairingCode: mint,
			control,
			stop,
		};
	} catch (error) {
		await stop();
		throw error;
	}
}
