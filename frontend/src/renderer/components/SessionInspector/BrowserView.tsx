import { useTranslation } from "react-i18next";
import { inspectorEmptyClass } from "@tallpine/product-ui";
import type { WorkspaceSession } from "../../types/workspace";
import { BrowserPanelView } from "../BrowserPanel";
import { type BrowserAnnotationQueueModel } from "../browser/browser-types";
import type { BrowserViewModel } from "../../hooks/useBrowserView";
import { Button } from "../ui/button";
import { cn } from "../../lib/utils";

export function BrowserView({
	session,
	isActive,
	browserPoppedOut,
	browserAnnotationQueue,
	onTogglePopOut,
	browserView,
	topbarHost,
}: {
	session: WorkspaceSession;
	isActive: boolean;
	browserPoppedOut: boolean;
	browserAnnotationQueue?: BrowserAnnotationQueueModel;
	onTogglePopOut?: (next: boolean) => void;
	browserView?: BrowserViewModel;
	topbarHost?: HTMLElement | null;
}) {
	// While maximized, the browser is a full-window overlay that covers the rail,
	// so the inspector's Browser tab has nothing to show (and must not mount a
	// second BrowserPanelView — it would fight the overlay over the shared native
	// view slot). Exit is via the overlay's own minimize button.
	const { t } = useTranslation();
	if (browserPoppedOut) {
		return (
			<div className="h-full min-h-0" data-browser-dock-target="" role="tabpanel">
				<div className={cn(inspectorEmptyClass, "flex flex-col items-center gap-2 py-10 px-5 text-center")}>
					<p className="text-md-sm text-muted-foreground">{t("inspector.browserInCenter")}</p>
					<Button onClick={() => onTogglePopOut?.(false)} size="sm" type="button" variant="outline">
						{t("inspector.returnToPanel")}
					</Button>
				</div>
			</div>
		);
	}

	if (!browserView || !browserAnnotationQueue) {
		return null;
	}

	return (
		<BrowserPanelView
			active={isActive}
			annotationQueue={browserAnnotationQueue}
			browserView={browserView}
			onTogglePopOut={(next) => onTogglePopOut?.(next)}
			poppedOut={false}
			session={session}
			topbarHost={topbarHost}
		/>
	);
}
