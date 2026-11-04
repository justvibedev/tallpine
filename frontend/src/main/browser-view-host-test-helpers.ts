import { vi } from "vitest";
import { type BrowserNavState, createBrowserViewHost } from "./browser-view-host";
import { browserProfilePartition, type BrowserProfile } from "../shared/browser-profiles";
import type { BrowserProfileStore } from "./browser-profile-store";
import type { BrowserHistoryStore } from "./browser-history-store";
import type { TabLifecycleLimits } from "./browser-tab-lifecycle";
import type { BrowserAnnotationDraft, BrowserAnnotationSession } from "../shared/browser-annotations";

export type InvokeHandler = (event: unknown, ...args: unknown[]) => unknown;

export type EventHandler = (
	event: { sender: { id: number; getZoomFactor?: () => number; send?: (channel: string, payload: unknown) => void } },
	...args: unknown[]
) => unknown;


export function annotationDraft(url = "http://localhost:4173/"): BrowserAnnotationDraft {
	return {
		kind: "comment",
		body: "Keep this text after refresh",
		target: {
			context: {
				url,
				tag: "button",
				classes: [],
				selector: "button#save",
				size: { width: 80, height: 30 },
				rect: { x: 10, y: 20, width: 80, height: 30 },
				computedStyle: {},
			},
		},
		adjustments: [],
	};
}


export function annotationSession(url = "http://localhost:4173/"): BrowserAnnotationSession {
	return {
		version: 1,
		page: { url, title: "Preview" },
		annotations: [],
		draft: annotationDraft(url),
		screenshots: [],
	};
}


export function submittedAnnotationSession(
	url = "http://localhost:5173/",
	comments: Array<{ body: string; selector: string }> = [{ body: "Make this button blue.", selector: "button" }],
): BrowserAnnotationSession {
	const timestamp = "2026-06-15T00:00:00Z";
	return {
		version: 1,
		page: { url, title: "Preview" },
		annotations: comments.map((comment, index) => ({
			id: `annotation-${index + 1}`,
			number: index + 1,
			kind: "comment",
			body: comment.body,
			target: {
				context: {
					url,
					tag: "button",
					classes: [],
					selector: comment.selector,
					size: { width: 80, height: 30 },
					rect: { x: 10, y: 20 + index * 40, width: 80, height: 30 },
					computedStyle: {},
				},
			},
			adjustments: [],
			createdAt: timestamp,
			updatedAt: timestamp,
		})),
		screenshots: [],
	};
}


