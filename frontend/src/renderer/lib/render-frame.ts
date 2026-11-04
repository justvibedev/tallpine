export { RENDER_MAX_HEIGHT, RENDER_MIN_HEIGHT, clampRenderHeight, measuredRenderHeight, readArtifactRef, readRenderRef } from "@tallpine/shared/chat/render-frame";

/**
 * The file a saved render is written to: its title, without characters a file
 * system refuses, capped at 120. Ported from T3 Code's `htmlRenderFileName`
 * (packages/shared/src/htmlRender.ts).
 */
export function renderFileName(title: string): string {
	const name = title
		.replace(/[\\/:*?"<>|\p{Cc}]+/gu, " ")
		.replace(/\s+/g, " ")
		.trim()
		.slice(0, 120)
		.trim();
	return `${name || "Page"}.html`;
}

export interface RenderTheme {
	appearance: "light" | "dark";
	variables: Record<string, string>;
}

// Agent-facing names (documented in commands/render.md) -> Tallpine's semantic tokens
// in frontend/src/styles/tokens.css. getComputedStyle resolves their var() chains.
const THEME_TOKENS: ReadonlyArray<readonly [string, string]> = [
	["--background", "--color-bg-primary"],
	["--foreground", "--color-text-primary"],
	["--muted", "--color-bg-tertiary"],
	["--muted-foreground", "--color-text-muted"],
	["--card", "--color-bg-secondary"],
	["--card-foreground", "--color-text-primary"],
	["--popover", "--color-bg-elevated"],
	["--border", "--color-border"],
	["--border-strong", "--color-border-strong"],
	["--primary", "--color-accent"],
	["--primary-foreground", "--color-accent-foreground"],
	["--accent", "--color-accent"],
	["--accent-foreground", "--color-accent-foreground"],
	["--success", "--color-success"],
	["--warning", "--color-warning"],
	["--destructive", "--color-danger"],
	["--code", "--color-text-markdown-code"],
	["--link", "--color-text-markdown-link"],
	["--chart-1", "--color-brand-logo"],
	["--font-sans", "--font-family-base"],
	["--font-mono", "--font-family-mono"],
	["--radius", "--radius-md"],
];

// ponytail: fixed categorical series for agent pages only (never Tallpine chrome);
// Tallpine's own --chart-* tokens are grayscale. Swap for tokens once design names some.
const CHART_SERIES = {
	dark: ["#2dd4bf", "#fbbf24", "#c084fc", "#fb7185", "#a3e635"],
	light: ["#0d9488", "#d97706", "#9333ea", "#e11d48", "#65a30d"],
} as const;

export function readRenderTheme(root: HTMLElement = document.documentElement): RenderTheme {
	const appearance = root.getAttribute("data-theme") === "light" ? "light" : "dark";
	const style = getComputedStyle(root);
	const variables: Record<string, string> = {};
	for (const [name, token] of THEME_TOKENS) {
		const value = style.getPropertyValue(token).trim();
		if (value) variables[name] = value;
	}
	CHART_SERIES[appearance].forEach((color, index) => {
		variables[`--chart-${index + 2}`] = color;
	});
	return { appearance, variables };
}

export function renderThemesEqual(left: RenderTheme, right: RenderTheme): boolean {
	return left.appearance === right.appearance && JSON.stringify(left.variables) === JSON.stringify(right.variables);
}

/** Where the page shows: inline in the thread, or expanded in the dialog. */
export type RenderDisplayMode = "inline" | "fullscreen";

/**
 * The fragment a render reads its theme and wrapping choice from before first
 * paint. scrollable lets a page scroll inside a capped frame with a scrollbar.
 */
export function renderThemeFragment(theme: RenderTheme, displayMode: RenderDisplayMode, scrollable = false, wrapContent = false): string {
	return `#ao-theme=${encodeURIComponent(JSON.stringify({ ...theme, displayMode, ...(scrollable ? { scrollable } : {}), wrapContent }))}`;
}

/** Live theme and wrapping updates; displayMode is the MCP Apps host-context field. */
export function renderThemeMessage(theme: RenderTheme, displayMode: RenderDisplayMode, scrollable = false, wrapContent = false) {
	return {
		jsonrpc: "2.0",
		method: "ui/notifications/host-context-changed",
		params: { theme: theme.appearance, styles: { variables: theme.variables }, displayMode, ...(scrollable ? { scrollable } : {}), wrapContent },
	} as const;
}

function rpc(data: unknown, method: string): Record<string, unknown> | undefined {
	if (typeof data !== "object" || data === null) return undefined;
	const message = data as Record<string, unknown>;
	if (message.jsonrpc !== "2.0" || message.method !== method) return undefined;
	return typeof message.params === "object" && message.params !== null ? (message.params as Record<string, unknown>) : undefined;
}

export function readRenderContentHeight(data: unknown): number | undefined {
	const height = rpc(data, "ui/notifications/size-changed")?.height;
	return typeof height === "number" && Number.isFinite(height) && height > 0 ? height : undefined;
}

export function readRenderLinkRequest(data: unknown): string | undefined {
	const url = rpc(data, "ui/open-link")?.url;
	return typeof url === "string" && /^https?:\/\//i.test(url) ? url : undefined;
}
