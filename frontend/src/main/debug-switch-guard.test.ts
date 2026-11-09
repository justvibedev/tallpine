import { readFileSync } from "node:fs";
import path from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import { enforceDebugSwitchPolicy, findRefusedDebugSwitch } from "./debug-switch-guard";

describe("findRefusedDebugSwitch", () => {
	it.each([
		["--remote-debugging-port=9222"],
		["-remote-debugging-port=0"],
		["--Remote-Debugging-Pipe"],
		["--remote-allow-origins=*"],
		["--inspect=9229"],
		["--inspect-brk"],
	])("refuses %s", (arg) => {
		expect(findRefusedDebugSwitch(["/Applications/Tallpine", arg])).not.toBeNull();
	});

	it("ignores ordinary arguments", () => {
		expect(findRefusedDebugSwitch(["Tallpine", "--no-sandbox", "tallpine://open", "--remote-debugging-portal-x"])).toBeNull();
	});

	it("refuses a switch smuggled through ELECTRON_EXTRA_LAUNCH_ARGS", () => {
		expect(findRefusedDebugSwitch(["Tallpine"], { ELECTRON_EXTRA_LAUNCH_ARGS: "--foo --remote-debugging-port=1" })).toBe(
			"--remote-debugging-port",
		);
	});
});

describe("enforceDebugSwitchPolicy", () => {
	const base = () => ({
		isPackaged: true,
		argv: ["Tallpine", "--remote-debugging-port=9222"],
		env: {},
		hasSwitch: () => false,
		exit: vi.fn(),
		log: vi.fn(),
	});

	it("exits a packaged build", () => {
		const deps = base();
		expect(enforceDebugSwitchPolicy(deps)).toBe(true);
		expect(deps.exit).toHaveBeenCalledWith(1);
	});

	it("exits when only app.commandLine has the switch", () => {
		const deps = { ...base(), argv: ["Tallpine"], hasSwitch: (n: string) => n === "remote-debugging-pipe" };
		expect(enforceDebugSwitchPolicy(deps)).toBe(true);
		expect(deps.exit).toHaveBeenCalledWith(1);
	});

	it("keeps dev builds working", () => {
		const deps = { ...base(), isPackaged: false };
		expect(enforceDebugSwitchPolicy(deps)).toBe(false);
		expect(deps.exit).not.toHaveBeenCalled();
	});
});

describe("startup module on the packaged path", () => {
	afterEach(() => {
		vi.resetModules();
		vi.doUnmock("electron");
		vi.restoreAllMocks();
	});

	async function run(isPackaged: boolean, argv: string[]) {
		const appExit = vi.fn();
		vi.doMock("electron", () => ({
			app: { isPackaged, exit: appExit, commandLine: { hasSwitch: () => false } },
		}));
		const original = process.argv;
		process.argv = argv;
		const procExit = vi.spyOn(process, "exit").mockImplementation((() => undefined) as never);
		vi.spyOn(console, "error").mockImplementation(() => undefined);
		try {
			await import("./debug-switch-guard-startup");
		} finally {
			process.argv = original;
		}
		return { appExit, procExit };
	}

	it("quits a packaged app started with --remote-debugging-port", async () => {
		const { appExit, procExit } = await run(true, ["Tallpine", "--remote-debugging-port=9222"]);
		expect(appExit).toHaveBeenCalledWith(1);
		expect(procExit).toHaveBeenCalledWith(1);
	});

	it("lets a dev build run with the switch", async () => {
		const { appExit } = await run(false, ["Tallpine", "--remote-debugging-port=9222"]);
		expect(appExit).not.toHaveBeenCalled();
	});

	it("runs before anything else in app-bootstrap, the first module main.ts imports", () => {
		const main = readFileSync(path.join(__dirname, "..", "main.ts"), "utf8");
		expect(/^import\s+"([^"]+)"/m.exec(main)?.[1]).toBe("./main/app-bootstrap");
		const boot = readFileSync(path.join(__dirname, "app-bootstrap.ts"), "utf8");
		expect(/^import\s+"([^"]+)"/m.exec(boot)?.[1]).toBe("./debug-switch-guard-startup");
	});
});
