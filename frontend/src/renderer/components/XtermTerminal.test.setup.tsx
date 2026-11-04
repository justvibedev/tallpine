import { vi } from "vitest";

const state = vi.hoisted(() => ({
	fit: vi.fn(),
	lifecycle: [] as string[],
	linkHandler: null as null | ((event: MouseEvent, uri: string) => void),
	queueViewportSyncOnOpen: false,
	sessionLinkProvider: null as null | {
		provideLinks: (
			line: number,
			callback: (
				links?: Array<{
					text: string;
					range: { start: { x: number; y: number }; end: { x: number; y: number } };
					activate: (event: MouseEvent) => void;
				}>,
			) => void,
		) => void;
	},
	searchAddon: null as null | {
		clearDecorations: ReturnType<typeof vi.fn>;
		findNext: ReturnType<typeof vi.fn>;
		findPrevious: ReturnType<typeof vi.fn>;
		resultListeners: Set<(results: { resultCount: number; resultIndex: number }) => void>;
	},
	mouseMoveListener: vi.fn(),
	lastTerminal: null as null | {
		write(data: Uint8Array, done?: () => void): void;
		cols: number;
		screenElement: HTMLElement;
		keyHandler?: (event: KeyboardEvent) => boolean;
		wheelHandler?: (event: WheelEvent) => boolean;
		selection: string;
		options: Record<string, unknown>;
		rows: number;
		dimensions?: { css: { cell: { width: number; height: number } } };
		resize: ReturnType<typeof vi.fn>;
		modes: { bracketedPasteMode: boolean; mouseTrackingMode: string };
		buffer: {
			active: {
				baseY: number;
				type: string;
				viewportY: number;
				length: number;
				getNullCell: () => {
					chars: string;
					width: number;
					getChars: () => string;
					getWidth: () => number;
				};
				getLine: (
					row: number,
				) =>
					| {
							translateToString: (trimRight: boolean, startColumn?: number, endColumn?: number) => string;
							isWrapped: boolean;
							length?: number;
							getCell?: (column: number, cell: { chars: string; width: number }) => unknown;
					  }
					| undefined;
			};
		};
		bufferLines: Array<{
			translateToString: (trimRight: boolean, startColumn?: number, endColumn?: number) => string;
			isWrapped: boolean;
		}>;
		selectionRange?: { start: { x: number; y: number }; end: { x: number; y: number } };
		getSelectionPosition():
			| { start: { x: number; y: number }; end: { x: number; y: number } }
			| undefined;
		scrollLines: ReturnType<typeof vi.fn>;
		scrollToBottom: ReturnType<typeof vi.fn>;
		scrollToLine: ReturnType<typeof vi.fn>;
		refresh: ReturnType<typeof vi.fn>;
		clear: ReturnType<typeof vi.fn>;
		dispose: ReturnType<typeof vi.fn>;
		focus: ReturnType<typeof vi.fn>;
		selectAll: ReturnType<typeof vi.fn>;
		dataListeners: Set<(data: string) => void>;
		renderListeners: Set<() => void>;
		csiHandlers: Array<{
			callback: (params: (number | number[])[]) => boolean | Promise<boolean>;
			id: { final: string; intermediates?: string; prefix?: string };
		}>;
		keyListeners: Set<(event: { key: string }) => void>;
		selectionListeners: Set<() => void>;
		scrollListeners: Set<() => void>;
		_core: {
			element: {
				classList: { add: ReturnType<typeof vi.fn>; remove: ReturnType<typeof vi.fn> };
				getBoundingClientRect: () => DOMRect;
			};
			viewport: { scrollBarWidth: number };
			_selectionService: {
				enable: ReturnType<typeof vi.fn>;
				shouldForceSelection: (event: MouseEvent) => boolean;
				_mouseMoveListener?: EventListener;
				_dragScrollAmount?: number;
			};
		};
	},
}));