export function setupHost(agentBrowserRuntime?: import("./agent-browser-runtime/runtime").AgentBrowserRuntime) {
	let currentURL = "";
	let browserZoomFactor = 1;
	const webContentsListeners = new Map<string, (...args: never[]) => void>();
	const shellWebContentsListeners = new Map<string, (...args: never[]) => void>();
	const addListener = (
		listeners: Map<string, (...args: never[]) => void>,
		event: string,
		listener: (...args: never[]) => void,
	) => {
		const previous = listeners.get(event);
		listeners.set(
			event,
			previous
				? (...args) => {
						previous(...args);
						listener(...args);
					}
				: listener,
		);
	};
	const debuggerListeners = new Map<string, (...args: never[]) => void>();
	let debuggerAttached = false;
	const openDevTools = vi.fn();
	const closeDevTools = vi.fn();
	const insertCSS = vi.fn(async (_css: string, _options?: { cssOrigin?: "author" | "user" }) => "tallpine-browser-scrollbars");
	let insertedStyleNumber = 0;
	insertCSS.mockImplementation(async () => `tallpine-browser-scrollbars-${++insertedStyleNumber}`);
	const removeInsertedCSS = vi.fn(async (_key: string) => undefined);
	const findInPage = vi.fn(() => 17);
	const stopFindInPage = vi.fn();
	const writeImage = vi.fn();
	const debuggerSendCommand = vi.fn(async (method: string, params?: Record<string, unknown>): Promise<unknown> => {
		if (method === "Page.navigate" && typeof params?.url === "string") currentURL = params.url;
		return {};
	});
	const setPermissionCheckHandler = vi.fn();
	const setPermissionRequestHandler = vi.fn();
	const webContents = {
		id: 99,
		mainFrame: { frameToken: "preview-frame" },
		canGoBack: () => false,
		canGoForward: () => false,
		capturePage: vi.fn(async () => ({
			isEmpty: (): boolean => false,
			toJPEG: () => Buffer.from("snapshot"),
			toPNG: () => Buffer.from("png-snapshot"),
			getSize: () => ({ width: 640, height: 480 }),
			resize: vi.fn(() => ({ toPNG: () => Buffer.from("resized-png") })),
		})),
		debugger: {
			attach: vi.fn(() => {
				debuggerAttached = true;
			}),
			detach: vi.fn(() => {
				debuggerAttached = false;
			}),
			isAttached: () => debuggerAttached,
			on: (event: string, listener: (...args: never[]) => void) => debuggerListeners.set(event, listener),
			sendCommand: debuggerSendCommand,
		},
		clearHistory: () => undefined,
		getTitle: () => "",
		getURL: () => currentURL,
		getZoomFactor: () => browserZoomFactor,
		goBack: () => undefined,
		goForward: () => undefined,
		isLoading: () => false,
		insertCSS,
		removeInsertedCSS,
		invalidate: vi.fn(),
		loadURL: vi.fn(async (url: string) => {
			currentURL = url;
		}),
		on: (event: string, listener: (...args: never[]) => void) => {
			addListener(webContentsListeners, event, listener);
		},
		executeJavaScript: vi.fn(async (_script: string) => undefined),
		focus: vi.fn(),
		findInPage,
		reload: vi.fn(),
		send: vi.fn(),
		sendInputEvent: vi.fn(),
		setWindowOpenHandler: () => undefined,
		stop: () => undefined,
		stopFindInPage,
		close: vi.fn(),
		openDevTools,
		closeDevTools,
		setWebRTCIPHandlingPolicy: vi.fn(),
		session: {
			setPermissionCheckHandler,
			setPermissionRequestHandler,
			setProxy: vi.fn(async () => undefined),
		},
	};
	const view = {
		webContents,
		setBounds: vi.fn(),
		getBounds: vi.fn(() => ({ x: -10000, y: -10000, width: 1280, height: 720 })),
		setBorderRadius: vi.fn(),
		setVisible: vi.fn(),
		setIgnoreMouseEvents: vi.fn(),
	};
	const runtime =
		agentBrowserRuntime ??
		({
			runAction: vi.fn(async (_sessionId, action, args, provider) => {
				if (action === "open") {
					await provider.listTargets()[0]?.debugger.sendCommand("Page.navigate", { url: args.url });
					return {};
				}
				if (action === "snapshot") return { snapshot: "(empty accessibility snapshot)", refs: {} };
				if (action === "tab-new") {
					await provider.createTarget(typeof args.url === "string" ? args.url : "about:blank");
					return {};
				}
				if (action === "tab-select") {
					await provider.activateTarget(String(args.tabId));
					return {};
				}
				if (action === "tab-close") {
					await provider.closeTarget(String(args.tabId));
					return {};
				}
				if (action === "get") return { value: currentURL };
				if (action === "console" || action === "errors") return { messages: [] };
				return {};
			}),
			screenshot: vi.fn(async () => ({
				data: Buffer.from("png-snapshot").toString("base64"),
				width: 640,
				height: 480,
				untrustedExternalContent: true as const,
			})),
			closeSession: vi.fn(async () => undefined),
			dispose: vi.fn(async () => undefined),
		} as unknown as import("./agent-browser-runtime/runtime").AgentBrowserRuntime);
	const handlers = new Map<string, InvokeHandler>();
	const eventHandlers = new Map<string, EventHandler>();
	const sent: Array<{ channel: string; payload: unknown }> = [];
	const shellFocus = vi.fn();
	const shellSend = vi.fn((channel: string, payload?: unknown) => sent.push({ channel, payload }));
	const mainContentView = { addChildView: vi.fn(), removeChildView: vi.fn() };
	const isWindowVisible = vi.fn(() => true);
	const host = createBrowserViewHost({
		mainWindow: {
			isVisible: isWindowVisible,
			contentView: mainContentView,
			getContentBounds: () => ({ x: 0, y: 0, width: 800, height: 600 }),
			webContents: {
				id: 1,
				focus: shellFocus,
				send: shellSend,
				on: (event: string, listener: (...args: never[]) => void) => {
					addListener(shellWebContentsListeners, event, listener);
				},
			},
		} as never,
		ipcMain: {
			handle: (channel: string, fn: InvokeHandler) => handlers.set(channel, fn),
			on: (channel: string, fn: EventHandler) => eventHandlers.set(channel, fn),
			removeHandler: () => undefined,
			off: () => undefined,
		} as never,
		shell: { openExternal: async () => undefined },
		WebContentsView: function () {
			return view;
		} as never,
		annotatePreloadPath: "/preload.js",
		rendererOrigin: "http://localhost:5173",
		agentBrowserRuntime: runtime,
		clipboard: { writeImage },
	});
	const rendererFrame = { processId: 5, routingId: 7 };
	const invoke = (channel: string, ...args: unknown[]) =>
		handlers.get(channel)!({ sender: { id: 1 }, senderFrame: rendererFrame }, ...args) as Promise<BrowserNavState>;
	// browser:annotation:submit is a handle() (invoke/await), not an on() —
	// unlike invoke() above it must impersonate the browser tab's own
	// webContents (senderId), not the shell window's, so forwardAnnotationSubmit
	// can resolve it via tabsByWebContentsId.
	const invokeFromTab = (channel: string, senderId: number, ...args: unknown[]) =>
		handlers.get(channel)!({ sender: { id: senderId } }, ...args);
	const emit = (channel: string, zoomFactor: number, ...args: unknown[]) =>
		eventHandlers.get(channel)!({ sender: { id: 1, getZoomFactor: () => zoomFactor, send: shellSend } }, ...args);
	const send = (channel: string, senderId: number, ...args: unknown[]) =>
		eventHandlers.get(channel)!({ sender: { id: senderId } }, ...args);
	const emitBeforeInput = (input: {
		key: string;
		control?: boolean;
		meta?: boolean;
		shift?: boolean;
		alt?: boolean;
		type?: string;
		isAutoRepeat?: boolean;
	}) => {
		const event = { preventDefault: vi.fn() };
		webContentsListeners.get("before-input-event")?.(
			event as never,
			{
				control: false,
				meta: false,
				shift: false,
				alt: false,
				type: "keyDown",
				...input,
			} as never,
		);
		return event;
	};
	const emitShellBeforeInput = (input: {
		key: string;
		control?: boolean;
		meta?: boolean;
		shift?: boolean;
		alt?: boolean;
		type?: string;
		isAutoRepeat?: boolean;
	}) => {
		const event = { preventDefault: vi.fn() };
		shellWebContentsListeners.get("before-input-event")?.(
			event as never,
			{ control: false, meta: false, shift: false, alt: false, type: "keyDown", ...input } as never,
		);
		return event;
	};
	return {
		emit,
		emitBeforeInput,
		emitShellBeforeInput,
		host,
		invoke,
		invokeFromTab,
		mainContentView,
		isWindowVisible,
		rendererFrame,
		send,
		sent,
		setPermissionCheckHandler,
		setPermissionRequestHandler,
		shellFocus,
		shellSend,
		openDevTools,
		closeDevTools,
		insertCSS,
		removeInsertedCSS,
		writeImage,
		setBrowserZoomFactor: (zoomFactor: number) => {
			browserZoomFactor = zoomFactor;
		},
		setCurrentURL: (url: string) => {
			currentURL = url;
		},
		view,
		webContents,
		webContentsListeners,
		debuggerListeners,
		debuggerSendCommand,
	};
}


