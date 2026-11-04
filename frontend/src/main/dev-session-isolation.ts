import { createHash } from "node:crypto";
import path from "node:path";
import { DEFAULT_DAEMON_PORT, ENV_PREFIX, HOME_DIR_NAME } from "../shared/brand";

/** Set to "1" to let a dev build started inside an agent session use the inherited real state. */
export const DEV_REAL_DATA_ENV = "TALLPINE_DEV_REAL_DATA";

/** A variable in the product's namespace. */
const isProductEnv = (name: string): boolean => name.startsWith(ENV_PREFIX);
/** Dev knobs and the devtools switch survive isolation. */
const isDevKnob = (name: string): boolean => name.startsWith(`${ENV_PREFIX}DEV_`) || name === `${ENV_PREFIX}OPEN_DEVTOOLS`;

const AGENT_SESSION_ENV = ["TALLPINE_SESSION_ID", "AO_SESSION_ID"];
const DEV_PORT_BASE = 3200;
const DEV_PORT_SPAN = 500;

export interface DevSessionIsolation {
	sessionId: string;
	stateDir: string;
	dataDir: string;
	runFile: string;
	port: number;
	electronDir: string;
	dropped: string[];
}

function within(dir: string, target: string): boolean {
	const rel = path.relative(dir, target);
	return rel === "" || (!rel.startsWith("..") && !path.isAbsolute(rel));
}

/**
 * An agent session's environment is the production app's daemon environment
 * plus session wiring: TALLPINE_DATA_DIR and TALLPINE_RUN_FILE point at the real ~/.tallpine,
 * TALLPINE_TMUX_SOCKET_NAME at the real tmux server, TALLPINE_SESSION_TOKEN at the real
 * daemon. A dev build (`npm run dev`) started there would inherit all of it and
 * run against production data. This drops that wiring and pins state, run
 * file, daemon port and Electron profile to a directory derived from the
 * checkout, so parallel worktrees also stop sharing one ~/.tallpine/dev and port 3102.
 *
 * Explicit scratch overrides survive: a TALLPINE_DATA_DIR or TALLPINE_RUN_FILE outside the
 * real state root (or under ~/.tallpine/dev), a TALLPINE_PORT other than the production
 * port, and every TALLPINE_DEV_* knob. Returns null outside a session or with
 * TALLPINE_DEV_REAL_DATA=1.
 *
 * A packaged app is the supervisor, never an agent, even when it was started
 * from a shell inside one: it only drops the session markers, so its daemon's
 * isolation check (config.CheckDaemonIsolation) does not mistake it for one.
 */
export function isolateDevSessionEnv(
	env: Record<string, string | undefined>,
	opts: { isPackaged: boolean; homeDir: string; checkoutDir: string },
): DevSessionIsolation | null {
	if (opts.isPackaged) {
		for (const name of AGENT_SESSION_ENV) delete env[name];
		return null;
	}
	if (env[DEV_REAL_DATA_ENV] === "1") return null;
	const sessionId = AGENT_SESSION_ENV.map((name) => env[name]?.trim()).find(Boolean);
	if (!sessionId) return null;

	const realRoot = path.join(opts.homeDir, HOME_DIR_NAME);
	const devRoot = path.join(realRoot, "dev");
	const scratchPath = (value: string | undefined): string | undefined => {
		const trimmed = value?.trim();
		if (!trimmed || !path.isAbsolute(trimmed)) return undefined;
		// An agent session's $TMPDIR lives under <root>/data/tmp; scratch state there is not real state.
		const scratchRoots = [devRoot, path.join(realRoot, "data", "tmp")];
		return within(realRoot, trimmed) && !scratchRoots.some((root) => within(root, trimmed)) ? undefined : trimmed;
	};
	const keptDataDir = scratchPath(env.TALLPINE_DATA_DIR);
	const keptRunFile = scratchPath(env.TALLPINE_RUN_FILE);
	const inheritedPort = Number(env.TALLPINE_PORT);
	const keptPort =
		Number.isInteger(inheritedPort) && inheritedPort > 0 && inheritedPort !== DEFAULT_DAEMON_PORT ? inheritedPort : undefined;

	const dropped: string[] = [];
	for (const name of Object.keys(env)) {
		if (name === "AO_SESSION_ID" || (isProductEnv(name) && !isDevKnob(name))) {
			dropped.push(name);
			delete env[name];
		}
	}

	const digest = createHash("sha256").update(path.resolve(opts.checkoutDir)).digest();
	const stateDir = path.join(devRoot, "checkouts", digest.toString("hex").slice(0, 12));
	const isolation: DevSessionIsolation = {
		sessionId,
		stateDir,
		dataDir: keptDataDir ?? path.join(stateDir, "data"),
		runFile: keptRunFile ?? path.join(stateDir, "running.json"),
		port: keptPort ?? DEV_PORT_BASE + (digest.readUInt32BE(0) % DEV_PORT_SPAN),
		electronDir: env.TALLPINE_DEV_ELECTRON_DIR?.trim() || path.join(stateDir, "electron"),
		dropped: dropped.sort(),
	};
	env.TALLPINE_DATA_DIR = isolation.dataDir;
	env.TALLPINE_RUN_FILE = isolation.runFile;
	env.TALLPINE_PORT = String(isolation.port);
	env.TALLPINE_DEV_ELECTRON_DIR = isolation.electronDir;
	return isolation;
}
