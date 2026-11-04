import type { KeyboardEventHandler, PointerEventHandler, RefObject, WheelEventHandler } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../../lib/utils";
import type { useTimelineMinimap } from "./useTimelineMinimap";

type Scrollbar = ReturnType<typeof useTimelineMinimap>["scrollbar"];

export interface TimelineMinimapProps {
	scrollTrack: RefObject<HTMLDivElement | null>;
	/** Read at event time: the ref changes without re-rendering the Timeline. */
	inspectorOpenRef: RefObject<boolean>;
	minimapEnabled: boolean;
	minimapRevealed: boolean;
	scrollbar: Scrollbar;
	previews: Array<{ title: string; detail?: string }>;
	hoveredMarker: number | null;
	setHoveredMarker: (index: number | null) => void;
	onPointerDown: PointerEventHandler<HTMLDivElement>;
	onPointerMove: PointerEventHandler<HTMLDivElement>;
	stopDrag: PointerEventHandler<HTMLDivElement>;
	onWheel: WheelEventHandler<HTMLDivElement>;
	onKeyDown: KeyboardEventHandler<HTMLDivElement>;
}

/** The turn-tick scrollbar beside the transcript, with its hover preview. */
export function TimelineMinimap({
	scrollTrack,
	inspectorOpenRef,
	minimapEnabled,
	minimapRevealed,
	scrollbar,
	previews,
	hoveredMarker,
	setHoveredMarker,
	onPointerDown,
	onPointerMove,
	stopDrag,
	onWheel,
	onKeyDown,
}: TimelineMinimapProps) {
	const { t } = useTranslation();
	const inspectorOpen = inspectorOpenRef.current;
	return (
		<>
			<div
				ref={scrollTrack}
				role="scrollbar"
				data-testid="chat-conversation-minimap"
				data-inspector-open={inspectorOpen}
				tabIndex={minimapEnabled && !inspectorOpen ? 0 : -1}
				aria-hidden={inspectorOpen || undefined}
				aria-label={t("chat.timeline.scrollbarAria")}
				aria-orientation="vertical"
				aria-valuemin={0}
				aria-valuemax={100}
				aria-valuenow={scrollbar.percent}
				onPointerDown={onPointerDown}
				onPointerMove={onPointerMove}
				onPointerUp={stopDrag}
				onPointerCancel={stopDrag}
				onWheel={onWheel}
				onKeyDown={onKeyDown}
				onFocus={() => {
					if (!minimapEnabled || inspectorOpenRef.current || scrollbar.markers.length === 0) return;
					setHoveredMarker(
						Math.min(
							scrollbar.markers.length - 1,
							Math.round((scrollbar.percent / 100) * (scrollbar.markers.length - 1)),
						),
					);
				}}
				onBlur={() => setHoveredMarker(null)}
				onPointerLeave={() => setHoveredMarker(null)}
				className={cn(
					"group/scroll absolute inset-y-3 right-1 z-10 w-6 touch-none rounded-full outline-none focus-visible:ring-1 focus-visible:ring-logo-accent/60",
					minimapRevealed && "transition-opacity",
					minimapEnabled && !inspectorOpen ? "cursor-pointer opacity-100" : "pointer-events-none opacity-0",
					"data-[inspector-open=true]:pointer-events-none data-[inspector-open=true]:opacity-0",
				)}
			>
				<div className="absolute inset-0 cursor-grab group-active/scroll:cursor-grabbing">
					{minimapEnabled && !inspectorOpen
						? scrollbar.markers.map((marker, index) => {
								const distance = hoveredMarker === null ? Number.POSITIVE_INFINITY : Math.abs(index - hoveredMarker);
								return (
									<span
										key={index}
										data-chat-scroll-marker=""
										data-scroll-target={marker.scrollTop}
										data-marker-index={index}
										onPointerEnter={() => setHoveredMarker(index)}
										className="chat-scroll-marker-hit"
										style={{ top: marker.top }}
									>
										<span
											aria-hidden="true"
											className={cn(
												"chat-scroll-marker",
												marker.visible && "chat-scroll-marker-visible",
												distance === 0 && "chat-scroll-marker-active",
												distance === 1 && "chat-scroll-marker-adjacent",
												distance === 2 && "chat-scroll-marker-near",
											)}
										/>
									</span>
								);
							})
						: null}
				</div>

				{minimapEnabled && hoveredMarker !== null && scrollbar.markers[hoveredMarker] && previews[hoveredMarker] ? (
					<div
						role="tooltip"
						className="chat-scroll-preview pointer-events-none absolute right-full z-20 mr-3 w-80 rounded-xl border border-border-strong bg-raised px-3.5 py-3 shadow-lg"
						style={{
							top: `clamp(4rem, ${scrollbar.markers[hoveredMarker].top}px, calc(100% - 4rem))`,
						}}
					>
						<strong className="line-clamp-1 text-sm font-medium leading-snug text-foreground">
							{previews[hoveredMarker].title}
						</strong>
						{previews[hoveredMarker].detail ? (
							<p className="mt-1.5 line-clamp-3 text-xs leading-relaxed text-muted-foreground">
								{previews[hoveredMarker].detail}
							</p>
						) : null}
					</div>
				) : null}
			</div>
		</>
	);
}
