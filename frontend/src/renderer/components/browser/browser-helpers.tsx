/** Pure labels and URL helpers plus small icon and preview components. */
import { PRODUCT_NAME } from "../../../shared/brand";
import { useEffect, useState } from "react";
import { Globe2 } from "lucide-react";
import { type BrowserViewModel } from "../../hooks/useBrowserView";
import { appI18n, type MessageKey } from "../../i18n";
import { RemoteImg } from "../RemoteImg";

export function agentActivityLabel(activity: BrowserViewModel["agentBrowserActivity"], active: boolean): string {
	if (!active && !activity?.active) return "";
	const action = activity?.active ? activity.action : "";
	if (!action) return appI18n.t("browser.agentUsing");
	return appI18n.t("browser.agentAction", { verb: browserActionVerb(action) });
}

// The daemon's refusal codes (backend/internal/browserruntime/broker.go). Name a
// cause only for a code that identifies one; any other refusal stays generic.
export function browserRuntimeRejectedKey(code: string | undefined): MessageKey {
	switch (code) {
		case "BROWSER_RUNTIME_TOKEN_REJECTED":
			return "browser.runtimeRejectedToken";
		case "BROWSER_RUNTIME_PROTOCOL_MISMATCH":
			return "browser.runtimeRejectedVersion";
		default:
			return "browser.runtimeRejected";
	}
}

export function browserActionVerb(action: string): string {
	const key = ((): MessageKey => {
		switch (action) {
			case "click":
				return "browser.verb.click";
			case "fill":
			case "type":
				return "browser.verb.type";
			case "press":
				return "browser.verb.press";
			case "hover":
				return "browser.verb.hover";
			case "scroll":
				return "browser.verb.scroll";
			case "open":
				return "browser.verb.open";
			case "wait":
				return "browser.verb.wait";
			case "snapshot":
				return "browser.verb.read";
			case "highlight":
				return "browser.verb.highlight";
			case "unhighlight":
				return "browser.verb.clearHighlight";
			case "tab-new":
				return "browser.verb.openTab";
			case "tab-select":
				return "browser.verb.switchTab";
			case "tab-close":
				return "browser.verb.closeTab";
			case "tabs":
				return "browser.verb.checkTabs";
			default:
				return "browser.verb.using";
		}
	})();
	return appI18n.t(key);
}

export function getDisplayUrl(url: string): string {
	if (!url) return url;
	try {
		const parsed = new URL(url);
		if (parsed.protocol === "http:" || parsed.protocol === "https:") {
			return parsed.hostname.replace(/^www\./, "");
		}
		return url;
	} catch {
		return url;
	}
}


export function webOrigin(url: string): string | undefined {
	try {
		const parsed = new URL(url);
		return parsed.protocol === "http:" || parsed.protocol === "https:" ? parsed.origin : undefined;
	} catch {
		return undefined;
	}
}

export function faviconForOpenTab(url: string, tabs: BrowserViewModel["tabs"]): string | undefined {
	const origin = webOrigin(url);
	if (!origin) return undefined;
	return tabs.find((tab) => tab.favicon && webOrigin(tab.url) === origin)?.favicon;
}

export function BrowserSuggestionIcon({ cachedFavicon, url, viewId }: { cachedFavicon?: string; url: string; viewId: string }) {
	const origin = webOrigin(url);
	const nativeCompositionEnabled = window.tallpine?.browser.nativeCompositionEnabled === true;
	const directFavicon = !nativeCompositionEnabled && origin ? `${origin}/favicon.ico` : undefined;
	const [favicon, setFavicon] = useState(cachedFavicon ?? directFavicon);
	useEffect(() => {
		setFavicon(cachedFavicon ?? directFavicon);
		const historyFavicon = window.tallpine?.browser.historyFavicon;
		if (cachedFavicon || !nativeCompositionEnabled || !viewId || typeof historyFavicon !== "function") return;
		let current = true;
		void historyFavicon({ viewId, url }).then(
			(nextFavicon) => {
				if (current && nextFavicon) setFavicon(nextFavicon);
			},
			() => undefined,
		);
		return () => {
			current = false;
		};
	}, [cachedFavicon, directFavicon, nativeCompositionEnabled, url, viewId]);
	if (!favicon) {
		return <Globe2 aria-hidden="true" className="size-icon-base shrink-0 text-settings-muted" />;
	}
	return (
		<RemoteImg
			alt=""
			className="size-icon-base shrink-0 rounded-sm object-contain"
			onError={() => setFavicon(undefined)}
			src={favicon}
		/>
	);
}

export function StaticPreview({ url }: { url: string }) {
	return (
		<div className="absolute inset-0 overflow-auto bg-background text-foreground">
			<div className="border-b border-border bg-surface px-4 py-3">
				<div className="text-caption font-semibold uppercase tracking-wide-md text-muted-foreground">{PRODUCT_NAME} Preview</div>
				<div className="mt-1 truncate font-mono text-xs text-accent">{url}</div>
			</div>
			<div className="mx-auto max-w-preview-max px-5 py-6">
				<div className="rounded-lg border border-border bg-card p-5 shadow-sm">
					<div className="flex items-center justify-between gap-3">
						<div>
							<h1 className="text-heading-lg font-semibold leading-tight tracking-normal text-foreground">
								Demo app preview
							</h1>
							<p className="mt-1 text-control leading-row text-muted-foreground">
								The worker exposed a local Vite app with <span className="font-mono">tallpine preview</span>.
							</p>
						</div>
						<span className="rounded-md bg-success/15 px-2.5 py-1 text-caption font-semibold text-success">
							Loaded
						</span>
					</div>
					<div className="mt-5 grid grid-cols-3 gap-3">
						{[
							["Routes", "12 passing"],
							["Build", "ready"],
							["Latency", "42 ms"],
						].map(([label, value]) => (
							<div key={label} className="rounded-md border border-border bg-raised p-3">
								<div className="text-caption font-medium uppercase tracking-wide text-muted-foreground">{label}</div>
								<div className="mt-1 text-subtitle font-semibold text-foreground">{value}</div>
							</div>
						))}
					</div>
					<div className="mt-5 rounded-md border border-border bg-terminal p-3 font-mono text-xs leading-row text-terminal-dim">
						<div>$ npm run dev -- --host 127.0.0.1</div>
						<div className="text-success-bright">ready in 418 ms</div>
						<div>Local: http://localhost:5173/</div>
					</div>
				</div>
			</div>
		</div>
	);
}
