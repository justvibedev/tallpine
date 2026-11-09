/**
 * Packaged builds refuse Chromium/Node debugging switches. A same-user app
 * that relaunched Tallpine with --remote-debugging-port could drive the
 * renderer over the DevTools protocol, and main injects the daemon token into
 * renderer requests. No Electron fuse covers the Chromium switches (the
 * EnableNodeCliInspectArguments fuse only covers Node's --inspect), so the
 * check is ours. It runs as the first import of app-bootstrap.ts (main.ts's first import); Chromium has already
 * parsed argv by then, so the process exits before any window or daemon work.
 */

// Chromium accepts -switch and --switch (and /switch on Windows), with =value.
const REFUSED_SWITCHES = new Set([
	"remote-debugging-port",
	"remote-debugging-pipe",
	"remote-debugging-address",
	"remote-allow-origins",
	"inspect",
	"inspect-brk",
	"inspect-port",
	"inspect-publish-uid",
	"debug-port",
]);

// Electron appends these to the command line before Chromium parses it.
const EXTRA_ARGS_ENV = ["ELECTRON_EXTRA_LAUNCH_ARGS"];

function switchName(arg: string): string | null {
	const match = /^(?:--?|\/)([^=\s]+)/.exec(arg.trim());
	return match ? match[1].toLowerCase() : null;
}

/** The refused switch found in argv or the extra-args env, if any. */
export function findRefusedDebugSwitch(argv: readonly string[], env: NodeJS.ProcessEnv = {}): string | null {
	const args = [...argv];
	for (const name of EXTRA_ARGS_ENV) {
		const value = env[name];
		if (value) args.push(...value.split(/\s+/));
	}
	for (const arg of args) {
		const name = switchName(arg);
		if (name && REFUSED_SWITCHES.has(name)) return `--${name}`;
	}
	return null;
}

/**
 * Packaged builds exit when a refused switch is present. `app` is also asked
 * (hasSwitch) in case Electron folded a switch in that is not in argv.
 */
export function enforceDebugSwitchPolicy(deps: {
	isPackaged: boolean;
	argv: readonly string[];
	env: NodeJS.ProcessEnv;
	hasSwitch: (name: string) => boolean;
	exit: (code: number) => void;
	log: (message: string) => void;
}): boolean {
	if (!deps.isPackaged) return false;
	let refused = findRefusedDebugSwitch(deps.argv, deps.env);
	if (!refused) {
		const hit = [...REFUSED_SWITCHES].find((name) => deps.hasSwitch(name));
		if (hit) refused = `--${hit}`;
	}
	if (!refused) return false;
	deps.log(`Tallpine refuses ${refused} in packaged builds.`);
	deps.exit(1);
	return true;
}
