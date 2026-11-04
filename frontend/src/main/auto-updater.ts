import { mkdir, writeFile } from "node:fs/promises";
import path from "node:path";
import { feedConfig, feedConfigYaml } from "./update-feed";
import { planUpdates, type UpdateEnvironment, type UpdatePlan } from "./update-eligibility";
import { isManagedUpdatePolicy, type ManagedUpdatePolicy } from "./update-policy";
import { isQuietUpdateError } from "./update-errors";
import {
	readUpdateSettings,
	writeUpdateSettings,
	type UpdatePreferences,
	type UpdateSettings,
	type UpdateStatus,
} from "./update-settings";

/** First check this long after launch, so startup is not competing with it. */
export const FIRST_CHECK_DELAY_MS = 60_000;
export const CHECK_INTERVAL_MS = 6 * 60 * 60 * 1000;
/** How often a pending "restart when idle" re-checks the agents. */
export const IDLE_POLL_MS = 15_000;

/** The slice of electron-updater's AppUpdater this controller drives. */
export interface UpdaterLike {
	autoDownload: boolean;
	autoInstallOnAppQuit: boolean;
	allowPrerelease: boolean;
	allowDowngrade: boolean;
	disableDifferentialDownload: boolean;
	updateConfigPath: string | null;
	on(event: string, listener: (...args: any[]) => void): unknown;
	checkForUpdates(): Promise<unknown>;
	quitAndInstall(isSilent?: boolean, isForceRunAfter?: boolean): void;
}

export interface AutoUpdaterDeps {
	/** ~/.tallpine (or TALLPINE_DATA_DIR's state root). */
	stateDir: string;
	createUpdater: () => UpdaterLike;
	environment: () => Promise<UpdateEnvironment>;
	readPolicy: () => Promise<ManagedUpdatePolicy>;
	onStatus: (status: UpdateStatus) => void;
	/** Called right before Squirrel quits the app; main marks the quit as an update restart. */
	beforeInstall: () => void;
	/** Squirrel rejected the update after Restart was clicked; main clears the restart mark. */
	installFailed: () => void;
	/** True while any agent turn or review is running; an unreadable answer must count as busy. */
	agentsBusy?: () => Promise<boolean>;
	log?: (message: string, error?: unknown) => void;
	setTimeout?: (fn: () => void, ms: number) => unknown;
	clearTimeout?: (handle: unknown) => void;
	setInterval?: (fn: () => void, ms: number) => unknown;
	clearInterval?: (handle: unknown) => void;
}

export interface AutoUpdaterController {
	start(): Promise<void>;
	getStatus(): UpdateStatus;
	getPreferences(): Promise<UpdatePreferences>;
	setSettings(settings: UpdateSettings): Promise<void>;
	checkNow(): Promise<void>;
	install(): boolean;
	/** Restart as soon as no agent is busy. False when no update is staged. */
	installWhenIdle(): boolean;
	cancelInstallWhenIdle(): void;
	dispose(): void;
}

