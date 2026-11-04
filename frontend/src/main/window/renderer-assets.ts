/* Split out of main-window.ts (B297, P53). */
declare const MAIN_WINDOW_VITE_DEV_SERVER_URL: string | undefined;
declare const MAIN_WINDOW_VITE_NAME: string;
declare const __TALLPINE_TEAM_ID__: string | undefined;
declare const __TALLPINE_BUILD_INFO__: { version: string; commit: string; date: string; dirty: string } | undefined;
import {
app,
clipboard,
ClipboardItem,
nativeImage,
net,
protocol
} from "electron";
import { existsSync } from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { rendererCspHeader } from "../../shared/renderer-csp";
import { RENDERER_SCHEME } from "../app-bootstrap";
import { RENDERER_HOST, RENDERER_ORIGIN } from "../app-runtime-state";

// Maps app://renderer/<path> to the built renderer in dist/. Paths without a
// file extension are client-side routes and fall back to index.html (SPA).
export function registerRendererProtocol(): void {
	const distRoot = path.join(__dirname, `../renderer/${MAIN_WINDOW_VITE_NAME}`);
	protocol.handle(RENDERER_SCHEME, async (request) => {
		const url = new URL(request.url);
		if (url.host !== RENDERER_HOST) {
			return new Response("Not found", { status: 404 });
		}
		const resolved = path.resolve(path.join(distRoot, decodeURIComponent(url.pathname)));
		if (resolved !== distRoot && !resolved.startsWith(distRoot + path.sep)) {
			return new Response("Forbidden", { status: 403 });
		}
		const target = path.extname(resolved) === "" ? path.join(distRoot, "index.html") : resolved;
		try {
			const response = await net.fetch(pathToFileURL(target).toString());
			const headers = new Headers(response.headers);
			headers.set("Content-Security-Policy", rendererCspHeader());
			return new Response(response.body, { status: response.status, statusText: response.statusText, headers });
		} catch {
			return new Response("Not found", { status: 404 });
		}
	});
}

export function rendererUrl(): string {
	if (typeof MAIN_WINDOW_VITE_DEV_SERVER_URL !== "undefined" && MAIN_WINDOW_VITE_DEV_SERVER_URL) {
		return MAIN_WINDOW_VITE_DEV_SERVER_URL;
	}

	return `${RENDERER_ORIGIN}/index.html`;
}

export function preloadPath(): string {
	return path.join(__dirname, "preload.cjs");
}

export function annotatePreloadPath(): string {
	return path.join(__dirname, "annotate-preload.cjs");
}

// Runtime window/taskbar icon for Linux and Windows. macOS ignores this and
// uses the .app bundle's .icns instead. Packaged: shipped via extraResource to
// resources/icon.png.
// Unpackaged runs return undefined so the dev window keeps Electron's default
// icon and never impersonates the installed app's taskbar/dock icon (#3642).
export function windowIconPath(): string | undefined {
	if (!app.isPackaged) return undefined;
	const iconFile = process.platform === "win32" ? "icon.ico" : "icon.png";
	const candidate = path.join(process.resourcesPath, iconFile);
	if (existsSync(candidate)) return candidate;
	const fallback = path.join(process.resourcesPath, "icon.png");
	return existsSync(fallback) ? fallback : undefined;
}

// Electron 44 removed clipboard.writeImage in favour of the W3C-style
// ClipboardItem API; the browser host and chat images share this adapter.
export function writeClipboardImage(image: Electron.NativeImage): Promise<void> {
	return clipboard.write([
		new ClipboardItem({ "image/png": new Blob([new Uint8Array(image.toPNG())], { type: "image/png" }) }),
	]);
}

export function applyRuntimeAppIcon(): void {
	if (process.platform !== "darwin") return;
	// Unpackaged runs keep Electron's default dock icon so the dev window is
	// visually distinct from the installed app (#3642).
	if (!app.isPackaged) return;
	const iconPath = windowIconPath();
	if (!iconPath) return;
	const icon = nativeImage.createFromPath(iconPath);
	if (!icon.isEmpty()) {
		app.dock?.setIcon(icon);
	}
}
