/** One field of an elicitation form. */
import { cn } from "../../../lib/utils";
import { QuestionOptions } from "../QuestionOptions";
import { InputValue, propertyLabel, enumOptions, toggleValue } from "./elicitation-model";

export function FormField({
	name,
	property,
	value,
	required,
	invalid,
	disabled,
	labelledBy,
	rows,
	onChange,
}: {
	name: string;
	property: Record<string, unknown>;
	value: InputValue | undefined;
	required: boolean;
	invalid: boolean;
	disabled: boolean;
	/** Use the dock header as this field's name instead of drawing a legend. */
	labelledBy?: string;
	/** Draw choices and free text as dock rows rather than a labelled form field. */
	rows?: boolean;
	onChange: (value: InputValue) => void;
}) {
	const label = propertyLabel([name, property]);
	const description = typeof property.description === "string" ? property.description : undefined;
	const id = `elicitation-${name}`;
	const errorId = `${id}-error`;
	const options = enumOptions(property);
	const multi = property.type === "array";

	if (options.length > 0) {
		return (
			<fieldset
				className="min-w-0"
				disabled={disabled}
				aria-required={required || undefined}
				aria-invalid={invalid || undefined}
				aria-labelledby={labelledBy}
				aria-describedby={invalid ? errorId : undefined}
			>
				{labelledBy ? null : (
					<legend className="px-3 pb-1 text-xs font-medium text-muted-foreground">
						{label}
						{required ? " *" : ""}
					</legend>
				)}
				{description && !labelledBy ? (
					<p className="px-3 pb-1 text-caption leading-relaxed text-muted-foreground">{description}</p>
				) : null}
				<QuestionOptions
					name={name}
					options={options}
					multi={multi}
					isChecked={(optionValue) =>
						multi ? Array.isArray(value) && value.includes(optionValue) : value === optionValue
					}
					onSelect={(optionValue) =>
						onChange(multi ? toggleValue(Array.isArray(value) ? value : [], optionValue) : optionValue)
					}
				/>
				{invalid ? (
					<p id={errorId} className="px-3 pt-1 text-xs text-destructive">
						Choose an answer.
					</p>
				) : null}
			</fieldset>
		);
	}

	if (property.type === "boolean") {
		return (
			<>
				<label
					className={cn(
						"flex min-h-10 min-w-0 cursor-pointer items-center gap-2.5 px-3 py-2 transition-colors hover:bg-interactive-hover",
						rows ? "" : "rounded-lg",
					)}
				>
					<input
						type="checkbox"
						checked={value === true}
						disabled={disabled}
						aria-required={required || undefined}
						aria-invalid={invalid || undefined}
						aria-describedby={invalid ? errorId : undefined}
						onChange={(event) => onChange(event.target.checked)}
						className="size-3 shrink-0 accent-[var(--logo-accent)]"
					/>
					<span className="min-w-0 flex-1">
						<span className="block text-xs leading-relaxed text-foreground">{label}</span>
						{description ? (
							<span className="mt-0.5 block text-caption leading-relaxed text-muted-foreground">
								{description}
							</span>
						) : null}
					</span>
				</label>
				{invalid ? (
					<p id={errorId} className={cn("px-3 text-xs text-destructive", rows ? "pb-1" : "pt-1")}>
						This field is required.
					</p>
				) : null}
			</>
		);
	}

	const numeric = property.type === "number" || property.type === "integer";
	const control = (
		<input
			id={id}
			type={numeric ? "number" : "text"}
			placeholder={rows ? `${label}…` : undefined}			aria-required={required || undefined}
			min={typeof property.minimum === "number" ? property.minimum : undefined}
			max={typeof property.maximum === "number" ? property.maximum : undefined}
			step={property.type === "integer" ? 1 : undefined}
			minLength={typeof property.minLength === "number" ? property.minLength : undefined}
			maxLength={typeof property.maxLength === "number" ? property.maxLength : undefined}
			value={typeof value === "string" || typeof value === "number" ? value : ""}
			disabled={disabled}
			aria-invalid={invalid || undefined}
			aria-describedby={invalid ? errorId : undefined}
			onChange={(event) => onChange(numeric && event.target.value !== "" ? Number(event.target.value) : event.target.value)}
			className={cn(
				"w-full min-w-0 rounded-lg border bg-background/40 px-2.5 text-xs leading-relaxed text-foreground outline-none transition-colors focus-visible:border-border-strong",
				rows ? "h-7 border-0 bg-transparent px-0 focus-visible:border-transparent" : "mt-1.5 h-8",
				invalid ? "border-destructive" : rows ? "border-transparent" : "border-border",
			)}
		/>
	);

	// A Claude "Other" answer is one more way to answer the question above it, so
	// it is one more row, indented into the column the choices keep for their
	// radio. Its title is a quiet visible label above the field rather than a
	// placeholder: DESIGN.md §9 allows a placeholder as an example, never as the
	// only name a field has.
	if (rows) {
		return (
			<>
				{/* The field itself is borderless here and global CSS strips rings from
				    the focused element, so the row carries focus like a choice row does. */}
				<div className="mx-2 mt-0.5 flex min-w-0 items-center gap-3 rounded-lg px-2 py-1 has-[:focus-visible]:ring-2 has-[:focus-visible]:ring-ring/40">
					<span aria-hidden="true" className="grid size-[18px] shrink-0 place-items-center rounded-[5px] border text-[10px] font-medium leading-none tabular-nums transition-colors border-border-strong text-muted-foreground">
						0
					</span>
					<label htmlFor={id} className="sr-only">
						{label}
						{required ? " *" : ""}
					</label>
					<span className="min-w-0 flex-1">{control}</span>
				</div>
				{invalid ? (
					<p id={errorId} className="px-3 pb-1 text-xs text-destructive">
						This field is required.
					</p>
				) : null}
			</>
		);
	}

	return (
		<label htmlFor={id} className="block">
			<span className="text-xs font-medium text-foreground">
				{label}
				{required ? " *" : ""}
			</span>
			{description ? (
				<span className="mt-0.5 block text-caption leading-relaxed text-muted-foreground">{description}</span>
			) : null}
			{control}
			{invalid ? (
				<span id={errorId} className="mt-1 block text-xs text-destructive">
					This field is required.
				</span>
			) : null}
		</label>
	);
}

