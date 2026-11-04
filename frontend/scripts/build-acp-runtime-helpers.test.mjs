// @vitest-environment node
import {
	existsSync,
	mkdirSync,
	mkdtempSync,
	readFileSync,
	readdirSync,
	rmSync,
	symlinkSync,
	writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import {
	archiveExtraction,
	createWorkDirectory,
	npmInvocation,
	patchClaudeAutoCompactWindow,
	patchClaudeContextBreakdown,
	patchClaudeContextUsage,
	patchClaudeHibernationCheck,
	patchClaudeRetryDetails,
	pruneNodeDistribution,
	runtimeSourceFiles,
} from "./build-acp-runtime-helpers.mjs";

const temporaryDirectories = [];

afterEach(() => {
	for (const directory of temporaryDirectories.splice(0)) {
		rmSync(directory, { recursive: true, force: true });
	}
});

describe("createWorkDirectory", () => {
	it("places extraction on the output filesystem", () => {
		const outputRoot = temporaryDirectory();
		const workDirectory = createWorkDirectory(outputRoot);

		expect(dirname(workDirectory)).toBe(outputRoot);
		expect(existsSync(workDirectory)).toBe(true);
	});
});

describe("runtimeSourceFiles", () => {
	it("packages and fingerprints the ACP runtime manifest", () => {
		expect(runtimeSourceFiles()).toEqual([
			"package.json",
			"package-lock.json",
		]);
	});
});

describe("npmInvocation", () => {
	it("runs the parent npm CLI through Node on Windows", () => {
		expect(
			npmInvocation(["ci", "--omit=dev"], {
				platform: "win32",
				execPath: "C:\\node\\node.exe",
				npmExecPath: "C:\\node\\node_modules\\npm\\bin\\npm-cli.js",
				commandInterpreter: "C:\\Windows\\System32\\cmd.exe",
			}),
		).toEqual({
			command: "C:\\node\\node.exe",
			args: ["C:\\node\\node_modules\\npm\\bin\\npm-cli.js", "ci", "--omit=dev"],
		});
	});

	it("falls back to cmd.exe for a directly invoked Windows build script", () => {
		expect(
			npmInvocation(["ci"], {
				platform: "win32",
				npmExecPath: null,
				commandInterpreter: "C:\\Windows\\System32\\cmd.exe",
			}),
		).toEqual({
			command: "C:\\Windows\\System32\\cmd.exe",
			args: ["/d", "/s", "/c", "npm.cmd", "ci"],
		});
	});

	it("invokes npm directly on Unix when no parent npm CLI is available", () => {
		expect(npmInvocation(["ci"], { platform: "linux", npmExecPath: null })).toEqual({
			command: "npm",
			args: ["ci"],
		});
	});
});

describe("patchClaudeRetryDetails", () => {
	it("keeps Claude's retry delay in the published session failure", () => {
		const adapterPath = join(temporaryDirectory(), "acp-agent.js");
		writeFileSync(adapterPath, `
                            case "api_retry": {
                                const kind = message.error_status === null ? "transport_lost" : "x";
                                const title = "retrying";
                                await publishSessionFailure(kind, { title, severity: "warning" });
                                break;
                            }
                            case "model_refusal_fallback": {
`);

		expect(patchClaudeRetryDetails(adapterPath)).toBe(true);
		expect(patchClaudeRetryDetails(adapterPath)).toBe(false);
		const patched = readFileSync(adapterPath, "utf8");
		expect(patched).toContain("message.retry_delay_ms / 1000");
		expect(patched).toContain("Trying again in ${retryDelay}.");
		expect(patched).toContain("details: retryDetails");
	});
});

describe("patchClaudeHibernationCheck", () => {
	const anchors = [
		"// session.liveBackgroundTasks.set(taskId",
		"// session.lastSessionState = message.state;",
		"agent.endTask = (session, taskId) => { session.liveBackgroundTasks.delete(taskId); };",
		"// session.liveBackgroundTasks.delete(taskId);",
		"connection.onRequest(GOAL_CONTROL_METHOD, { parse: parseGoalRequest }, (ctx) => agent.goal(ctx.params));",
	];

	it("keeps live native tasks awake and allows sleep after they settle", () => {
		const adapterPath = join(temporaryDirectory(), "acp-agent.js");
		writeFileSync(adapterPath, anchors.join("\n"));
		expect(patchClaudeHibernationCheck(adapterPath)).toBe(true);
		expect(patchClaudeHibernationCheck(adapterPath)).toBe(false);
		const handlers = new Map();
		const connection = { onRequest: (method, parser, handler) => {
			expect(typeof parser.parse).toBe("function");
			expect(typeof handler).toBe("function");
			handlers.set(method, (ctx) => handler({ params: parser.parse(ctx.params) }));
			return connection;
		} };
		const tasks = new Map();
		const agent = { sessions: { native: { liveBackgroundTasks: tasks } } };
		new Function("connection", "agent", "GOAL_CONTROL_METHOD", "parseGoalRequest", "RequestError", readFileSync(adapterPath, "utf8"))(
			connection, agent, "goal", params => params, { invalidParams: () => new Error("invalid session") },
		);
		const check = handlers.get("_ao/session/can_hibernate");
		const ctx = { params: { sessionId: "native" } };
		expect(check(ctx)).toEqual({ canHibernate: true });
		tasks.set("server", { isSubagent: false });
		expect(check(ctx)).toEqual({ canHibernate: false });
		tasks.delete("server");
		expect(check(ctx)).toEqual({ canHibernate: true });
		tasks.set("ended", { isSubagent: true, endedPerLevel: "ended" });
		expect(check(ctx)).toEqual({ canHibernate: true });
		// A finished task's notification starts a cycle AO has no turn for.
		agent.sessions.native.lastSessionState = "running";
		expect(check(ctx)).toEqual({ canHibernate: false });
		agent.sessions.native.lastSessionState = "requires_action";
		expect(check(ctx)).toEqual({ canHibernate: false });
		agent.sessions.native.lastSessionState = "idle";
		expect(check(ctx)).toEqual({ canHibernate: true });
		// The notification cycle is reported as running a moment after the task leaves.
		tasks.set("shell", { isSubagent: false });
		agent.endTask(agent.sessions.native, "shell");
		expect(check(ctx)).toEqual({ canHibernate: false });
		agent.sessions.native.aoTaskEndedAt -= 10000;
		expect(check(ctx)).toEqual({ canHibernate: true });
		for (const sessionId of [undefined, "missing", "__proto__"]) {
			expect(() => check({ params: { sessionId } })).toThrow("invalid session");
		}
		agent.sessions.native.queryClosed = true;
		expect(() => check(ctx)).toThrow("invalid session");
	});

	it("fails packaging if the pinned native task registry changes", () => {
		const adapterPath = join(temporaryDirectory(), "acp-agent.js");
		for (const missing of anchors.keys()) {
			writeFileSync(adapterPath, anchors.filter((_, i) => i !== missing).join("\n"));
			expect(() => patchClaudeHibernationCheck(adapterPath)).toThrow("native-task hibernation check");
		}
	});
});

describe("patchClaudeContextUsage", () => {
	it("publishes the SDK context snapshot through ACP after a result", async () => {
		const adapterPath = join(temporaryDirectory(), "acp-agent.js");
		writeFileSync(adapterPath, `
                            // Send usage_update notification
                            if (lastAssistantTotalUsage !== null) {
                                await sendUpdate({
                                    update: {
                                        used: lastAssistantTotalUsage,
                                        size: session.contextWindowSize,
                                    },
                                });
                            }
                            if (session.cancelled) {
`);

		expect(patchClaudeContextUsage(adapterPath)).toBe(true);
		expect(patchClaudeContextUsage(adapterPath)).toBe(false);
		const patched = readFileSync(adapterPath, "utf8");
		// The default detail is "full", which makes token-count API calls per category.
		expect(patched).toContain("session.query.getContextUsage({ detail: 'summary' })");
		expect(patched).toContain("lastAssistantTotalUsage = contextUsage.totalTokens");
		expect(patched).toContain("session.contextWindowSize = contextUsage.rawMaxTokens");

		const start = patched.indexOf("// AO: use the SDK's context snapshot.");
		const end = patched.indexOf("if (session.cancelled) {", start);
		const AsyncFunction = Object.getPrototypeOf(async function () {}).constructor;
		const run = new AsyncFunction("session", "sendUpdate", `
			let lastAssistantTotalUsage = 9;
			${patched.slice(start, end)}
		`);
		const updates = [];
		const session = {
			contextWindowSize: 100,
			contextWindowAuthoritative: false,
			query: { getContextUsage: async () => ({ totalTokens: 17, rawMaxTokens: 200 }) },
		};
		await run.call({ logger: { error: () => {} } }, session, (notification) => {
			updates.push(notification.update);
		});
		expect(updates.map(({ used, size }) => [used, size])).toEqual([[17, 200]]);
		expect(session.contextWindowAuthoritative).toBe(true);

		const fallback = {
			contextWindowSize: 100,
			contextWindowAuthoritative: false,
			query: { getContextUsage: async () => { throw new Error("unavailable"); } },
		};
		const fallbackUpdates = [];
		await run.call({ logger: { error: () => {} } }, fallback, (notification) => {
			fallbackUpdates.push(notification.update);
		});
		expect(fallbackUpdates.map(({ used, size }) => [used, size])).toEqual([[9, 100]]);
		expect(fallback.contextWindowAuthoritative).toBe(false);

		const zero = {
			contextWindowSize: 100,
			contextWindowAuthoritative: false,
			query: { getContextUsage: async () => ({ totalTokens: 0, rawMaxTokens: 200 }) },
		};
		const zeroUpdates = [];
		await run.call({ logger: { error: () => {} } }, zero, (notification) => {
			zeroUpdates.push(notification.update);
		});
		expect(zeroUpdates.map(({ used, size }) => [used, size])).toEqual([[9, 100]]);
		expect(zero.contextWindowAuthoritative).toBe(false);
	});
});

// AO's adapter patches are written against exactly this release. Bumping the pin
// must be a deliberate act: re-check each patchClaude* function against the new
// dist (the build throws if an anchor moved, and so does the real-dist test below
// when the runtime is installed), drop any patch upstream now ships, then update
// the version here.
describe("pinned claude-agent-acp release", () => {
	const root = join(dirname(fileURLToPath(import.meta.url)), "..", "acp-runtime");

	it("is the exact version the patches were written against", () => {
		const manifest = JSON.parse(readFileSync(join(root, "package.json"), "utf8"));
		const lock = JSON.parse(readFileSync(join(root, "package-lock.json"), "utf8"));
		expect(manifest.dependencies["@agentclientprotocol/claude-agent-acp"]).toBe("0.99.0");
		expect(lock.packages["node_modules/@agentclientprotocol/claude-agent-acp"].version).toBe("0.99.0");
	});

	it("takes every patch when the runtime is installed", () => {
		const installed = join(root, "node_modules", "@agentclientprotocol", "claude-agent-acp", "dist", "acp-agent.js");
		if (!existsSync(installed)) return;
		const copy = join(temporaryDirectory(), "acp-agent.js");
		writeFileSync(copy, readFileSync(installed));
		for (const patch of [patchClaudeRetryDetails, patchClaudeContextUsage, patchClaudeHibernationCheck, patchClaudeAutoCompactWindow, patchClaudeContextBreakdown]) {
			expect(patch(copy)).toBe(true);
		}
	});
});

describe("pruneNodeDistribution", () => {
	it("removes Unix package-manager links before deleting their targets", () => {
		const nodeRoot = temporaryDirectory();
		const bin = join(nodeRoot, "bin");
		const npmBin = join(nodeRoot, "lib", "node_modules", "npm", "bin");
		mkdirSync(bin, { recursive: true });
		mkdirSync(npmBin, { recursive: true });
		writeFileSync(join(bin, "node"), "node");
		writeFileSync(join(npmBin, "npm-cli.js"), "npm");
		writeFileSync(join(npmBin, "npx-cli.js"), "npx");
		symlinkSync("../lib/node_modules/npm/bin/npm-cli.js", join(bin, "npm"));
		symlinkSync("../lib/node_modules/npm/bin/npx-cli.js", join(bin, "npx"));
		symlinkSync("../lib/node_modules/corepack/dist/corepack.js", join(bin, "corepack"));

		pruneNodeDistribution(nodeRoot);

		expect(readdirSync(bin)).toEqual(["node"]);
		expect(existsSync(join(nodeRoot, "lib"))).toBe(false);
	});

	it("removes package-manager files and modules from a Windows distribution", () => {
		const nodeRoot = temporaryDirectory();
		writeFileSync(join(nodeRoot, "node.exe"), "node");
		writeFileSync(join(nodeRoot, "LICENSE"), "license");
		for (const name of ["corepack", "corepack.cmd", "npm", "npm.cmd", "npx", "npx.cmd"]) {
			writeFileSync(join(nodeRoot, name), name);
		}
		mkdirSync(join(nodeRoot, "node_modules", "npm"), { recursive: true });

		pruneNodeDistribution(nodeRoot);

		expect(readdirSync(nodeRoot).sort()).toEqual(["LICENSE", "node.exe"]);
	});
});

describe("patchClaudeAutoCompactWindow", () => {
	const fixture = [
		"agent.start = function (sessionId, env, configuredSettings) {",
		"        const resolvedProvider = undefined;",
		"        const settings = resolvedProvider",
		"            ? configuredSettings : configuredSettings;",
		"        const providerCacheKey = providerCacheKeyFor(env);",
		"        const options = {",
		"            systemPrompt,",
		'            settingSources: ["user"],',
		"            settings, env };",
		"        return options;",
		"};",
		"connection",
		"        .onRequest(GOAL_CONTROL_METHOD, { parse: parseGoalRequest }, (ctx) => agent.goal(ctx.params))",
	].join("\n");

	// A Claude Code stand-in: it reports a window (raw follows the configured one, as
	// the real CLI does) and either follows the flag setting or, like 2.1.312, accepts
	// it and keeps its launch window.
	function cli({ honors, launch = 400000, fallback = 1000000 }) {
		let window = launch;
		return {
			calls: [],
			getContextUsage: async (options) => {
				expect(options).toEqual({ detail: "summary" });
				return { maxTokens: window, rawMaxTokens: window };
			},
			applyFlagSettings: async function (settings) {
				this.calls.push(settings);
				if (honors) window = settings.autoCompactWindow === null ? fallback : settings.autoCompactWindow;
			},
		};
	}

	function load(query = cli({ honors: true })) {
		const adapterPath = join(temporaryDirectory(), "acp-agent.js");
		writeFileSync(adapterPath, fixture);
		expect(patchClaudeAutoCompactWindow(adapterPath)).toBe(true);
		expect(patchClaudeAutoCompactWindow(adapterPath)).toBe(false);
		const handlers = new Map();
		const connection = { onRequest: (method, parser, handler) => {
			handlers.set(method, (params) => handler({ params: parser.parse(params) }));
			return connection;
		} };
		const agent = { sessions: { live: { queryClosed: false, query } } };
		new Function("connection", "agent", "GOAL_CONTROL_METHOD", "parseGoalRequest", "providerCacheKeyFor", "systemPrompt", "RequestError", readFileSync(adapterPath, "utf8"))(
			connection, agent, "goal", (p) => p, () => "key", "sp", { invalidParams: (_, message) => new Error(message) },
		);
		return { agent, calls: query.calls, set: handlers.get("_ao/session/set_auto_compact_window") };
	}

	it("moves the launch window out of the environment so it can change live", async () => {
		const { agent, calls, set } = load();
		const env = { CLAUDE_CODE_AUTO_COMPACT_WINDOW: "400000", KEEP: "1" };
		const options = agent.start("live", env, undefined);
		expect(options.env).toEqual({ KEEP: "1" });
		expect(options.settings).toEqual({ autoCompactWindow: 400000 });
		await expect(set({ sessionId: "live", tokens: 300000 })).resolves.toEqual({ applied: true, tokens: 300000 });
		await expect(set({ sessionId: "live", tokens: null })).resolves.toEqual({ applied: true, tokens: null });
		expect(calls).toEqual([{ autoCompactWindow: 300000 }, { autoCompactWindow: null }]);
		expect(agent.aoCompactWindows.get("live").tokens).toBeNull();
	});

	it("answers applied:false when Claude Code accepts the flag but keeps its launch window", async () => {
		const { agent, calls, set } = load(cli({ honors: false }));
		agent.start("live", { CLAUDE_CODE_AUTO_COMPACT_WINDOW: "400000" }, undefined);
		await expect(set({ sessionId: "live", tokens: 300000 })).resolves.toEqual({ applied: false, tokens: 400000 });
		await expect(set({ sessionId: "live", tokens: null })).resolves.toEqual({ applied: false, tokens: 400000 });
		expect(calls).toEqual([{ autoCompactWindow: 300000 }, { autoCompactWindow: null }]);
		expect(agent.aoCompactWindows.get("live").tokens).toBe(400000);
	});

	it("counts a reset as applied for a session that launched without a window (reattached Auto chat)", async () => {
		const { agent, calls, set } = load(cli({ honors: false, launch: 1000000 }));
		agent.start("live", {}, undefined);
		await expect(set({ sessionId: "live", tokens: null })).resolves.toEqual({ applied: true, tokens: null });
		expect(calls).toEqual([{ autoCompactWindow: null }]);
	});

	it("does not count a reset as applied when the CLI keeps a custom launch window (raw follows it too)", async () => {
		const { agent, set } = load(cli({ honors: false, launch: 400000 }));
		agent.start("live", { CLAUDE_CODE_AUTO_COMPACT_WINDOW: "400000" }, undefined);
		await expect(set({ sessionId: "live", tokens: null })).resolves.toEqual({ applied: false, tokens: 400000 });
	});

	it("counts a target the CLI already uses as applied, and only an exact match", async () => {
		const { agent, set } = load(cli({ honors: false, launch: 400000 }));
		agent.start("live", { CLAUDE_CODE_AUTO_COMPACT_WINDOW: "400000" }, undefined);
		await expect(set({ sessionId: "live", tokens: 400000 })).resolves.toEqual({ applied: true, tokens: 400000 });
		await expect(set({ sessionId: "live", tokens: 900000 })).resolves.toMatchObject({ applied: false });
	});

	it("keeps existing settings and applies live when the session launched on Auto", async () => {
		const { agent, calls, set } = load();
		const options = agent.start("live", {}, { modelOverrides: { a: "b" } });
		expect(options.settings).toEqual({ modelOverrides: { a: "b" } });
		await expect(set({ sessionId: "live", tokens: 200000 })).resolves.toEqual({ applied: true, tokens: 200000 });
		expect(calls).toHaveLength(1);
		expect(agent.start("live", { CLAUDE_CODE_AUTO_COMPACT_WINDOW: "250000" }, { modelOverrides: { a: "b" } }).settings)
			.toEqual({ modelOverrides: { a: "b" }, autoCompactWindow: 250000 });
	});

	it("reports applied:false when the environment value could not move into settings", async () => {
		const { agent, calls, set } = load();
		const options = agent.start("live", { CLAUDE_CODE_AUTO_COMPACT_WINDOW: "400000" }, "/path/settings.json");
		expect(options.env.CLAUDE_CODE_AUTO_COMPACT_WINDOW).toBe("400000");
		await expect(set({ sessionId: "live", tokens: 300000 })).resolves.toEqual({ applied: false, tokens: null });
		expect(calls).toEqual([]);
	});

	it("rejects an unknown or closed session and an out-of-range window", async () => {
		const { agent, set } = load();
		agent.start("live", {}, undefined);
		for (const params of [{ sessionId: "missing", tokens: 200000 }, { sessionId: "__proto__", tokens: 200000 }, {}]) {
			await expect(set(params)).rejects.toThrow("Live Claude session unavailable");
		}
		for (const tokens of [99999, 1000001, 2.5, "200000", undefined]) {
			await expect(set({ sessionId: "live", tokens })).rejects.toThrow("tokens must be null or an integer");
		}
		agent.sessions.live.queryClosed = true;
		await expect(set({ sessionId: "live", tokens: 200000 })).rejects.toThrow("Live Claude session unavailable");
	});

	it("fails packaging if the pinned adapter's session setup changes", () => {
		const adapterPath = join(temporaryDirectory(), "acp-agent.js");
		const lines = fixture.split("\n");
		for (const anchor of ["const settings = resolvedProvider", "providerCacheKeyFor", "const options", ".onRequest(GOAL"]) {
			writeFileSync(adapterPath, lines.filter((line) => !line.includes(anchor)).join("\n"));
			expect(() => patchClaudeAutoCompactWindow(adapterPath)).toThrow("auto-compact window patch");
		}
	});
});

describe("patchClaudeContextBreakdown", () => {
	const fixture = "connection\n        .onRequest(GOAL_CONTROL_METHOD, { parse: parseGoalRequest }, (ctx) => agent.goal(ctx.params))";

	function load(query) {
		const adapterPath = join(temporaryDirectory(), "acp-agent.js");
		writeFileSync(adapterPath, fixture);
		expect(patchClaudeContextBreakdown(adapterPath)).toBe(true);
		expect(patchClaudeContextBreakdown(adapterPath)).toBe(false);
		const handlers = new Map();
		const connection = { onRequest: (method, parser, handler) => {
			handlers.set(method, (params) => handler({ params: parser.parse(params) }));
			return connection;
		} };
		const agent = { sessions: { live: { queryClosed: false, query } } };
		new Function("connection", "agent", "GOAL_CONTROL_METHOD", "parseGoalRequest", "RequestError", readFileSync(adapterPath, "utf8"))(
			connection, agent, "goal", (p) => p, { invalidParams: (_, message) => new Error(message) },
		);
		return { agent, call: handlers.get("_ao/session/context_usage") };
	}

	it("asks for the full breakdown on request and drops the display grid", async () => {
		const asked = [];
		const { call } = load({ getContextUsage: async (options) => {
			asked.push(options);
			return { categories: [{ name: "Messages", tokens: 5 }], totalTokens: 5, maxTokens: 100, gridRows: [[1, 2, 3]] };
		} });
		const usage = await call({ sessionId: "live" });
		expect(asked).toEqual([{ detail: "full" }]);
		expect(usage).toEqual({ categories: [{ name: "Messages", tokens: 5 }], totalTokens: 5, maxTokens: 100 });
	});

	it("rejects an unknown or closed session", async () => {
		const { agent, call } = load({ getContextUsage: async () => ({}) });
		for (const params of [{ sessionId: "missing" }, { sessionId: "__proto__" }, {}]) {
			await expect(call(params)).rejects.toThrow("Live Claude session unavailable");
		}
		agent.sessions.live.queryClosed = true;
		await expect(call({ sessionId: "live" })).rejects.toThrow("Live Claude session unavailable");
	});

	it("fails packaging if the request registration point moves", () => {
		const adapterPath = join(temporaryDirectory(), "acp-agent.js");
		writeFileSync(adapterPath, "connection");
		expect(() => patchClaudeContextBreakdown(adapterPath)).toThrow("context breakdown patch");
	});
});

function temporaryDirectory() {
	const directory = mkdtempSync(join(tmpdir(), "ao-acp-runtime-test-"));
	temporaryDirectories.push(directory);
	return directory;
}

describe("archiveExtraction", () => {
	// Extraction must not go through PowerShell's Expand-Archive: it is bound by
	// MAX_PATH, and with LongPathsEnabled=0 a deep checkout pushes Node's bundled
	// npm tree past 260 characters, where it fails while still exiting zero.
	it("uses bsdtar for the Windows zip", () => {
		expect(archiveExtraction("C:\\w\\node.zip", "C:\\w", {
			platform: "win32",
			systemRoot: "C:\\Windows",
		})).toEqual({
			command: "C:\\Windows\\System32\\tar.exe",
			args: ["-xf", "C:\\w\\node.zip", "-C", "C:\\w"],
		});
	});

	it("keeps gzip handling on the other platforms", () => {
		for (const platform of ["darwin", "linux"]) {
			expect(archiveExtraction("/w/node.tar.gz", "/w", { platform })).toEqual({
				command: "tar",
				args: ["-xzf", "/w/node.tar.gz", "-C", "/w"],
			});
		}
	});

	it("never shells out to a command interpreter", () => {
		for (const platform of ["win32", "darwin", "linux"]) {
			const { command } = archiveExtraction("/w/a", "/w", { platform, systemRoot: "C:\\Windows" });
			expect(command).not.toMatch(/powershell|cmd\.exe|\bsh\b/i);
		}
	});
});
