import { useEffect, useRef, useState, type ReactNode } from "react";
import { Paperclip } from "lucide-react";
import { useTranslation } from "react-i18next";
import { dragCarriesFiles, firstDragEntryIsDirectory } from "../../lib/dropClassify";

type ChatDropOverlayProps = {
	/** Returns false when the files could not be attached (nothing is claimed then). */
	onFiles: (files: File[]) => boolean;
	/** Drags are ignored while attaching is unavailable. */
	enabled: boolean;
	className?: string;
	children: ReactNode;
};

/**
 * Wraps the whole chat pane so files can be dropped anywhere on it, not only on
 * the composer. While a file drag is over the pane a "Drop to attach" overlay
 * covers it; Escape cancels the drag state.
 */
export function ChatDropOverlay({ onFiles, enabled, className, children }: ChatDropOverlayProps) {
	const { t } = useTranslation();
	const [active, setActive] = useState(false);
	// dragenter/dragleave fire for every child boundary: count them to find the real exit.
	const depth = useRef(0);
	// Escape hides the overlay for the rest of this drag; a new drag shows it again.
	const cancelled = useRef(false);

	// Folders are left alone so they bubble to the app-wide drop that adds a project.
	const wantsDrag = (dataTransfer: DataTransfer | null) =>
		enabled && dragCarriesFiles(dataTransfer) && !firstDragEntryIsDirectory(dataTransfer);

	const reset = () => {
		depth.current = 0;
		cancelled.current = false;
		setActive(false);
	};

	useEffect(() => {
		if (!active) return;
		const onKey = (event: KeyboardEvent) => {
			if (event.key !== "Escape") return;
			event.preventDefault();
			cancelled.current = true;
			setActive(false);
		};
		// Capture phase: the composer's own drop handler stops propagation, and a drop
		// outside the pane or a drag ended by the OS never reaches the pane's handlers.
		window.addEventListener("keydown", onKey);
		window.addEventListener("dragend", reset, true);
		window.addEventListener("drop", reset, true);
		return () => {
			window.removeEventListener("keydown", onKey);
			window.removeEventListener("dragend", reset, true);
			window.removeEventListener("drop", reset, true);
		};
	}, [active]);

	useEffect(() => {
		if (!enabled && active) reset();
	}, [enabled, active]);

	return (
		<div
			className={className ?? "relative flex min-h-0 flex-1 flex-col"}
			onDragEnter={(event) => {
				if (!wantsDrag(event.dataTransfer)) return;
				depth.current += 1;
				if (!cancelled.current) setActive(true);
			}}
			onDragOver={(event) => {
				if (!wantsDrag(event.dataTransfer)) return;
				// Required for the pane to be a drop target.
				event.preventDefault();
			}}
			onDragLeave={(event) => {
				if (!dragCarriesFiles(event.dataTransfer)) return;
				depth.current = Math.max(0, depth.current - 1);
				if (depth.current === 0) {
					cancelled.current = false;
					setActive(false);
				}
			}}
			onDrop={(event) => {
				const hadFiles = dragCarriesFiles(event.dataTransfer) && !firstDragEntryIsDirectory(event.dataTransfer);
				const wasCancelled = cancelled.current;
				reset();
				if (!enabled || !hadFiles || wasCancelled) return;
				const files = Array.from(event.dataTransfer.files ?? []);
				if (files.length === 0) return;
				event.preventDefault();
				if (onFiles(files)) event.stopPropagation();
			}}
		>
			{children}
			{active ? (
				<div
					role="presentation"
					data-testid="chat-drop-overlay"
					className="pointer-events-none absolute inset-2 z-30 flex flex-col items-center justify-center gap-2 rounded-lg border-2 border-dashed border-logo-accent bg-background/80 text-sm text-foreground"
				>
					<Paperclip aria-hidden="true" className="size-5" />
					<span>{t("chat.dropOverlay.title")}</span>
					<span className="text-xs text-muted-foreground">{t("chat.dropOverlay.hint")}</span>
				</div>
			) : null}
		</div>
	);
}
