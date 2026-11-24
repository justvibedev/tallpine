/**
 * Quits one Chrome process the way Cmd+Q does. On macOS, Chrome exits on
 * SIGTERM without flushing its cookie store, which it commits only every 30
 * seconds. A sign-in made just before Done was then lost, and the agent's Chrome
 * came back signed out. A quit sent to that one process id writes the cookies
 * first. It never reaches another Chrome, such as the person's own.
 */
import { execFile } from "node:child_process";

// JXA: NSRunningApplication for exactly this pid; terminate asks it to quit.
const TERMINATE_PID = `ObjC.import("AppKit");
function run(argv) {
	const app = $.NSRunningApplication.runningApplicationWithProcessIdentifier(parseInt(argv[0], 10));
	if (!app || app.isNil()) return "missing";
	return app.terminate ? "sent" : "refused";
}`;

/** Asks the Chrome with pid to quit gracefully; false when the request could not be sent. */
export type QuitChromeProcess = (pid: number) => Promise<boolean>;

/** The platform's graceful quit: an app quit on macOS, none elsewhere (SIGTERM is graceful on Linux). */
export function defaultQuitChromeProcess(platform: NodeJS.Platform): QuitChromeProcess {
	if (platform !== "darwin") return async () => false;
	return (pid) =>
		new Promise((resolve) => {
			execFile("/usr/bin/osascript", ["-l", "JavaScript", "-e", TERMINATE_PID, String(pid)], { timeout: 5_000 }, (error, stdout) => {
				resolve(!error && String(stdout).trim() === "sent");
			});
		});
}
