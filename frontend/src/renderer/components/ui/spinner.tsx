import type { ComponentType, HTMLAttributes, SVGProps } from "react";
import { cn } from "../../lib/utils";

/**
 * Spinning icon. The rotation runs on a wrapper <span>, never on the <svg>: Chromium
 * does not composite a CSS transform animation on an <svg> root, so a bare animated
 * lucide icon forces a main-thread frame (style recalc + layerize + commit) on every
 * vsync, repainting the whole page while it is on screen (B331). A span animates on
 * the compositor, so an idle spinner costs the main thread nothing.
 */
export function Spinner({ icon: Icon, className, strokeWidth, ...rest }: {
	icon: ComponentType<SVGProps<SVGSVGElement>>;
	className?: string;
	strokeWidth?: number;
} & Omit<HTMLAttributes<HTMLSpanElement>, "className">) {
	return (
		// A labelled spinner is an image to assistive tech (the label used to sit on the
		// svg); an unlabelled one is decorative. A bare generic span cannot carry a name.
		<span
			className={cn("inline-flex shrink-0 animate-spin motion-reduce:animate-none", className)}
			{...(rest["aria-label"] ? { role: "img" } : { "aria-hidden": true })}
			{...rest}
		>
			<Icon strokeWidth={strokeWidth} aria-hidden="true" className={/(^|\s)size-/.test(className ?? "") ? "size-full" : undefined} />
		</span>
	);
}
