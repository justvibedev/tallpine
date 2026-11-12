/**
 * The full-size viewer a chat image or thumbnail opens.
 *
 * It pages through the images of one message (or one tool row), zooms 1-5x with
 * the buttons, `+`/`-`, the wheel or a double-click, pans a zoomed image by
 * dragging, and copies (`C`) or downloads (`D`) the image on screen.
 */

import { useCallback, useEffect, useRef, useState, type PointerEvent, type WheelEvent } from "react";
import { useTranslation } from "react-i18next";
import { ChevronLeft, ChevronRight, Copy, Download, Minus, Plus } from "lucide-react";
import { mediaCrossOrigin } from "../../lib/file-media";
import { cn } from "../../lib/utils";
import { useUiStore } from "../../stores/ui-store";
import { Button } from "../ui/button";
import { Dialog, DialogContent, DialogTitle } from "../ui/dialog";
import { copyChatImage, saveChatImage } from "./chat-image-actions";
import { runImageAction } from "./chat-image-run";
import type { ChatImageItem } from "./chat-image-set";

export const MIN_ZOOM = 1;
export const MAX_ZOOM = 5;
const ZOOM_STEP = 0.5;

const clampZoom = (zoom: number) => Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, zoom));

type Point = { x: number; y: number };
const ORIGIN: Point = { x: 0, y: 0 };

/** Mounted afresh for each opening, so index, zoom and pan always start clean. */
export function ChatImageViewer({ items, index = 0, open, onOpenChange }: { items: ChatImageItem[]; index?: number; open: boolean; onOpenChange: (open: boolean) => void }) {
	return open && items.length > 0 ? <Lightbox items={items} initialIndex={index} onOpenChange={onOpenChange} /> : null;
}

