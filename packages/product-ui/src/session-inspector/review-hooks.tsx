/** Click-away and overflow hooks plus review links. */
import { useEffect, useRef, useState } from "react";
import { InspectorReviewLabels } from "./review-types";

export function useClickAway<T extends HTMLElement>(open: boolean, onDismiss: () => void) {
	const ref = useRef<T | null>(null);
	const onDismissRef = useRef(onDismiss);
	onDismissRef.current = onDismiss;
	useEffect(() => {
		if (!open) return;
		const onPointerDown = (event: PointerEvent) => {
			if (!ref.current?.contains(event.target as Node)) onDismissRef.current();
		};
		document.addEventListener("pointerdown", onPointerDown);
		return () => document.removeEventListener("pointerdown", onPointerDown);
	}, [open]);
	return ref;
}

export function ReviewLinks({
	clamped,
	expanded,
	labels,
	onExpandedChange,
}: {
	clamped: boolean;
	expanded: boolean;
	labels: InspectorReviewLabels;
	onExpandedChange: () => void;
}) {
	if (!clamped) return null;
	return (
		<span className="mt-1 flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-1 text-micro text-passive">
			<button className="font-medium transition-colors hover:text-foreground" onClick={onExpandedChange} type="button">
				{expanded ? labels.showLess : labels.showMore}
			</button>
		</span>
	);
}

export function useRenderedOverflow<T extends HTMLElement>(contentKey: string, axis: "horizontal" | "vertical", active = true) {
	const ref = useRef<T | null>(null);
	const [isOverflowing, setIsOverflowing] = useState(false);
	useEffect(() => {
		if (!active) return;
		const element = ref.current;
		if (!element) return;
		const update = () => {
			const next = axis === "horizontal"
				? element.scrollWidth > element.clientWidth + 1
				: element.scrollHeight > element.clientHeight + 1;
			setIsOverflowing(next);
		};
		update();
		window.addEventListener("resize", update);
		const observer = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(update);
		observer?.observe(element);
		return () => {
			window.removeEventListener("resize", update);
			observer?.disconnect();
		};
	}, [active, axis, contentKey]);
	return { ref, isOverflowing };
}
