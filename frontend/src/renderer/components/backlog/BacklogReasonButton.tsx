import { Button } from "../ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";

type BacklogReasonButtonProps = React.ComponentProps<typeof Button> & {
	/** Why the button is disabled; shown on hover or focus of its wrapper. A button with a reason is disabled. */
	disabledReason?: string;
};

/** A button that explains itself when it cannot be used (DESIGN section 9: disabled states say why). */
export function BacklogReasonButton({ disabledReason, children, ...props }: BacklogReasonButtonProps) {
	if (!disabledReason) return <Button {...props}>{children}</Button>;
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				{/* A disabled button gets no pointer or focus events, so the wrapper carries the tooltip. */}
				<span tabIndex={0} className="inline-flex rounded focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60">
					<Button {...props} disabled aria-disabled="true" tabIndex={-1}>{children}</Button>
				</span>
			</TooltipTrigger>
			<TooltipContent>{disabledReason}</TooltipContent>
		</Tooltip>
	);
}
