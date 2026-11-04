// Reading and measuring the pages and artifacts an agent publishes inline (`tallpine render`,
// `tallpine report --artifact`). Desktop and phone share these parsers; each keeps only its own
// frame, theme and bridge code. The path guards (readArtifactRef) apply on both.

/** An agent HTML page published with `tallpine render`, shown inline in its turn. */
export interface RenderRef {
	id: string;
	title: string;
	height: number;
	/** Daemon-relative route, `/api/v1/sessions/{id}/renders/{renderId}`. */
	path: string;
	/** `[width, height]` pairs measured when the page was published, sorted by width. */
	heights?: Array<[number, number]>;
}

/** An HTML session artifact the agent reported with `tallpine report --artifact`, shown inline in its turn. */
export interface ArtifactRef {
	/** Relative to the session's artifact directory, slash-separated. */
	path: string;
	name: string;
	/** Daemon-relative route, `/api/v1/sessions/{id}/artifact-files/{path}`. */
	url: string;
}

/** The part of an activity's detail these readers look at. */
export interface RenderDetail {
	event?: string;
	render?: unknown;
	artifact?: unknown;
}

export const RENDER_MIN_HEIGHT = 80;
export const RENDER_MAX_HEIGHT = 2000;
export const RENDER_PATH = /^\/api\/v1\/sessions\/[^/]+\/renders\/[^/]+$/;
const ARTIFACT_URL = /^(\/api\/v1\/sessions\/[^/]+\/artifact-files\/).+/;
const MAX_MEASURED_WIDTHS = 16;

export function clampRenderHeight(height: number): number {
	return Math.min(RENDER_MAX_HEIGHT, Math.max(RENDER_MIN_HEIGHT, Math.round(height)));
}

/** The page a `render` activity points at, or undefined for anything malformed. */
export function readRenderRef(detail: RenderDetail | undefined): RenderRef | undefined {
	const render = detail?.event === "render" ? (detail.render as Record<string, unknown> | undefined) : undefined;
	if (
		!render ||
		typeof render.id !== "string" ||
		typeof render.title !== "string" ||
		typeof render.height !== "number" ||
		!Number.isFinite(render.height) ||
		typeof render.path !== "string" ||
		!RENDER_PATH.test(render.path)
	) {
		return undefined;
	}
	const ref: RenderRef = { id: render.id, title: render.title, height: clampRenderHeight(render.height), path: render.path };
	// A malformed measurement costs only the measured first height, not the page.
	if (isMeasuredHeights(render.heights)) ref.heights = render.heights;
	return ref;
}

/** The HTML artifact an `artifact` activity points at, or undefined for anything malformed. */
export function readArtifactRef(detail: RenderDetail | undefined): ArtifactRef | undefined {
	const artifact = detail?.event === "artifact" ? (detail.artifact as Record<string, unknown> | undefined) : undefined;
	if (
		!artifact ||
		typeof artifact.path !== "string" ||
		!isRelativePath(artifact.path) ||
		typeof artifact.name !== "string" ||
		typeof artifact.url !== "string" ||
		!staysUnder(artifact.url, ARTIFACT_URL.exec(artifact.url)?.[1])
	) {
		return undefined;
	}
	return { path: artifact.path, name: artifact.name, url: artifact.url };
}

/** Whether url, once the browser resolves its dot segments (escaped ones too), is still under prefix. */
function staysUnder(url: string, prefix: string | undefined): boolean {
	return prefix !== undefined && new URL(url, "http://127.0.0.1").pathname.startsWith(prefix);
}

/** Non-empty, not rooted or drive-lettered, and no `..` segment. */
function isRelativePath(path: string): boolean {
	return path !== "" && !/^([a-z]:|[\\/])/i.test(path) && !path.split(/[\\/]/).includes("..");
}

export const isPositiveInteger = (value: unknown) => typeof value === "number" && Number.isInteger(value) && value > 0;

/** 1-16 `[width, height]` pairs of positive integers, in increasing width. */
function isMeasuredHeights(heights: unknown): heights is Array<[number, number]> {
	return (
		Array.isArray(heights) &&
		heights.length > 0 &&
		heights.length <= MAX_MEASURED_WIDTHS &&
		heights.every(
			(pair, index) =>
				Array.isArray(pair) &&
				pair.length === 2 &&
				isPositiveInteger(pair[0]) &&
				isPositiveInteger(pair[1]) &&
				(index === 0 || pair[0] > heights[index - 1][0]),
		)
	);
}

/**
 * The page's height at a frame width, from its measured heights: the taller of
 * the heights at the nearest measured widths on each side, since a breakpoint
 * between them can make the page as tall as either. Ported from T3 Code's
 * `measuredHeight` (packages/shared/src/htmlRender.ts).
 */
export function measuredRenderHeight(heights: Array<[number, number]>, width: number): number {
	const above = heights.findIndex(([measuredWidth]) => measuredWidth >= width);
	const high = above === -1 ? heights.length - 1 : above;
	const low = heights[high]![0] === width ? high : Math.max(0, high - 1);
	return Math.max(heights[low]![1], heights[high]![1]);
}
