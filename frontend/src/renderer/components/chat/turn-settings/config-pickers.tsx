/** Toggle, execution-mode and provider config option pickers. */
import { Fragment, useMemo, useState, type ReactNode } from "react";
import { ChevronDown, type LucideIcon } from "lucide-react";
import { useTranslation } from "react-i18next";
import { OptionMenu, OptionMenuContent, OptionMenuItem, OptionMenuLabel, OptionMenuSub, OptionMenuSubContent, OptionMenuSubTrigger, OptionMenuTrigger } from "../../ui/option-menu";
import { cn } from "../../../lib/utils";
import { splitClaudeModels } from "../../../lib/agent-model-choices";
import { Switch } from "../../ui/switch";
import { ModelMenuChoices } from "../ModelMenuChoices";
import type { ChatConfigOption, ChatConfigOptionValue } from "../../../types/conversation";
import { isPlanMode, isPlanChoice, optionIsEnabled, choiceIsEnabled, executionModeLabel, isPlanBinary, optionCurrentLabel, isModelOption, focusModelSearch } from "./config-options";
import { TRIGGER_CLASS, CHAT_MENU_CLASS } from "./approval-copy";
import { Picker } from "./composer-menu";
import { PermissionRow } from "./permission";

export function PlanModeToggle({
	option,
	planReturn,
	onChange,
}: {
	option: ChatConfigOption;
	/** Where turning Plan off goes, when the provider shares one slot for both. */
	planReturn?: string;
	onChange: (optionId: string, value: ChatConfigOptionValue) => void;
}) {
	const planning = isPlanMode(option);
	const next = planning
		? planReturn ?? option.choices.find((choice) => !isPlanChoice(choice))?.value
		: option.choices.find((choice) => isPlanChoice(choice))?.value;
	if (!next) return null;
	return (
		<MenuToggle
			label="Plan Mode"
			checked={planning}
			onCheckedChange={() => onChange(option.id, { value: next })}
		/>
	);
}

// ⌘⇧M opens the model menu from the composer, so closing it (Esc or a choice)
// hands focus back to the composer instead of leaving it on the trigger.
export function restoreComposerFocus(event: Event) {
	const trigger = document.querySelector<HTMLElement>("[data-shortcut-target='model-picker'][data-shortcut-opened]");
	if (!trigger) return;
	delete trigger.dataset.shortcutOpened;
	const composer = document.querySelector<HTMLElement>('[aria-label="Message the agent"]');
	if (!composer || composer.getAttribute("aria-disabled") === "true") return;
	event.preventDefault();
	composer.focus();
}

export function ConfigToggle({
	option,
	onChange,
}: {
	option: ChatConfigOption;
	onChange: (optionId: string, value: ChatConfigOptionValue) => void;
}) {
	return (
		<MenuToggle
			label={option.name}
			checked={optionIsEnabled(option)}
			onCheckedChange={(enabled) => {
				if (option.type === "boolean") {
					onChange(option.id, { enabled });
					return;
				}
				const next = option.choices.find((choice) => choiceIsEnabled(choice) === enabled);
				if (next) onChange(option.id, { value: next.value });
			}}
		/>
	);
}

export function MenuToggle({
	label,
	checked,
	onCheckedChange,
}: {
	label: string;
	checked: boolean;
	onCheckedChange: (checked: boolean) => void;
}) {
	return (
		<OptionMenuItem
			// Keep the menu open so several toggles can be flipped, and make Enter on
			// the focused row do what clicking its switch does.
			onSelect={(event) => {
				event.preventDefault();
				onCheckedChange(!checked);
			}}
			className="justify-between gap-4 px-3 py-2 text-xs"
		>
			<span>{label}</span>
			<Switch
				aria-label={label}
				checked={checked}
				// Presentational: the row owns the toggle so a click or Enter fires once.
				tabIndex={-1}
			/>
		</OptionMenuItem>
	);
}

