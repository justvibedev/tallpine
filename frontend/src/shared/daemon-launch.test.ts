import { describe, expect, it } from "vitest";
import { bundledDaemonIdentityError, resolveDaemonLaunch } from "./daemon-launch";

describe("resolveDaemonLaunch", () => {
	it("uses the daemon command override in dev, without a shell", () => {
		expect(
			resolveDaemonLaunch({ TALLPINE_DAEMON_COMMAND: "/tmp/tallpine daemon" }, false, "/resources", "/app", "/home/user", "darwin"),
		).toEqual({ command: "/tmp/tallpine", args: ["daemon"], cwd: "/app", shell: false, source: "configured" });
	});

	it("ignores the daemon command override in packaged builds", () => {
		const spec = resolveDaemonLaunch(
			{ TALLPINE_DAEMON_COMMAND: "/bin/sh -c evil" },
			true,
			"/resources",
			"/app",
			"/home/user",
			"darwin",
		);
		expect(spec?.source).toBe("bundled");
		expect(spec?.command).toBe("/resources/daemon/tallpine");
		expect(spec?.shell).toBe(false);
	});

	it("uses the prebuilt daemon binary in non-Windows dev", () => {
		expect(resolveDaemonLaunch({}, false, "/resources", "/repo/frontend", "/home/user", "darwin")).toEqual({
			command: "/repo/frontend/daemon/tallpine",
			args: ["daemon"],
			cwd: "/repo/frontend",
			shell: false,
			source: "dev",
		});
	});

	it("uses the prebuilt daemon exe in Windows dev", () => {
		expect(resolveDaemonLaunch({}, false, "/resources", "C:\\repo\\frontend", "C:\\Users\\alice", "win32")).toEqual({
			command: "C:\\repo\\frontend/daemon/tallpine.exe",
			args: ["daemon"],
			cwd: "C:\\repo\\frontend",
			shell: false,
			source: "dev",
		});
	});

	it("uses the versioned daemon exe in Windows dev when build-daemon wrote one", () => {
		expect(
			resolveDaemonLaunch(
				{ TALLPINE_DEV_DAEMON_BINARY: "C:\\repo\\frontend\\daemon\\dev-123\\tallpine.exe" },
				false,
				"/resources",
				"C:\\repo\\frontend",
				"C:\\Users\\alice",
				"win32",
			),
		).toEqual({
			command: "C:\\repo\\frontend\\daemon\\dev-123\\tallpine.exe",
			args: ["daemon"],
			cwd: "C:\\repo\\frontend",
			shell: false,
			source: "dev",
		});
	});

	it("uses the bundled daemon binary for packaged macOS/Linux builds", () => {
		expect(
			resolveDaemonLaunch(
				{},
				true,
				"/Applications/Tallpine.app/Contents/Resources",
				"/app",
				"/Users/alice",
				"darwin",
			),
		).toEqual({
			command: "/Applications/Tallpine.app/Contents/Resources/daemon/tallpine",
			args: ["daemon"],
			cwd: "/Users/alice/.tallpine",
			shell: false,
			source: "bundled",
		});
	});

	it("uses the bundled daemon exe for packaged Windows builds", () => {
		expect(
			resolveDaemonLaunch(
				{},
				true,
				"C:\\Program Files\\Tallpine\\resources",
				"C:\\Program Files\\Tallpine\\resources\\app.asar",
				"C:\\Users\\alice",
				"win32",
			),
		).toEqual({
			command: "C:\\Program Files\\Tallpine\\resources/daemon/tallpine.exe",
			args: ["daemon"],
			cwd: "C:\\Users\\alice/.tallpine",
			shell: false,
			source: "bundled",
		});
	});
});

describe("bundledDaemonIdentityError", () => {
	const samePath = (a: string, b: string): boolean => a === b;
	const appImage = "/home/user/Apps/tallpine.AppImage";
	// The bundled command under AppImage: a random FUSE mount, different per launch.
	const launchCommand = "/tmp/.mount_agent-mDQfUL/resources/daemon/tallpine";

	it("accepts the same install across two AppImage mounts (relaunch-to-update)", () => {
		const probe = {
			executablePath: "/tmp/.mount_agent-1Qs4N6/resources/daemon/tallpine",
			appImagePath: appImage,
		};
		expect(bundledDaemonIdentityError(probe, launchCommand, appImage, samePath)).toBeNull();
	});

	it("rejects a daemon from a different AppImage install", () => {
		const other = "/home/user/Apps/tallpine-nightly.AppImage";
		const probe = { executablePath: "/tmp/.mount_agent-1Qs4N6/resources/daemon/tallpine", appImagePath: other };
		expect(bundledDaemonIdentityError(probe, launchCommand, appImage, samePath)).toBe(
			`Another Tallpine daemon is already running from ${other}; expected ${appImage}. Stop the other daemon before using this app.`,
		);
	});

	it("fails closed under AppImage when the daemon does not report its install identity", () => {
		const probe = { executablePath: "/tmp/.mount_agent-1Qs4N6/resources/daemon/tallpine" };
		expect(bundledDaemonIdentityError(probe, launchCommand, appImage, samePath)).toBe(
			"An older Tallpine daemon is already running, but it does not report its install identity. Stop it and restart this app.",
		);
	});

	it("compares executable paths outside AppImage", () => {
		const command = "/opt/Tallpine/resources/daemon/tallpine";
		expect(bundledDaemonIdentityError({ executablePath: command }, command, undefined, samePath)).toBeNull();
		expect(bundledDaemonIdentityError({ executablePath: "/other/tallpine" }, command, undefined, samePath)).toBe(
			`Another Tallpine daemon is already running from /other/tallpine; expected ${command}. Stop the other daemon before using this app.`,
		);
	});

	it("fails closed outside AppImage when the daemon does not report its binary path", () => {
		expect(bundledDaemonIdentityError({}, "/opt/tallpine/resources/daemon/tallpine", undefined, samePath)).toBe(
			"An older Tallpine daemon is already running, but it does not report its binary path. Stop it and restart this app.",
		);
	});
});