function Lightbox({ items, initialIndex, onOpenChange }: { items: ChatImageItem[]; initialIndex: number; onOpenChange: (open: boolean) => void }) {
	const { t } = useTranslation();
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const [index, setIndex] = useState(initialIndex);
	const [zoom, setZoom] = useState(MIN_ZOOM);
	const [pan, setPan] = useState<Point>(ORIGIN);
	const drag = useRef<{ pointer: number; start: Point; origin: Point } | null>(null);
	const stage = useRef<HTMLDivElement>(null);

	const current = items[Math.min(index, items.length - 1)];
	const count = items.length;

	const reset = useCallback(() => {
		setZoom(MIN_ZOOM);
		setPan(ORIGIN);
	}, []);
	const go = useCallback(
		(delta: number) => {
			setIndex((value) => Math.min(count - 1, Math.max(0, value + delta)));
			reset();
		},
		[count, reset],
	);
	// Panning is bounded by the room the zoom adds beyond the stage; unmeasured (zero) stages are left free.
	const clampPan = useCallback((value: Point, level: number): Point => {
		const box = stage.current?.getBoundingClientRect();
		if (!box || (box.width === 0 && box.height === 0)) return level === MIN_ZOOM ? ORIGIN : value;
		const limitX = ((level - 1) * box.width) / 2;
		const limitY = ((level - 1) * box.height) / 2;
		return { x: Math.min(limitX, Math.max(-limitX, value.x)), y: Math.min(limitY, Math.max(-limitY, value.y)) };
	}, []);
	const zoomTo = useCallback(
		(next: number) => {
			const level = clampZoom(next);
			setZoom(level);
			setPan((value) => clampPan(value, level));
		},
		[clampPan],
	);

	// On the window, not the dialog: a button that disables itself at the end of
	// the set (Next) drops focus out of the dialog, and the arrows must keep working.
	const keys = useRef<(event: globalThis.KeyboardEvent) => void>(() => {});
	useEffect(() => {
		const listener = (event: globalThis.KeyboardEvent) => keys.current(event);
		window.addEventListener("keydown", listener);
		return () => window.removeEventListener("keydown", listener);
	}, []);

	if (!current) return null;
	const copy = () => runImageAction(showGlobalToast, () => copyChatImage(current.url), t("chat.image.copied"), t("chat.image.copyFailed"));
	const download = () => runImageAction(showGlobalToast, () => saveChatImage(current.url, current.label), null, t("chat.image.saveFailed"));

	const onKeyDown = (event: globalThis.KeyboardEvent) => {
		if (event.altKey) return;
		const key = event.key.toLowerCase();
		if (key === "arrowleft") go(-1);
		else if (key === "arrowright") go(1);
		else if (key === "+" || key === "=") zoomTo(zoom + ZOOM_STEP);
		else if (key === "-") zoomTo(zoom - ZOOM_STEP);
		else if (key === "0") reset();
		else if (key === "c" && !((event.metaKey || event.ctrlKey) && window.getSelection()?.toString())) copy();
		else if (key === "d" && !event.metaKey && !event.ctrlKey) download();
		else return;
		event.preventDefault();
	};
	keys.current = onKeyDown;
	const onWheel = (event: WheelEvent) => zoomTo(zoom * Math.exp(-event.deltaY * 0.002));
	const onPointerDown = (event: PointerEvent) => {
		if (zoom === MIN_ZOOM || event.button !== 0) return;
		drag.current = { pointer: event.pointerId, start: { x: event.clientX, y: event.clientY }, origin: pan };
		event.currentTarget.setPointerCapture?.(event.pointerId);
	};
	const onPointerMove = (event: PointerEvent) => {
		const state = drag.current;
		if (!state || state.pointer !== event.pointerId) return;
		setPan(clampPan({ x: state.origin.x + event.clientX - state.start.x, y: state.origin.y + event.clientY - state.start.y }, zoom));
	};
	const endDrag = () => {
		drag.current = null;
	};

	const iconButton = "text-muted-foreground hover:text-foreground";
	return (
		<Dialog open onOpenChange={onOpenChange}>
			<DialogContent
				aria-describedby={undefined}
				className="z-overlay w-auto max-w-[calc(100vw-4rem)] gap-2 bg-popover p-2 pt-10"
			>
				<DialogTitle className="sr-only">{current.label || t("chat.image.untitled")}</DialogTitle>
				<div className="absolute left-2 top-2 flex items-center gap-0.5" role="toolbar" aria-label={t("chat.image.toolbar")}>
					{count > 1 ? (
						<>
							<Button type="button" variant="ghost" size="icon-sm" className={iconButton} disabled={index === 0} onClick={() => go(-1)} aria-label={t("chat.image.previous")}>
								<ChevronLeft aria-hidden="true" />
							</Button>
							<span className="min-w-10 text-center font-mono text-caption tabular-nums text-muted-foreground" aria-live="polite">
								{t("chat.image.position", { index: index + 1, count })}
							</span>
							<Button type="button" variant="ghost" size="icon-sm" className={iconButton} disabled={index === count - 1} onClick={() => go(1)} aria-label={t("chat.image.next")}>
								<ChevronRight aria-hidden="true" />
							</Button>
						</>
					) : null}
					<Button type="button" variant="ghost" size="icon-sm" className={iconButton} disabled={zoom <= MIN_ZOOM} onClick={() => zoomTo(zoom - ZOOM_STEP)} aria-label={t("chat.image.zoomOut")}>
						<Minus aria-hidden="true" />
					</Button>
					<button
						type="button"
						onClick={reset}
						disabled={zoom === MIN_ZOOM && pan.x === 0 && pan.y === 0}
						aria-label={t("chat.image.zoomReset")}
						className="min-w-10 rounded-sm text-center font-mono text-caption tabular-nums text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:pointer-events-none"
					>
						{Math.round(zoom * 100)}%
					</button>
					<Button type="button" variant="ghost" size="icon-sm" className={iconButton} disabled={zoom >= MAX_ZOOM} onClick={() => zoomTo(zoom + ZOOM_STEP)} aria-label={t("chat.image.zoomIn")}>
						<Plus aria-hidden="true" />
					</Button>
					<Button type="button" variant="ghost" size="icon-sm" className={iconButton} onClick={copy} aria-label={t("chat.image.copy")}>
						<Copy aria-hidden="true" />
					</Button>
					<Button type="button" variant="ghost" size="icon-sm" className={iconButton} onClick={download} aria-label={t("chat.image.save")}>
						<Download aria-hidden="true" />
					</Button>
				</div>
				<div
					ref={stage}
					data-chat-lightbox-stage=""
					onWheel={onWheel}
					onPointerDown={onPointerDown}
					onPointerMove={onPointerMove}
					onPointerUp={endDrag}
					onPointerCancel={endDrag}
					onDoubleClick={() => (zoom === MIN_ZOOM ? zoomTo(2) : reset())}
					className={cn("flex items-center justify-center overflow-hidden rounded-md", zoom > MIN_ZOOM ? "cursor-grab active:cursor-grabbing" : "cursor-zoom-in")}
				>
					<img
						key={current.url}
						crossOrigin={mediaCrossOrigin(current.url)}
						src={current.src}
						alt={current.label}
						draggable={false}
						style={{ transform: `translate(${pan.x}px, ${pan.y}px) scale(${zoom})` }}
						className="block max-h-[calc(100svh-8rem)] max-w-full select-none rounded-md object-contain"
					/>
				</div>
			</DialogContent>
		</Dialog>
	);
}
