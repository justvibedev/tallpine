import { randomUUID } from "node:crypto";
import type { WebContents } from "electron";
import {
	isAutoAllowedSitePermission,
	isConfirmableExternalUrl,
	isSitePermissionKind,
	sitePermissionKinds,
	sitePermissionOrigin,
	type BrowserSitePermissionEntry,
	type BrowserSitePermissionPrompt,
	type BrowserSitePermissionResetInput,
	type BrowserSitePermissionRespondInput,
} from "../../shared/browser-site-permissions";
import { pushNavState } from "./nav-events";
import { canWriteBrowserClipboard } from "./shortcuts";
import type { HostContext } from "./context";
import type { BrowserEntry, BrowserSessionEntry, PendingSitePrompt } from "./types";

// A page that keeps asking cannot stack prompts without end.
const MAX_PENDING_PROMPTS = 4;

type PermissionDetails = {
	requestingUrl?: string;
	isMainFrame?: boolean;
	mediaTypes?: readonly string[];
	mediaType?: string;
};

/**
 * Where a tab's answers are remembered (the profile ID, or the temporary
 * partition), or undefined when the tab may not ask at all. Only a tab the
 * person opened in the session's own profile may ask. Agent tabs (throwaway
 * or granted partition), tabs showing the agent's own browser, and a tab left
 * from a profile being switched away stay deny-all.
 */
export function sitePermissionScope(
	entry: Pick<BrowserEntry, "agentDriven" | "agentTab" | "remote" | "partition">,
	session: Pick<BrowserSessionEntry, "profileId" | "profilePartition"> | undefined,
): string | undefined {
	if (entry.agentDriven || entry.agentTab !== undefined || entry.remote) return undefined;
	if (!session || entry.partition !== session.profilePartition) return undefined;
	return session.profileId ?? session.profilePartition;
}

