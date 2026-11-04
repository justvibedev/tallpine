import { useRef } from "react";
import { useTranslation } from "react-i18next";
import { X } from "lucide-react";
import { cn } from "@/lib/utils";
import { useFastMemorySampling } from "../hooks/useSessionMemory";
import { Button } from "./ui/button";
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogTitle, settingsDialogBodyClass, settingsDialogContentClass, settingsDialogHeaderClass } from "./ui/dialog";
import { DiagnosticsBody } from "./session-memory/SessionMemoryDiagnostics";

export function SessionMemoryPanel({
	onOpenChange,
	open,
	projectId,
}: {
	onOpenChange: (open: boolean) => void;
	open: boolean;
	projectId?: string;
}) {
	const { t } = useTranslation();
	useFastMemorySampling();
	const scroller = useRef<HTMLDivElement>(null);
	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent className={cn(settingsDialogContentClass, "w-[min(52rem,calc(100vw-var(--space-8)))]")} showCloseButton={false}>
				<div className={cn(settingsDialogHeaderClass, "flex h-auto flex-row items-center justify-between border-b-0 pb-3")}>
					<div className="min-w-0 flex-1">
						<DialogTitle className="text-lg font-semibold leading-6 text-settings-label">{t("shell.memoryPanelTitle")}</DialogTitle>
						<DialogDescription className="sr-only">{t("shell.memoryPanelDescription")}</DialogDescription>
					</div>
					<DialogClose asChild>
						<Button aria-label={t("common.close")} size="icon" variant="ghost">
							<X className="size-icon-md" aria-hidden="true" />
						</Button>
					</DialogClose>
				</div>
				<div
					className={cn(settingsDialogBodyClass, "settings-dialog-body min-h-0 flex-1 gap-(--size-settings-section-gap,1.5rem) overscroll-contain px-(--size-modal-padding) pt-0")}
					ref={scroller}
				>
					<DiagnosticsBody projectId={projectId} scroller={scroller} />
				</div>
			</DialogContent>
		</Dialog>
	);
}

