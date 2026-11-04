import { cn } from "../../lib/utils";

export interface QuestionOption {
	value: string;
	label: string;
	description?: string;
}

/**
 * The numbered choice rows of a question: a real radio (or checkbox) laid over
 * each row, the number doubling as keyboard shortcut and selection mark. Shared
 * by the agent question dock and the plan card.
 */
export function QuestionOptions({
	name,
	options,
	multi = false,
	isChecked,
	onSelect,
}: {
	name: string;
	options: QuestionOption[];
	multi?: boolean;
	isChecked: (value: string) => boolean;
	onSelect: (value: string) => void;
}) {
	return (
		<div className="flex flex-col gap-0.5 px-2">
			{options.map((option, index) => {
				const checked = isChecked(option.value);
				return (
					<label
						key={option.value}
						className={cn(
							"group relative flex min-w-0 cursor-pointer items-start gap-3 rounded-lg px-2 py-2 transition-colors has-[:focus-visible]:ring-2 has-[:focus-visible]:ring-ring/40",
							checked ? "bg-logo-accent/[0.1]" : "hover:bg-interactive-hover",
						)}
					>
						<input
							type={multi ? "checkbox" : "radio"}
							name={name}
							value={option.value}
							checked={checked}
							onChange={() => onSelect(option.value)}
							// Invisible but laid over the whole row, so a pointer on the row
							// lands on the real radio rather than on its label.
							className="absolute inset-0 m-0 size-full cursor-pointer opacity-0"
						/>
						{/* The number doubles as the keyboard shortcut and the selection mark. */}
						<span
							aria-hidden="true"
							className={cn(
								"mt-px grid size-[18px] shrink-0 place-items-center rounded-[5px] border text-[10px] font-medium leading-none tabular-nums transition-colors",
								checked
									? "border-logo-accent bg-logo-accent text-logo-accent-foreground"
									: "border-border-strong text-muted-foreground group-hover:text-foreground",
							)}
						>
							{index < 9 ? index + 1 : ""}
						</span>
						<span className="min-w-0 flex-1">
							<span className="block text-xs font-medium leading-5 text-foreground">{option.label}</span>
							{option.description ? (
								<span className="block text-caption leading-relaxed text-muted-foreground">
									{option.description}
								</span>
							) : null}
						</span>
					</label>
				);
			})}
		</div>
	);
}
