import { cn } from "../../lib/utils";
import type { BrowserPanelViewState } from "./browser-types";
import { StaticPreview } from "./browser-helpers";

/** Browser viewport body. */
export function BrowserBody({ v }: { v: BrowserPanelViewState }) {
	const {
		deviceFrameWidth,
		hasNativeBrowser,
		navState,
		showStaticPreview,
		slotRef,
		t,
	} = v;
	return (
		<>
			<div className="browser-panel__body flex min-h-0 flex-1 overflow-hidden">
				<div
					className="browser-panel__viewport relative min-h-0 flex-1 overflow-hidden"
					// The live page paints as a separate native WebContentsView, not inside
					// this div. Opening any browser overlay (e.g. the controls menu) briefly
					// raises the transparent shell above that native view so the overlay can
					// paint on top — if this div painted an opaque background here, it would
					// blank the live page for the duration. `.browser-panel__viewport` in
					// styles.css carries its own plain-CSS background for the empty/no-bridge
					// placeholder states that is NOT a Tailwind utility and so can't be
					// toggled via className — Tailwind utilities live in a lower-priority
					// cascade layer and can never override plain author CSS. Gate that CSS
					// rule with this data attribute instead, so there's exactly one place
					// deciding opacity.
					data-placeholder={!hasNativeBrowser || navState.url === "" ? "true" : undefined}
					data-testid="browser-viewport"
				>
					{/* Only the native-view slot is width-constrained for a device
					    preset — the empty/error placeholders below stay full-width
					    overlays. maxWidth caps it to whatever room the panel actually
					    has instead of overflowing a narrow docked panel. */}
					<div
						className={cn("relative mx-auto h-full", deviceFrameWidth && "border-x border-border shadow-(--shadow-popover)")}
						style={deviceFrameWidth ? { maxWidth: "100%", width: deviceFrameWidth } : undefined}
					>
						<div
							className="browser-panel__slot absolute inset-0 min-h-px min-w-px"
							data-testid="browser-device-frame"
							ref={slotRef}
						/>
					</div>
					{showStaticPreview ? <StaticPreview url={navState.url} /> : null}
					{navState.url === "" ? (
						<div className="pointer-events-none absolute inset-0 grid place-items-center p-5 text-center text-xs text-muted-foreground">
							<p>{t("browser.emptyUrl")}</p>
						</div>
					) : null}
					{navState.error ? (
						<p
							className={cn(
								"absolute inset-x-2.5 bottom-2.5 m-0 border border-error/35 bg-error/8 px-2.5 py-2",
								"rounded-md text-xs text-destructive",
							)}
							data-testid="browser-preview-error"
						>
							{navState.error}
						</p>
					) : null}
				</div>
			</div>
		</>
	);
}