export function createSitePermissions(ctx: HostContext) {
	const { entries, ipcDisposers, options, tabsByWebContentsId } = ctx;
	const store = options.browserSitePermissionStore;

	const personScope = (entry: BrowserEntry | undefined): string | undefined =>
		entry ? sitePermissionScope(entry, entries.get(entry.state.viewId)) : undefined;

	const entryFor = (contents: Pick<WebContents, "id"> | null): BrowserEntry | undefined =>
		contents ? tabsByWebContentsId.get(contents.id) : undefined;

	const pushIfActive = (entry: BrowserEntry): void => {
		if (entries.get(entry.state.viewId)?.activeTabId === entry.tabId) pushNavState(options, entry);
	};

	const enqueue = (entry: BrowserEntry, prompt: Omit<BrowserSitePermissionPrompt, "id">, scope: string, resolve: (allow: boolean) => void): void => {
		const queue = (entry.sitePrompts ??= []);
		const same = queue.find(
			(pending) =>
				pending.prompt.origin === prompt.origin &&
				pending.prompt.externalUrl === prompt.externalUrl &&
				pending.prompt.kinds.join() === prompt.kinds.join(),
		);
		if (same) {
			const previous = same.resolve;
			same.resolve = (allow) => {
				previous(allow);
				resolve(allow);
			};
			return;
		}
		if (queue.length >= MAX_PENDING_PROMPTS) {
			resolve(false);
			return;
		}
		queue.push({ prompt: { ...prompt, id: randomUUID() }, scope, resolve });
		if (queue.length === 1) pushIfActive(entry);
	};

	const sitePermissionCheck = (
		contents: WebContents | null,
		permission: string,
		requestingOrigin: string,
		details: PermissionDetails,
	): boolean => {
		if (canWriteBrowserClipboard(contents, permission, details.requestingUrl, details.isMainFrame === true)) return true;
		const scope = personScope(entryFor(contents));
		if (!scope || !store) return false;
		if (isAutoAllowedSitePermission(permission)) return true;
		const kinds = sitePermissionKinds(permission, details);
		const origin = sitePermissionOrigin(details.requestingUrl ?? requestingOrigin);
		if (!kinds || !origin || details.isMainFrame === false) return false;
		return kinds.every((kind) => store.get(scope, origin, kind) === "allow");
	};

	const sitePermissionRequest = (
		contents: WebContents | null,
		permission: string,
		callback: (granted: boolean) => void,
		details: PermissionDetails,
	): void => {
		if (canWriteBrowserClipboard(contents, permission, details.requestingUrl, details.isMainFrame === true)) return callback(true);
		const entry = entryFor(contents);
		const scope = personScope(entry);
		if (!entry || !scope || !store) return callback(false);
		if (isAutoAllowedSitePermission(permission)) return callback(true);
		const kinds = sitePermissionKinds(permission, details);
		const origin = sitePermissionOrigin(details.requestingUrl);
		if (!kinds || !origin || details.isMainFrame !== true) return callback(false);
		const remembered = kinds.map((kind) => store.get(scope, origin, kind));
		if (remembered.includes("block")) return callback(false);
		if (remembered.every((decision) => decision === "allow")) return callback(true);
		enqueue(entry, { origin, kinds }, scope, callback);
	};

	/**
	 * A mailto: or tel: link the page follows. Handled here (the navigation is
	 * cancelled) so a person tab asks before the mail or phone app opens.
	 * Returns false for any other URL.
	 */
	const confirmExternalUrl = (entry: BrowserEntry, url: string): boolean => {
		if (!isConfirmableExternalUrl(url)) return false;
		const scope = personScope(entry);
		if (!scope) return true;
		const origin = sitePermissionOrigin(entry.view.webContents.getURL()) ?? "";
		enqueue(entry, { origin, kinds: [], externalUrl: url }, scope, (allow) => {
			if (allow) void options.shell.openExternal(url).catch(() => undefined);
		});
		return true;
	};

	const rememberedSitePermissions = (entry: BrowserEntry): BrowserSitePermissionEntry[] => {
		const scope = personScope(entry);
		const origin = sitePermissionOrigin(entry.view.webContents?.getURL?.());
		return scope && origin && store ? store.list(scope, origin) : [];
	};

	const respond = (input: BrowserSitePermissionRespondInput): void => {
		const session = entries.get(input.viewId);
		if (!session) return;
		for (const entry of session.tabs.values()) {
			const index = entry.sitePrompts?.findIndex((pending) => pending.prompt.id === input.promptId) ?? -1;
			if (index < 0) continue;
			const [pending] = entry.sitePrompts!.splice(index, 1) as [PendingSitePrompt];
			const { kinds, origin } = pending.prompt;
			if (kinds.length) void store?.set(pending.scope, origin, kinds, input.allow ? "allow" : "block").catch(() => undefined);
			pending.resolve(input.allow);
			pushIfActive(entry);
			return;
		}
	};

	const reset = (input: BrowserSitePermissionResetInput): void => {
		const session = entries.get(input.viewId);
		const entry = session?.tabs.get(session.activeTabId);
		const scope = personScope(entry);
		const origin = sitePermissionOrigin(entry?.view.webContents.getURL());
		if (!entry || !scope || !origin || !store) return;
		void store.reset(scope, origin, isSitePermissionKind(input.kind) ? input.kind : undefined).catch(() => undefined);
		pushIfActive(entry);
	};

	// The same check as isRendererOwned (bounds-nav.ts): only the renderer
	// showing a view may answer for it.
	const isOwned = (sender: { id: number }, viewId: unknown): viewId is string =>
		typeof viewId === "string" && (ctx.rendererOwnersByViewId.get(viewId)?.has(sender.id) ?? false);

	const handle = (channel: string, fn: (input: never) => void): void => {
		options.ipcMain.handle(channel, (event, input: { viewId?: unknown } | undefined) => {
			if (input && isOwned(event.sender, input.viewId)) fn(input as never);
		});
		ipcDisposers.push(() => options.ipcMain.removeHandler(channel));
	};
	handle("browser:sitePermission:respond", (input: BrowserSitePermissionRespondInput) => {
		if (typeof input.promptId === "string" && typeof input.allow === "boolean") respond(input);
	});
	handle("browser:sitePermission:reset", reset);

	return { confirmExternalUrl, rememberedSitePermissions, sitePermissionCheck, sitePermissionRequest };
}