export function ExecutionModePicker({
	option,
	planReturn,
	disabled,
	onChange,
}: {
	option: ChatConfigOption;
	planReturn?: string;
	disabled?: boolean;
	onChange: (optionId: string, value: ChatConfigOptionValue) => void;
}) {
	return (
		<OptionMenu>
			<OptionMenuTrigger
				showCaret={false}
				disabled={disabled}
				aria-label="Model mode for the next turn"
				title="Model mode for the next turn"
				className={TRIGGER_CLASS}
			>
				<span className="min-w-0 max-w-[16ch] truncate">{executionModeLabel(option)}</span>
			</OptionMenuTrigger>
			<OptionMenuContent align="start" className={CHAT_MENU_CLASS}>
				{isPlanBinary(option) ? (
					<PlanModeToggle option={option} planReturn={planReturn} onChange={onChange} />
				) : (
					<ConfigOptionChoices
						option={option}
						onChange={(value) => onChange(option.id, value)}
					/>
				)}
			</OptionMenuContent>
		</OptionMenu>
	);
}

export function MoreOptionsSubmenu({
	options,
	onChange,
}: {
	options: ChatConfigOption[];
	onChange: (optionId: string, value: ChatConfigOptionValue) => void;
}) {
	return (
		<OptionMenuSub>
			<OptionMenuSubTrigger label="More" />
			<OptionMenuSubContent className={CHAT_MENU_CLASS}>
				{options.map((option) => (
					<OptionSubmenu key={option.id} option={option} onChange={onChange} />
				))}
			</OptionMenuSubContent>
		</OptionMenuSub>
	);
}

export function OptionSubmenu({
	option,
	label,
	onChange,
	scrollable,
	groupClaude,
}: {
	option: ChatConfigOption;
	/** A semantic label when one provider option is deliberately split in two. */
	label?: string;
	onChange: (optionId: string, value: ChatConfigOptionValue) => void;
	scrollable?: boolean;
	groupClaude?: boolean;
}) {
	const current = optionCurrentLabel(option);
	return (
		<OptionMenuSub>
			<OptionMenuSubTrigger label={label ?? option.name} value={current} />
			<OptionMenuSubContent
				scrollable={scrollable}
				className={CHAT_MENU_CLASS}
				onFocus={isModelOption(option) ? focusModelSearch : undefined}
			>
				{isModelOption(option) ? (
					<ConfigModelChoices option={option} onChange={(value) => onChange(option.id, value)} groupClaude={groupClaude} />
				) : (
					<ConfigOptionChoices
						option={option}
						onChange={(value) => onChange(option.id, value)}
					/>
				)}
			</OptionMenuSubContent>
		</OptionMenuSub>
	);
}

export function ConfigOptionPicker({
	option,
	label,
	title,
	onChange,
	disabled,
	footer,
	leadIcon,
	groupClaude,
}: {
	option: ChatConfigOption;
	label?: string;
	title?: string;
	onChange: (value: ChatConfigOptionValue) => void;
	disabled?: boolean;
	footer?: ReactNode;
	leadIcon?: LucideIcon;
	groupClaude?: boolean;
}) {
	return (
		<Picker
			label={label ?? optionCurrentLabel(option)}
			title={title || option.description || option.name}
			disabled={disabled}
			leadIcon={leadIcon}
			onFocus={isModelOption(option) ? focusModelSearch : undefined}
		>
			{isModelOption(option) ? (
				<ConfigModelChoices option={option} onChange={onChange} groupClaude={groupClaude} />
			) : leadIcon && option.type === "select" ? (
				option.choices.map((choice) => (
					<PermissionRow
						key={choice.value}
						mode={choice.permissionMode}
						title={choice.name}
						description={choice.description}
						active={choice.value === option.currentValue}
						onSelect={() => onChange({ value: choice.value })}
					/>
				))
			) : (
				<ConfigOptionChoices option={option} onChange={onChange} />
			)}
			{footer}
		</Picker>
	);
}