vi.mock("@xterm/xterm", () => ({
	Terminal: class FakeTerminal {
		options: Record<string, unknown>;
		cols = 80;
		screenElement = Object.assign(document.createElement("div"), {
			getBoundingClientRect: () => ({ left: 0, top: 0, width: 800, height: 240 }) as DOMRect,
		});
		rows = 24;
		dimensions?: { css: { cell: { width: number; height: number } } };
		resize = vi.fn((cols: number, rows: number) => { this.cols = cols; this.rows = rows; });
		selection = "";
		keyHandler?: (event: KeyboardEvent) => boolean;
		wheelHandler?: (event: WheelEvent) => boolean;
		modes = { bracketedPasteMode: false, mouseTrackingMode: "vt200" };
		bufferLines: Array<{
			translateToString: (trimRight: boolean, startColumn?: number, endColumn?: number) => string;
			isWrapped: boolean;
		}> = [];
		buffer = {
			active: {
				baseY: 0,
				type: "normal",
				viewportY: 0,
				length: 1,
				getNullCell: () => ({
					chars: "",
					width: 1,
					getChars() {
						return this.chars;
					},
					getWidth() {
						return this.width;
					},
				}),
				getLine: (row: number) => this.bufferLines[row],
			},
		};
		selectionRange?: { start: { x: number; y: number }; end: { x: number; y: number } };
		getSelectionPosition() {
			return this.selectionRange;
		}
		scrollLines = vi.fn();
		scrollToBottom = vi.fn();
		scrollToLine = vi.fn();
		refresh = vi.fn();
		clear = vi.fn();
		focus = vi.fn();
		selectAll = vi.fn();
		dataListeners = new Set<(data: string) => void>();
		renderListeners = new Set<() => void>();
		csiHandlers: Array<{
			callback: (params: (number | number[])[]) => boolean | Promise<boolean>;
			id: { final: string; intermediates?: string; prefix?: string };
		}> = [];
		parser = {
			registerCsiHandler: (
				id: { final: string; intermediates?: string; prefix?: string },
				callback: (params: (number | number[])[]) => boolean | Promise<boolean>,
			) => {
				const handler = { callback, id };
				this.csiHandlers.push(handler);
				return {
					dispose: () => {
						this.csiHandlers = this.csiHandlers.filter((candidate) => candidate !== handler);
					},
				};
			},
		};
		keyListeners = new Set<(event: { key: string }) => void>();
		selectionListeners = new Set<() => void>();
		scrollListeners = new Set<() => void>();
		writeBuffer = "";
		_core = {
			element: {
				classList: { add: vi.fn(), remove: vi.fn() },
				getBoundingClientRect: () => ({ left: 0, right: 800 }) as DOMRect,
			},
			viewport: { scrollBarWidth: 15 },
			_selectionService: {
				enable: vi.fn(),
				shouldForceSelection: () => false,
				_mouseMoveListener: state.mouseMoveListener,
				_dragScrollAmount: 0,
			},
		};

		constructor(options: Record<string, unknown>) {
			this.options = options;
			state.lastTerminal = this;
		}

		loadAddon() {}
		open(host: HTMLElement) {
			host.appendChild(document.createElement("textarea"));
			if (state.queueViewportSyncOnOpen) {
				window.setTimeout(() => state.lifecycle.push("viewport-sync"), 0);
			}
		}
		write(data: Uint8Array, done?: () => void) {
			this.writeBuffer += new TextDecoder().decode(data);
			if (this.writeBuffer.includes("\x1b[6n")) {
				this.dataListeners.forEach((listener) => listener("\x1b[7;21R"));
				this.writeBuffer = "";
			}
			done?.();
		}
		writeln() {}
		dispose = vi.fn(() => {
			state.lifecycle.push("dispose");
		});
		onData(listener: (data: string) => void) {
			this.dataListeners.add(listener);
			return { dispose: () => this.dataListeners.delete(listener) };
		}
		onResize() {
			return { dispose: () => undefined };
		}
		onRender(listener: () => void) {
			this.renderListeners.add(listener);
			return { dispose: () => this.renderListeners.delete(listener) };
		}
		onScroll(listener: () => void) {
			this.scrollListeners.add(listener);
			return { dispose: () => this.scrollListeners.delete(listener) };
		}
		onKey(listener: (event: { key: string }) => void) {
			this.keyListeners.add(listener);
			return { dispose: () => this.keyListeners.delete(listener) };
		}
		onSelectionChange(listener: () => void) {
			this.selectionListeners.add(listener);
			return { dispose: () => this.selectionListeners.delete(listener) };
		}
		hasSelection() {
			return this.selection.length > 0;
		}
		getSelection() {
			return this.selection;
		}
		attachCustomKeyEventHandler(listener: (event: KeyboardEvent) => boolean) {
			this.keyHandler = listener;
		}
		attachCustomWheelEventHandler(listener: (event: WheelEvent) => boolean) {
			this.wheelHandler = listener;
		}
		registerLinkProvider(provider: typeof state.sessionLinkProvider) {
			state.sessionLinkProvider = provider;
			return { dispose: () => undefined };
		}
		unicode = { activeVersion: "" };
	},
}));


vi.mock("@xterm/addon-fit", () => ({
	FitAddon: class FakeFitAddon {
		proposeDimensions() {
			return state.fit();
		}
	},
}));


vi.mock("@xterm/addon-search", () => ({
	SearchAddon: class FakeSearchAddon {
		clearDecorations = vi.fn();
		findNext = vi.fn(() => true);
		findPrevious = vi.fn(() => true);
		resultListeners = new Set<(results: { resultCount: number; resultIndex: number }) => void>();

		constructor() {
			state.searchAddon = this;
		}

		onDidChangeResults(listener: (results: { resultCount: number; resultIndex: number }) => void) {
			this.resultListeners.add(listener);
			return { dispose: () => this.resultListeners.delete(listener) };
		}
	},
}));


vi.mock("@xterm/addon-unicode11", () => ({
	Unicode11Addon: class FakeUnicode11Addon {},
}));


vi.mock("@xterm/addon-web-links", () => ({
	WebLinksAddon: class FakeWebLinksAddon {
		constructor(handler?: (event: MouseEvent, uri: string) => void) {
			state.linkHandler = handler ?? null;
		}
	},
}));


vi.mock("@xterm/addon-webgl", () => ({
	WebglAddon: class FakeWebglAddon {
		onContextLoss() {}
		dispose() {}
	},
}));


export function setNavigatorPlatform(platform: string) {
	Object.defineProperty(window.navigator, "platform", {
		configurable: true,
		value: platform,
	});
	Object.defineProperty(window.navigator, "userAgentData", {
		configurable: true,
		value: { platform },
	});
}


export type TestBufferCell = { chars: string; width: number };


export function asciiCells(text: string): TestBufferCell[] {
	return [...text].map((chars) => ({ chars, width: 1 }));
}


export function testBufferLine(cells: TestBufferCell[], isWrapped = false) {
	return {
		isWrapped,
		length: cells.length,
		getCell(column: number, target: { chars: string; width: number }) {
			const cell = cells[column];
			if (!cell) return undefined;
			target.chars = cell.chars;
			target.width = cell.width;
			return target;
		},
		translateToString(trimRight: boolean, startColumn = 0, endColumn = cells.length) {
			const text = cells
				.slice(startColumn, endColumn)
				.filter((cell) => cell.width > 0)
				.map((cell) => cell.chars || " ")
				.join("");
			return trimRight ? text.trimEnd() : text;
		},
	};
}


export { state };
