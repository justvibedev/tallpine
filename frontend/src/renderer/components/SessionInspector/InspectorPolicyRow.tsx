import { Info } from "lucide-react";
import { cn } from "../../lib/utils";
import { Switch } from "../ui/switch";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";

export function InspectorPolicyRow({
	id,
	label,
	ariaLabel = label,
	description,
	tooltipClassName,
	checked,
	disabled,
	onCheckedChange,
}: {
	id: string;
	label: string;
	ariaLabel?: string;
	description?: string;
	tooltipClassName?: string;
	checked: boolean;
	disabled: boolean;
	onCheckedChange: (checked: boolean) => void;
}) {
	return (
		<div className="flex min-h-8 items-center justify-between gap-3 py-1" data-slot="inspector-policy-row">
			<div className="flex min-w-0 items-center gap-1.5">
				<label className="min-w-0 text-sm text-settings-label" htmlFor={id}>
					{label}
				</label>
				{description ? (
					<Tooltip>
						<TooltipTrigger asChild>
							<button
								aria-label={description}
								className="inline-flex size-5 shrink-0 items-center justify-center rounded-full text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
								type="button"
							>
								<Info aria-hidden="true" className="size-icon-2xs" />
							</button>
						</TooltipTrigger>
						<TooltipContent className={cn("leading-normal", tooltipClassName)}>{description}</TooltipContent>
					</Tooltip>
				) : null}
			</div>
			<Switch
				aria-label={ariaLabel}
				checked={checked}
				disabled={disabled}
				id={id}
				onCheckedChange={onCheckedChange}
			/>
		</div>
	);
}