export function createAutoUpdater(deps: AutoUpdaterDeps): AutoUpdaterController {
	const set = deps.setTimeout ?? ((fn, ms) => setTimeout(fn, ms));
	const clear = deps.clearTimeout ?? ((h) => clearTimeout(h as NodeJS.Timeout));
	const every = deps.setInterval ?? ((fn, ms) => setInterval(fn, ms));
	const stopEvery = deps.clearInterval ?? ((h) => clearInterval(h as NodeJS.Timeout));
	const log = deps.log ?? ((message, error) => console.warn(`updater: ${message}`, error ?? ""));

	let status: UpdateStatus = { state: "idle" };
	let plan: UpdatePlan = { active: false, reason: "unpackaged" };
	let feedKey = "";
	let updater: UpdaterLike | null = null;
	let firstTimer: unknown = null;
	let interval: unknown = null;
	let env: UpdateEnvironment | null = null;
	let idleTimer: unknown = null;
	let idleChecking = false;

	const emit = (next: UpdateStatus) => {
		status = next;
		deps.onStatus(status);
	};

	const stopIdleWatch = () => {
		if (idleTimer !== null) stopEvery(idleTimer);
		idleTimer = null;
	};

	const clearIdleWatch = () => {
		stopIdleWatch();
		if (status.restartWhenIdle) {
			const { restartWhenIdle: _pending, ...rest } = status;
			emit(rest);
		}
	};

	const idleTick = async () => {
		if (idleChecking || !status.restartWhenIdle) return;
		idleChecking = true;
		try {
			// This can fire hours after the click: re-read settings and managed
			// policy first, so an MDM change since then (updates off, pinned
			// version) is honoured. Turning updates off drops the staged update.
			await apply();
			if (!status.restartWhenIdle || status.state !== "downloaded" || !plan.active) return;
			const busy = await (deps.agentsBusy?.() ?? Promise.resolve(true)).catch(() => true);
			// Cancelled, disabled or installed while the check was in flight.
			if (busy || !status.restartWhenIdle || status.state !== "downloaded" || !plan.active) return;
			controller.install();
		} catch (error) {
			log("restart when idle check failed", error);
		} finally {
			idleChecking = false;
		}
	};

	const stopSchedule = () => {
		if (firstTimer !== null) clear(firstTimer);
		if (interval !== null) stopEvery(interval);
		firstTimer = interval = null;
	};

	const ensureUpdater = (): UpdaterLike => {
		if (updater) return updater;
		const u = deps.createUpdater();
		u.autoDownload = true;
		// Never hand a download to Squirrel ahead of time: once ShipIt has staged
		// it, it installs at the next quit whatever this process decides later.
		// With this off, MacUpdater feeds Squirrel only inside quitAndInstall(),
		// so turning updates off really drops a downloaded update.
		u.autoInstallOnAppQuit = false;
		u.allowDowngrade = false;
		// Upstream saw corrupt Squirrel installs from macOS blockmap patches
		// (#3034); full zips until differential is proven on this feed.
		u.disableDifferentialDownload = true;
		u.on("checking-for-update", () => {
			if (status.state !== "downloaded" && status.state !== "installing") emit({ ...status, state: "checking", message: undefined });
		});
		u.on("update-not-available", () => emit({ state: "idle", checkedAt: Date.now() }));
		u.on("update-available", (info: { version?: string }) => emit({ state: "downloading", version: info?.version, checkedAt: Date.now() }));
		u.on("download-progress", (p: { percent?: number }) => {
			if (status.state === "downloading") emit({ ...status, percent: Math.round(p?.percent ?? 0) });
		});
		u.on("update-downloaded", (info: { version?: string }) => {
			// Turned off while downloading: Squirrel never sees it; do not offer it.
			if (!plan.active) return;
			emit({ state: "downloaded", version: info?.version ?? status.version, checkedAt: status.checkedAt });
			// Nothing newer to fetch until this one is installed.
			stopSchedule();
		});
		u.on("error", (error: unknown) => onError(error));
		updater = u;
		return u;
	};

	const onError = (error: unknown) => {
		if (status.state === "installing") {
			// Squirrel fetches and validates the zip only now (signature, ShipIt):
			// show why, drop the restart mark, and let the schedule fetch it again.
			log("install failed", error);
			deps.installFailed();
			const message = error instanceof Error ? error.message : String(error);
			emit({ state: "error", message: message.split("\n")[0]!.slice(0, 200), ...(status.checkedAt ? { checkedAt: status.checkedAt } : {}) });
			if (plan.active) startSchedule();
			return;
		}
		if (status.state === "downloaded") return;
		if (isQuietUpdateError(error)) {
			// No feed yet, offline, rate limited: nothing the user can act on.
			emit({ state: "idle", ...(status.checkedAt ? { checkedAt: status.checkedAt } : {}) });
			return;
		}
		log("update failed", error);
		const message = error instanceof Error ? error.message : String(error);
		emit({ state: "error", message: message.split("\n")[0]!.slice(0, 200), ...(status.checkedAt ? { checkedAt: status.checkedAt } : {}) });
	};

	const check = async () => {
		if (!plan.active || !updater || status.state === "downloaded" || status.state === "downloading" || status.state === "installing") return;
		try {
			await updater.checkForUpdates();
		} catch (error) {
			onError(error);
		}
	};

	const configure = async (active: Extract<UpdatePlan, { active: true }>) => {
		const u = ensureUpdater();
		u.allowPrerelease = active.channel === "beta";
		const key = JSON.stringify(feedConfig(active));
		if (key === feedKey) return;
		const dir = path.join(deps.stateDir, "updater");
		await mkdir(dir, { recursive: true, mode: 0o750 });
		const file = path.join(dir, "app-update.yml");
		await writeFile(file, feedConfigYaml(feedConfig(active)), { mode: 0o600 });
		// Resets electron-updater's provider so the next check reads this feed.
		u.updateConfigPath = file;
		feedKey = key;
	};

	const apply = async () => {
		env ??= await deps.environment();
		const [settings, policy] = await Promise.all([readUpdateSettings(deps.stateDir), deps.readPolicy()]);
		const wasActive = plan.active;
		plan = planUpdates(env, settings, policy);
		if (!plan.active) {
			stopSchedule();
			stopIdleWatch();
			// A downloaded update was never handed to Squirrel (autoInstallOnAppQuit
			// is off), so dropping the "downloaded" status drops the update.
			emit({ state: "disabled", reason: plan.reason });
			return;
		}
		await configure(plan);
		if (status.state === "disabled") emit({ state: "idle" });
		if (!wasActive) startSchedule();
	};

	function startSchedule() {
		stopSchedule();
		firstTimer = set(() => {
			firstTimer = null;
			void check();
		}, FIRST_CHECK_DELAY_MS);
		interval = every(() => void check(), CHECK_INTERVAL_MS);
	}

	const controller: AutoUpdaterController = {
		start: () => apply().catch((error) => log("could not start", error)),
		getStatus: () => status,
		async getPreferences() {
			const [settings, policy] = await Promise.all([readUpdateSettings(deps.stateDir), deps.readPolicy()]);
			const locked = isManagedUpdatePolicy(policy);
			const effective: UpdateSettings = {
				enabled: policy.invalid ? false : policy.enabled ?? settings.enabled,
				channel: policy.channel ?? settings.channel,
			};
			return { settings: effective, locked, ...(policy.pinnedVersion ? { pinnedVersion: policy.pinnedVersion } : {}) };
		},
		async setSettings(next) {
			if (isManagedUpdatePolicy(await deps.readPolicy())) throw new Error("Updates are managed by your organization.");
			await writeUpdateSettings(deps.stateDir, next);
			await apply();
		},
		checkNow: () => check(),
		install() {
			// Repeat clicks while Squirrel prepares the install do nothing.
			if (status.state !== "downloaded" || !updater || !plan.active) return false;
			stopIdleWatch();
			const { restartWhenIdle: _pending, ...staged } = status;
			emit({ ...staged, state: "installing" });
			deps.beforeInstall();
			// Not silent, relaunch afterwards: the normal quit path stops agents and
			// the daemon, and the new app starts the daemon, which resumes them.
			updater.quitAndInstall(false, true);
			return true;
		},
		installWhenIdle() {
			if (status.state !== "downloaded" || !updater || !plan.active || !deps.agentsBusy) return false;
			if (status.restartWhenIdle) return true;
			emit({ ...status, restartWhenIdle: true });
			idleTimer = every(() => void idleTick(), IDLE_POLL_MS);
			void idleTick();
			return true;
		},
		cancelInstallWhenIdle: clearIdleWatch,
		dispose() {
			stopSchedule();
			stopIdleWatch();
		},
	};
	return controller;
}
