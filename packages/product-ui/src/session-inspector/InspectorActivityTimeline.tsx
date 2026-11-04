/** Activity timeline for the inspector summary. */
import { type ReactNode } from "react";
import { cn } from "../utils";

export type InspectorTimelineTone = "now" | "good" | "warn" | "neutral";

export type InspectorTimelineEvent = {
	content: ReactNode;
	markerBreathe?: boolean;
	markerTone?: string;
	timestamp: string | null;
	tone: InspectorTimelineTone;
};

export const timelineNodeTone: Record<InspectorTimelineTone, string> = {
	neutral: "bg-passive shadow-timeline-dot",
	now: "bg-working shadow-timeline-dot-now",
	good: "bg-success shadow-timeline-dot",
	warn: "bg-warning shadow-timeline-dot",
};

export function InspectorActivityTimelineView({ events }: { events: InspectorTimelineEvent[] }) {
	return (
		<div className="flex flex-col">
			{events.map((event, index) => (
				<div key={index} className="flex min-h-7 items-center gap-2.5" data-testid="inspector-timeline-event">
					<span
						aria-hidden="true"
						className={cn(
							"size-1.5 shrink-0 rounded-full",
							timelineNodeTone[event.tone],
							event.markerBreathe && "animate-status-pulse",
						)}
						style={event.markerTone ? { background: event.markerTone } : undefined}
					/>
					<div className="min-w-0 flex-1 truncate text-sm text-foreground [&_b]:font-semibold">{event.content}</div>
					{event.timestamp ? (
						<span className="shrink-0 font-mono text-xs tabular-nums text-passive">{event.timestamp}</span>
					) : null}
				</div>
			))}
		</div>
	);
}
