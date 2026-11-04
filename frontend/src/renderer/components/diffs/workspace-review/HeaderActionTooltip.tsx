/** Tooltip wrapper for review header actions. */
import { type ReactElement } from "react";
import { Tooltip, TooltipContent, TooltipTrigger } from "../../ui/tooltip";

export function HeaderActionTooltip({ children, label }: { children: ReactElement; label: string }) {
	return (
		<Tooltip>
			<TooltipTrigger asChild>{children}</TooltipTrigger>
			<TooltipContent side="bottom">{label}</TooltipContent>
		</Tooltip>
	);
}
