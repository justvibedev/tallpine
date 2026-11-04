/** Composer menu rows, mode items and the generic picker. */
import { type FocusEvent } from "react";
import { Check, ListTodo, SlidersHorizontal, TriangleAlert, Zap, type LucideIcon } from "lucide-react";
import { OptionMenu, OptionMenuContent, OptionMenuItem, OptionMenuTrigger } from "../../ui/option-menu";
import { cn } from "../../../lib/utils";
import type { ChatConfigOption, ChatConfigOptionValue, ChatModel, TurnSettings } from "../../../types/conversation";
import { partitionConfigOptions, isPlanBinary, isPlanMode, isPlanChoice, isFastOption, optionIsEnabled, setConfigToggle } from "./config-options";
import { TRIGGER_CLASS, CHAT_MENU_CLASS } from "./approval-copy";

/** A row of the chat box's add menu: an icon, what it is, and what choosing it does. */
export function ComposerMenuRow({
	icon: Icon,
	title,
	description,
	checked,
	disabled,
	onSelect,
}: {
	icon: LucideIcon;
	title: string;
	description?: string;
	checked?: boolean;
	disabled?: boolean;
	onSelect: () => void;
}) {
	return (
		<OptionMenuItem disabled={disabled} onSelect={onSelect} className="gap-2 px-3 py-1.5 text-xs">
			<Icon aria-hidden="true" className="size-3.5 shrink-0 text-muted-foreground" />
			<span className="shrink-0 text-foreground">{title}</span>
			{description ? <span className="min-w-0 truncate text-muted-foreground">{description}</span> : null}
			{checked ? <Check aria-hidden="true" className="ml-auto size-3.5 shrink-0 text-foreground" /> : null}
		</OptionMenuItem>
	);
}

export const ULTRACODE = "ultracode";

/** Claude Code turns Ultracode on for a turn when the user's message names it. */
export function withUltracode(text: string): string {
	return /\bultracode\b/i.test(text) ? text : `Ultracode:\n${text}`;
}

/** The agent's on/off settings as add-menu rows: plan mode, fast mode and any other switch it advertises. */
export function ComposerModeItems({
	configOptions,
	settings,
	models,
	nativePlan = false,
	onChange,
	onChangeConfigOption,
	disabled,
}: {
	configOptions: ChatConfigOption[];
	settings: TurnSettings;
	/** The provider's own model list, for settings Tallpine sends per turn. */
	models?: ChatModel[];
	/** The provider takes plan mode as a per-turn setting from Tallpine. */
	nativePlan?: boolean;
	onChange?: (next: TurnSettings) => void;
	onChangeConfigOption?: (optionId: string, value: ChatConfigOptionValue) => Promise<unknown> | void;
	disabled?: boolean;
}) {
	const grouped = partitionConfigOptions(configOptions);
	const apply = (optionId: string, value: ChatConfigOptionValue) => {
		if (onChangeConfigOption) void Promise.resolve(onChangeConfigOption(optionId, value)).catch(() => {});
	};
	const executionMode = onChangeConfigOption && grouped.executionMode && isPlanBinary(grouped.executionMode)
		? grouped.executionMode
		: undefined;
	const planning = isPlanMode(executionMode);
	const planReturn = grouped.mode?.choices.find((choice) => choice.permissionMode === (settings.approvalMode ?? "default"))?.value;
	const planTarget = planning
		? planReturn ?? executionMode?.choices.find((choice) => !isPlanChoice(choice))?.value
		: executionMode?.choices.find(isPlanChoice)?.value;
	const planOn = executionMode ? planning : Boolean(settings.planMode);
	const togglePlan = executionMode
		? planTarget ? () => apply(executionMode.id, { value: planTarget }) : undefined
		: nativePlan && onChange ? () => onChange({ ...settings, planMode: settings.planMode ? undefined : true }) : undefined;
	const model = models?.find((entry) => entry.id === settings.model) ?? models?.find((entry) => entry.default);
	const rows = [
		...(togglePlan ? [{ icon: ListTodo, title: "Plan mode", on: planOn, toggle: togglePlan }] : []),
		...(model?.supportsFast && onChange ? [{
			icon: Zap,
			title: "Fast mode",
			on: Boolean(settings.fastMode),
			toggle: () => onChange({ ...settings, fastMode: settings.fastMode ? undefined : true }),
		}] : []),
		...(onChangeConfigOption ? grouped.toggles : []).map((option) => ({
			icon: isFastOption(option) ? Zap : SlidersHorizontal,
			title: option.name,
			on: optionIsEnabled(option),
			toggle: () => setConfigToggle(option, !optionIsEnabled(option), apply),
		})),
	];
	return rows.map((row, index) => (
		<ComposerMenuRow
			key={index}
			icon={row.icon}
			title={row.title}
			description={`Turn ${row.title.toLowerCase()} ${row.on ? "off" : "on"}`}
			checked={row.on}
			disabled={disabled}
			onSelect={row.toggle}
		/>
	));
}

/**
 * One dropdown, wearing the chrome Settings uses. These controls are the same
 * kind of thing as a settings row's — pick one of a list — so they are drawn the
 * same way, and the panel sizes itself from the shared surface rather than each
 * picker naming a width of its own.
 */
export function Picker({
	label,
	title,
	disabled,
	badge,
	children,
	onFocus,
	leadIcon: LeadIcon,
	alert,
}: {
	label: string;
	title: string;
	disabled?: boolean;
	/** The current choice will not hold for the next message: marked red. */
	alert?: boolean;
	/** Set on the permission control when it leads the row: marked with this, with a caret, opening rightwards. */
	leadIcon?: LucideIcon;
	/** A note that belongs on the trigger, e.g. the model that was overridden. */
	badge?: React.ReactNode;
	children: React.ReactNode;
	onFocus?: (event: FocusEvent<HTMLDivElement>) => void;
}) {
	return (
		<OptionMenu>
				<OptionMenuTrigger
					showCaret={Boolean(LeadIcon)}
					aria-label={title}
					title={title}
					disabled={disabled}
					className={cn(TRIGGER_CLASS, LeadIcon ? "px-2" : undefined, alert ? "text-destructive" : undefined)}
				>
					{alert ? <TriangleAlert aria-hidden="true" className="size-3.5 shrink-0 text-destructive" />
						: LeadIcon ? <LeadIcon aria-hidden="true" className="size-3.5 shrink-0" /> : null}
					<span className="min-w-0 max-w-[16ch] truncate">{label}</span>
					{badge}
				</OptionMenuTrigger>
			<OptionMenuContent
				align={LeadIcon ? "start" : "end"}
				// The permission menu fits every mode on one line each, plus the remember switch, without scrolling.
				className={cn(CHAT_MENU_CLASS, LeadIcon ? "max-h-(--radix-dropdown-menu-content-available-height)! max-w-[calc(100vw-2rem)]!" : undefined)}
				onFocus={onFocus}
			>
				{children}
			</OptionMenuContent>
		</OptionMenu>
	);
}