export const enableAnnotation = (invoke: (channel: string, ...args: unknown[]) => Promise<unknown>, nav: BrowserNavState) =>
	invoke("browser:annotation:setMode", { viewId: nav.viewId, enabled: true });


export const AGENT_VIEWER = "https://ao-preview-agent.localhost:47663/v/cdp";


export type LifecycleContents = {
	setBackgroundThrottling: ReturnType<typeof vi.fn>;
	forcefullyCrashRenderer: ReturnType<typeof vi.fn>;
	reload: ReturnType<typeof vi.fn>;
	isCrashed: () => boolean;
};


export function lifecycleContents(viewIndex: number, pids?: number[]): LifecycleContents & {
	getOSProcessId: () => number;
	isCurrentlyAudible: () => boolean;
} {
	let crashed = false;
	return {
		setBackgroundThrottling: vi.fn(),
		forcefullyCrashRenderer: vi.fn(() => {
			crashed = true;
		}),
		reload: vi.fn(() => {
			crashed = false;
		}),
		isCrashed: () => crashed,
		getOSProcessId: () => pids?.[viewIndex] ?? 5_000 + viewIndex,
		isCurrentlyAudible: () => false,
	};
}


export function setupTabHost(
	browserProfileStore?: BrowserProfileStore,
	failViewConstruction = false,
	loadURLHook?: (viewIndex: number, url: string) => Promise<void>,
	browserHistoryStore?: BrowserHistoryStore,
	clearBrowserProfileData = vi.fn(async (_partition: string) => undefined),
	overrides?: {
		entryReadyTimeoutMs?: number;
		confirmAgentLogins?: (sessionId: string, profileName: string) => Promise<boolean>;
		// Gives each view the lifecycle methods (throttling, renderer kill, pid);
		// `pids` assigns renderer processes by view index (default: one each).
		lifecycle?: { pids?: number[]; limits?: TabLifecycleLimits; sweepMs?: number };
	},
) {
	const constructorOptions: Array<{
		webPreferences: Electron.WebPreferences;
		webContents?: Electron.WebContents;
	}> = [];
	const handlers = new Map<string, InvokeHandler>();
	const eventHandlers = new Map<string, EventHandler>();
	const sent: Array<{ channel: string; payload: unknown }> = [];
	const views: Array<{
		webContents: {
			id: number;
			getURL: () => string;
			loadURL: ReturnType<typeof vi.fn>;
			openDevTools: ReturnType<typeof vi.fn>;
			closeDevTools: ReturnType<typeof vi.fn>;
			send: ReturnType<typeof vi.fn>;
			openWindow: (url: string, nativeGuest?: boolean) => Electron.WebContents | null;
			close: ReturnType<typeof vi.fn>;
			setWebRTCIPHandlingPolicy: ReturnType<typeof vi.fn>;
			emitConsoleMessage: (level: number, message: string, line?: number, sourceId?: string) => void;
			executeJavaScript: ReturnType<typeof vi.fn>;
				session: {
					fetch: ReturnType<typeof vi.fn>;
					setPermissionCheckHandler: ReturnType<typeof vi.fn>;
				setPermissionRequestHandler: ReturnType<typeof vi.fn>;
				setProxy: ReturnType<typeof vi.fn>;
				webRequest: {
					onCompleted: ReturnType<typeof vi.fn>;
					onErrorOccurred: ReturnType<typeof vi.fn>;
				};
			};
		};
		listeners: Map<string, (...args: never[]) => void>;
		setBounds: ReturnType<typeof vi.fn>;
		setBorderRadius: ReturnType<typeof vi.fn>;
		setVisible: ReturnType<typeof vi.fn>;
	}> = [];
	const guestViews = new Map<Electron.WebContents, (typeof views)[number]>();
	let nextID = 100;
	const debuggerCommands: Array<{ method: string; params?: Record<string, unknown> }> = [];
	// Populated by tests that need a specific navigation to fail, e.g. a dead
	// localhost dev server — every view's loadURL checks this shared set.
	const failNavigationTo = new Set<string>();
	const makeElectronSession = () => ({
		fetch: vi.fn(async (_url: string, _init?: RequestInit) => ({ ok: false } as Response)),
		setPermissionCheckHandler: vi.fn(),
		setPermissionRequestHandler: vi.fn(),
		setProxy: vi.fn(async () => undefined),
		webRequest: {
			onCompleted: vi.fn(),
			onErrorOccurred: vi.fn(),
		},
	});
	const electronSessions = new Map<string, ReturnType<typeof makeElectronSession>>();
	const makeView = (partition: string): (typeof views)[number] => {
		const viewIndex = views.length;
		const electronSession = electronSessions.get(partition) ?? makeElectronSession();
		electronSessions.set(partition, electronSession);
		let currentURL = "";
		let windowOpenHandler: ((details: Electron.HandlerDetails) => Electron.WindowOpenHandlerResponse) | undefined;
		const listeners = new Map<string, (...args: never[]) => void>();
		let debuggerAttached = false;
		const webContents = {
			id: nextID++,
			mainFrame: {},
			canGoBack: () => false,
			canGoForward: () => false,
			clearHistory: () => undefined,
			debugger: {
				attach: () => {
					debuggerAttached = true;
				},
				detach: () => {
					debuggerAttached = false;
				},
				isAttached: () => debuggerAttached,
				on: () => undefined,
				sendCommand: async (method: string, params?: Record<string, unknown>) => {
					debuggerCommands.push({ method, params });
					if (method === "Page.navigate" && typeof params?.url === "string") currentURL = params.url;
					return {};
				},
			},
			getTitle: () => (currentURL ? `Title ${currentURL}` : ""),
			getURL: () => currentURL,
			goBack: () => undefined,
			goForward: () => undefined,
			isLoading: () => false,
			loadURL: vi.fn(async (url: string) => {
				if (failNavigationTo.has(url)) {
					throw Object.assign(new Error(`ERR_CONNECTION_REFUSED (-102) loading '${url}'`), { errorCode: -102 });
				}
				currentURL = url;
				await loadURLHook?.(viewIndex, url);
				listeners.get("dom-ready")?.();
			}),
			on: (event: string, listener: (...args: never[]) => void) => {
				const previous = listeners.get(event);
				listeners.set(event, (...args) => {
					previous?.(...args);
					listener(...args);
				});
			},
			executeJavaScript: vi.fn(async (_script: string) => undefined),
			reload: () => undefined,
			...(overrides?.lifecycle ? lifecycleContents(viewIndex, overrides.lifecycle.pids) : {}),
			send: vi.fn(),
			setWindowOpenHandler: (
				handler: (details: Electron.HandlerDetails) => Electron.WindowOpenHandlerResponse,
			) => {
				windowOpenHandler = handler;
			},
			stop: () => undefined,
			setWebRTCIPHandlingPolicy: vi.fn(),
			close: vi.fn(),
			focus: vi.fn(),
			openDevTools: vi.fn(),
			closeDevTools: vi.fn(),
			session: electronSession,
			openWindow: (url: string, nativeGuest = true) => {
				const result = windowOpenHandler?.({
					url,
					frameName: "",
					features: "",
					disposition: "new-window",
					referrer: { url: currentURL, policy: "default" },
				});
				if (result?.action !== "allow") return null;
				const guest = nativeGuest ? makeView(partition) : undefined;
				if (guest) views.pop(); // Electron created contents, not an Tallpine tab yet.
				const windowOptions = {
					...result.overrideBrowserWindowOptions,
					webContents: guest?.webContents as unknown as Electron.WebContents | undefined,
				};
				const contents = result.createWindow!(windowOptions);
				// Match Electron 33's identity check; the old fake accepted unrelated contents.
				if (guest && contents !== guest.webContents as unknown as Electron.WebContents)
					throw new Error("Created window must adopt the supplied guest WebContents");
				if (guest) void contents.loadURL(url);
				return contents;
			},
			emitConsoleMessage: (level: number, message: string, line = 0, sourceId = "") => {
				const listener = listeners.get("console-message") as
					| ((event: unknown, level: number, message: string, line: number, sourceId: string) => void)
					| undefined;
				listener?.({}, level, message, line, sourceId);
			},
		};
		const view = { webContents, listeners, setBounds: vi.fn(), setBorderRadius: vi.fn(), setVisible: vi.fn(), setIgnoreMouseEvents: vi.fn() };
		views.push(view);
		guestViews.set(webContents as unknown as Electron.WebContents, view);
		return view;
	};
	const activeTargets = new Map<string, string>();
	const runtime = {
		runAction: vi.fn(async (sessionId, action, args, provider) => {
			const targets = () => provider.listTargets();
			const active = () =>
				targets().find((target: { id: string }) => target.id === activeTargets.get(sessionId)) ?? targets()[0];
			if (action === "open") {
				await active()?.debugger.sendCommand("Page.navigate", { url: args.url });
				return {};
			}
			if (action === "snapshot") return { snapshot: '- button "Open" [ref=e1]', refs: {} };
			if (action === "tab-new") {
				const created = await provider.createTarget(typeof args.url === "string" ? args.url : "about:blank");
				activeTargets.set(sessionId, created.id);
				return {};
			}
			if (action === "tab-select") {
				await provider.activateTarget(String(args.tabId));
				activeTargets.set(sessionId, String(args.tabId));
				return {};
			}
			if (action === "tab-close") {
				await provider.closeTarget(String(args.tabId));
				activeTargets.set(sessionId, targets().at(-1)?.id ?? "");
				return {};
			}
			if (action === "get") return { value: active()?.url ?? "" };
			if (action === "click" && args.ref === "e1") {
				throw { code: "STALE_REFERENCE", message: "snapshot again" };
			}
			return {};
		}),
		screenshot: vi.fn(async () => ({ data: "", width: 0, height: 0, untrustedExternalContent: true as const })),
		closeSession: vi.fn(async (sessionId: string) => {
			activeTargets.delete(sessionId);
		}),
		dispose: vi.fn(async () => undefined),
	} as unknown as import("./agent-browser-runtime/runtime").AgentBrowserRuntime;
	// The agent browser's viewer URLs are Tallpine's only while its host is registered.
	const agentHost = { registered: true };
	const addChildView = vi.fn();
	const onNativeViewAdded = vi.fn();
	const onTabCloseFailure = vi.fn();
	const host = createBrowserViewHost({
		mainWindow: {
			contentView: { addChildView, removeChildView: () => undefined },
			getContentBounds: () => ({ x: 0, y: 0, width: 800, height: 600 }),
			webContents: {
				id: 1,
				focus: () => undefined,
				send: (channel: string, payload: unknown) => sent.push({ channel, payload }),
			},
		} as never,
		ipcMain: {
			handle: (channel: string, fn: InvokeHandler) => handlers.set(channel, fn),
			on: (channel: string, fn: EventHandler) => eventHandlers.set(channel, fn),
			removeHandler: () => undefined,
			off: (channel: string) => eventHandlers.delete(channel),
		} as never,
		shell: { openExternal: async () => undefined },
		WebContentsView: function (options: { webPreferences: Electron.WebPreferences; webContents?: Electron.WebContents }) {
			if (failViewConstruction) throw new Error("browser view startup failed");
			constructorOptions.push(options);
			if (options.webContents) {
				const contents = options.webContents;
				// The guest was created by openWindow; wrap it without creating new contents.
				const view = guestViews.get(contents)!;
				views.push(view);
				return view;
			}
			return makeView(options.webPreferences.partition ?? "");
		} as never,
		annotatePreloadPath: "/preload.js",
		rendererOrigin: "http://localhost:5173",
		agentBrowserRuntime: runtime,
		isAgentBrowserUrl: (url: string) => agentHost.registered && /^https:\/\/ao-preview-agent\.localhost:\d+\/v\/cdp/.test(url),
		browserProfileStore,
		browserHistoryStore,
		clearBrowserProfileData,
		confirmAgentLogins: overrides?.confirmAgentLogins,
		entryReadyTimeoutMs: overrides?.entryReadyTimeoutMs,
		tabLifecycleLimits: overrides?.lifecycle?.limits,
		tabLifecycleSweepMs: overrides?.lifecycle?.sweepMs,
		onNativeViewAdded,
		onTabCloseFailure,
		// Kept only as a regression tripwire: the removed auto-send path used
		// this option to discover the daemon before calling net.fetch.
		...({ getDaemonPort: () => 43123 } as Record<string, unknown>),
	});
	const invoke = (channel: string, ...args: unknown[]) =>
		handlers.get(channel)!({ sender: { id: 1 } }, ...args) as Promise<unknown>;
	const invokeFromTab = (channel: string, senderId: number, ...args: unknown[]) =>
		handlers.get(channel)!({ sender: { id: senderId } }, ...args) as Promise<unknown>;
	const emit = (channel: string, ...args: unknown[]) =>
		eventHandlers.get(channel)!({
			sender: {
				id: 1,
				getZoomFactor: () => 1,
				send: (sentChannel: string, payload: unknown) => sent.push({ channel: sentChannel, payload }),
			},
		}, ...args);
	const sendFromTab = (channel: string, senderId: number, ...args: unknown[]) =>
		eventHandlers.get(channel)!({ sender: { id: senderId } }, ...args);
	return {
		activeTargets,
		agentHost,
		addChildView,
		onNativeViewAdded,
		onTabCloseFailure,
		constructorOptions,
		debuggerCommands,
		emit,
		failNavigationTo,
		host,
		invoke,
		invokeFromTab,
		runtime,
		sendFromTab,
		sent,
		views,
		clearBrowserProfileData,
	};
}


export function fakeBrowserProfileStore(
	profile: BrowserProfile,
	bindings: Record<string, string | null>,
	defaultProfileId: string | null = null,
): BrowserProfileStore {
	return {
		profiles: [profile],
		getProfile: (profileId: string) => (profileId === profile.id ? { ...profile } : undefined),
		getSessionProfileId: (sessionId: string) => bindings[sessionId],
		getDefaultProfileId: () => defaultProfileId,
		// A binding of `null` is an explicit, durable choice of Temporary and
		// must be kept distinct from an absent key (no binding at all).
		bindSession: vi.fn(async (sessionId: string, profileId: string | null) => {
			bindings[sessionId] = profileId;
		}),
		isProfileOperationInProgress: () => false,
		waitForProfileOperation: async () => undefined,
		partitionForProfile: (profileId: string) => browserProfilePartition(profileId),
	} as unknown as BrowserProfileStore;
}
