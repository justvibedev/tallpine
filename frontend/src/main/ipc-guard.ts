import type { IpcMain, IpcMainEvent, IpcMainInvokeEvent, WebContents } from "electron";

/**
 * Sender validation for every ipcMain registration. The preload is attached to
 * the shell view only, so a call from any other webContents or from a frame
 * that is not the app's own origin (injected iframe, navigated shell) is
 * rejected before the handler runs.
 */

/**
 * Exact (method, channel) pairs that other webContents legitimately call: the
 * annotation overlay preload (annotate-preload.ts) running inside browser-view
 * pages. Those handlers do their own ownership checks. Keyed by method because
 * `browser:annotation:discard` is a shell-side `handle` and a page-side `on`.
 */
export const NON_SHELL_CHANNELS: Readonly<Record<IpcMethod, ReadonlySet<string>>> = {
	handle: new Set(["browser:annotation:submit", "browser:annotation:capture"]),
	on: new Set(["browser:annotation:cancel", "browser:annotation:state", "browser:annotation:discard"]),
};

export type IpcMethod = "handle" | "on";

type AnyEvent = IpcMainEvent | IpcMainInvokeEvent;

export interface IpcGuardOptions {
	getShellWebContents: () => WebContents | null;
	/** Origins like `app://renderer` or `http://localhost:5173`. */
	allowedOrigins: readonly string[];
	nonShellChannels?: Readonly<Record<IpcMethod, ReadonlySet<string>>>;
}

/** `URL.origin` is "null" for custom schemes, so compare scheme and host. */
export function originOf(url: string): string | null {
	try {
		const parsed = new URL(url);
		return `${parsed.protocol}//${parsed.host}`;
	} catch {
		return null;
	}
}

export function isTrustedSender(event: AnyEvent, method: IpcMethod, channel: string, options: IpcGuardOptions): boolean {
	if ((options.nonShellChannels ?? NON_SHELL_CHANNELS)[method].has(channel)) return true;
	const shell = options.getShellWebContents();
	if (!shell || event.sender !== shell) return false;
	const origin = event.senderFrame ? originOf(event.senderFrame.url) : null;
	return origin !== null && options.allowedOrigins.includes(origin);
}

type Listener = (...args: never[]) => unknown;

/** Returns an ipcMain whose handle/on/once reject untrusted senders. */
export function guardIpcMain(ipcMain: IpcMain, options: IpcGuardOptions): IpcMain {
	const wrapped = new WeakMap<Listener, Listener>();
	const guard = (method: IpcMethod, channel: string, fn: Listener, onReject: (event: AnyEvent) => unknown): Listener => {
		const guarded = ((event: AnyEvent, ...args: unknown[]) => {
			if (!isTrustedSender(event, method, channel, options)) {
				console.warn(`ipc: rejected untrusted sender on ${channel}`);
				return onReject(event);
			}
			return (fn as (...a: unknown[]) => unknown)(event, ...args);
		}) as Listener;
		wrapped.set(fn, guarded);
		return guarded;
	};
	const rejectInvoke = (): never => {
		throw new Error("Untrusted IPC sender.");
	};
	const override: Partial<Record<keyof IpcMain, unknown>> = {
		handle: (channel: string, fn: Listener) => ipcMain.handle(channel, guard("handle", channel, fn, rejectInvoke) as never),
		on: (channel: string, fn: Listener) => ipcMain.on(channel, guard("on", channel, fn, () => undefined) as never),
		once: (channel: string, fn: Listener) => ipcMain.once(channel, guard("on", channel, fn, () => undefined) as never),
		off: (channel: string, fn: Listener) => ipcMain.off(channel, (wrapped.get(fn) ?? fn) as never),
		removeListener: (channel: string, fn: Listener) => ipcMain.removeListener(channel, (wrapped.get(fn) ?? fn) as never),
	};
	return new Proxy(ipcMain, {
		get(target, prop, receiver) {
			if (prop in override) return override[prop as keyof IpcMain];
			const value = Reflect.get(target, prop, receiver);
			return typeof value === "function" ? value.bind(target) : value;
		},
	});
}
