import { useCallback, useLayoutEffect, useRef, useState, type CSSProperties, type ReactNode } from "react";

export function SidebarSectionScroller({
	children,
	className,
	style,
	testId,
	wrapperClassName,
}: {
	children: ReactNode;
	className: string;
	style?: CSSProperties;
	testId: string;
	wrapperClassName?: string;
}) {
	const scrollerRef = useRef<HTMLDivElement>(null);
	const [scrollEdges, setScrollEdges] = useState({ top: false, bottom: false });
	const updateScrollEdges = useCallback(() => {
		const scroller = scrollerRef.current;
		if (!scroller) return;
		const overflow = scroller.scrollHeight - scroller.clientHeight;
		const next = {
			top: overflow > 1 && scroller.scrollTop > 1,
			bottom: overflow > 1 && scroller.scrollTop < overflow - 1,
		};
		setScrollEdges((current) => (current.top === next.top && current.bottom === next.bottom ? current : next));
	}, []);

	useLayoutEffect(() => {
		const scroller = scrollerRef.current;
		if (!scroller) return;
		updateScrollEdges();
		scroller.addEventListener("scroll", updateScrollEdges, { passive: true });
		const resizeObserver = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(updateScrollEdges);
		resizeObserver?.observe(scroller);
		if (scroller.firstElementChild) resizeObserver?.observe(scroller.firstElementChild);
		// Rows change on most session events. Measuring inside the mutation callback
		// forced a synchronous layout per commit; once per frame is enough for the fades.
		let edgesFrame: number | undefined;
		const mutationObserver =
			typeof MutationObserver === "undefined"
				? undefined
				: new MutationObserver(() => {
					if (scroller.firstElementChild) resizeObserver?.observe(scroller.firstElementChild);
					if (edgesFrame !== undefined) return;
					edgesFrame = requestAnimationFrame(() => {
						edgesFrame = undefined;
						updateScrollEdges();
					});
				});
		mutationObserver?.observe(scroller, { childList: true, subtree: true });
		return () => {
			scroller.removeEventListener("scroll", updateScrollEdges);
			resizeObserver?.disconnect();
			mutationObserver?.disconnect();
			if (edgesFrame !== undefined) cancelAnimationFrame(edgesFrame);
		};
	}, [updateScrollEdges]);

	return (
		<div className={`relative min-h-0 ${wrapperClassName ?? ""}`}>
			<div ref={scrollerRef} className={className} data-sidebar-section-scroller="" data-testid={testId} style={style}>
				{children}
			</div>
			{scrollEdges.top ? <div aria-hidden="true" className="sidebar-section-scroll-fade sidebar-section-scroll-fade--top" /> : null}
			{scrollEdges.bottom ? <div aria-hidden="true" className="sidebar-section-scroll-fade sidebar-section-scroll-fade--bottom" /> : null}
		</div>
	);
}