export function ConfigModelChoices({
	option,
	onChange,
	groupClaude,
}: {
	option: ChatConfigOption;
	onChange: (value: ChatConfigOptionValue) => void;
	/** Newest model per Claude family first, the rest behind a toggle, as in the new-task picker. */
	groupClaude?: boolean;
}) {
	const { t } = useTranslation();
	const models = useMemo(() => option.choices.map((choice) => ({
		...choice,
		id: choice.value,
		label: choice.name,
	})), [option.choices]);
	const claude = useMemo(() => (groupClaude ? splitClaudeModels(models) : undefined), [groupClaude, models]);
	const [otherOpen, setOtherOpen] = useState(false);
	const selectedIsOther = Boolean(claude?.other.some((model) => model.id === option.currentValue));
	const showOther = otherOpen || selectedIsOther;
	const menuModels = claude ? (showOther ? [...claude.current, ...claude.other] : claude.current) : models;
	return (
		<ModelMenuChoices models={menuModels}>
			{(matches, searchActiveID, optionID) => (
				<>
					<ConfigOptionChoices
						option={{ ...option, choices: matches }}
						onChange={onChange}
						searchActiveID={searchActiveID}
						optionID={optionID}
					/>
					{claude?.other.length && !selectedIsOther ? (
						<OptionMenuItem
							// The composer focuses its editor on any click that bubbles out of the portal,
							// which would pull focus out of the menu and close it.
							onClick={(event) => event.stopPropagation()}
							onSelect={(event) => {
								event.preventDefault();
								setOtherOpen((open) => !open);
							}}
							className="text-xs text-muted-foreground"
							aria-expanded={otherOpen}
						>
							{t("settings.models.otherModels")}
							<ChevronDown className={cn("ml-auto size-3 opacity-70 transition-transform", otherOpen && "rotate-180")} aria-hidden="true" />
						</OptionMenuItem>
					) : null}
				</>
			)}
		</ModelMenuChoices>
	);
}

export function ConfigOptionChoices({
	option,
	onChange,
	searchActiveID,
	optionID,
}: {
	option: ChatConfigOption;
	onChange: (value: ChatConfigOptionValue) => void;
	searchActiveID?: string;
	optionID?: (id: string) => string;
}) {
	if (option.type === "boolean") {
		return (
			<>
				{[true, false].map((enabled) => (
					<OptionMenuItem
							key={String(enabled)}
							active={enabled === option.currentBoolean}
							radio
						onSelect={() => onChange({ enabled })}
						className={cn("text-xs")}
					>
						<span
							className={cn(
								enabled === option.currentBoolean
									? "text-foreground"
									: "text-muted-foreground",
							)}
						>
							{enabled ? "On" : "Off"}
						</span>
					</OptionMenuItem>
				))}
			</>
		);
	}

	return (
		<>
			{option.choices.map((choice, index) => {
				const previousGroup = index > 0 ? option.choices[index - 1]?.group : undefined;
				return (
					<Fragment key={choice.value}>
						{choice.group && choice.group !== previousGroup ? (
							<OptionMenuLabel className="px-3 pb-1 pt-2 text-micro text-muted-foreground">
								{choice.groupName || choice.group}
							</OptionMenuLabel>
						) : null}
						<OptionMenuItem
							id={optionID?.(choice.value)}
							active={choice.value === option.currentValue}
							searchActive={choice.value === searchActiveID}
							radio
							onSelect={() => onChange({ value: choice.value })}
							className={cn("text-xs")}
						>
							<span className="flex w-full items-baseline gap-2">
								<span
									className={cn(
										"text-xs",
										choice.value === option.currentValue
											? "text-foreground"
											: "text-muted-foreground",
									)}
								>
									{choice.name}
								</span>
							</span>
						</OptionMenuItem>
					</Fragment>
				);
			})}
		</>
	);
}
