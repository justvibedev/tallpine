/** Model, effort and clubbed config pickers. */
import { useMemo } from "react";
import { Shuffle } from "lucide-react";
import { useTranslation } from "react-i18next";
import { OptionMenu, OptionMenuContent, OptionMenuItem, OptionMenuSub, OptionMenuSubContent, OptionMenuSubTrigger, OptionMenuTrigger } from "../../ui/option-menu";
import { fallbackEffort } from "../../../lib/effort";
import { cn } from "../../../lib/utils";
import { effortDisplayLabel, EffortMenuItems, EffortPicker } from "../../settings/EffortPicker";
import { claudeChoiceModel } from "../../../lib/agent-model-choices";
import { ModelMenuChoices } from "../ModelMenuChoices";
import type { ChatConfigOption, ChatConfigOptionValue, ChatModel, ModelReroute, TurnSettings } from "../../../types/conversation";
import { TRIGGER_CLASS, CHAT_MENU_CLASS } from "./approval-copy";
import { restoreComposerFocus, PlanModeToggle, ConfigToggle, MoreOptionsSubmenu, ExecutionModePicker, ConfigOptionPicker, OptionSubmenu } from "./config-pickers";
import { focusModelSearch, optionCurrentLabel } from "./config-options";

export function dedupeChoicesByName<T extends { value: string; name: string; group?: string }>(choices: T[], current: string | undefined): (T & { aliases?: string[] })[] {
	return choices.flatMap((choice) => {
		// One model from two providers (Pi's openai and openai-codex) stays two rows.
		const twins = choices.filter((other) => other.name === choice.name && other.group === choice.group);
		const kept = twins.find((other) => other.value === current) ?? twins[0];
		if (choice !== kept) return [];
		// The values folded into this one, so a favourite starred on any of them still shows.
		const aliases = twins.filter((other) => other !== choice).map((other) => other.value);
		return [aliases.length ? { ...choice, aliases } : choice];
	});
}

/**
 * The provider's model option offers family aliases ("opus") and bare ids. The new-task
 * picker names them by version, so the chat does too.
 */
export function claudeChoiceLabels(catalog: { id: string; displayName: string }[]) {
	const choiceModel = claudeChoiceModel(catalog.map((model) => ({ id: model.id, label: model.displayName })));
	return choiceModel && ((value: string, name?: string) => choiceModel(value, name)?.label);
}

export function ModelEffortPicker({
	models,
	settings,
	onChange,
	disabled,
	modelLabel,
	groupLabel,
	efforts,
	defaultEffort,
	availability,
	reroute,
	rerouted,
	chosenLabel,
	executionMode,
	planReturn,
	toggles = [],
	extraOptions = [],
	onChangeConfigOption,
}: {
	models: ChatModel[];
	settings: TurnSettings;
	onChange: (next: TurnSettings) => void;
	disabled?: boolean;
	modelLabel: string;
	groupLabel: string;
	efforts: string[];
	defaultEffort?: string;
	availability: "supported" | "unsupported" | "unknown";
	reroute?: ModelReroute;
	rerouted?: string;
	chosenLabel: string;
	executionMode?: ChatConfigOption;
	planReturn?: string;
	toggles?: ChatConfigOption[];
	extraOptions?: ChatConfigOption[];
	onChangeConfigOption?: (optionId: string, value: ChatConfigOptionValue) => void;
}) {
	const { t } = useTranslation();
	const catalog = useMemo(() => models.map((model) => ({ ...model, label: model.displayName })), [models]);
	const effortChoices = useMemo(() => efforts.map((value) => ({ value })), [efforts]);
	const savedEffort = settings.reasoningEffort === "default" ? "" : settings.reasoningEffort ?? "";

	return (
		<OptionMenu>
				<OptionMenuTrigger
					showCaret={false}
					disabled={disabled}
					aria-label="Model and reasoning effort for the next turn"
					data-shortcut-target="model-picker"
					title={
						reroute
							? `The provider answered with ${rerouted} instead of ${reroute.fromModel ?? chosenLabel}${
									reroute.reason ? `: ${reroute.reason}` : ""
								}`
							: "Model and reasoning effort for the next turn"
					}
					className={TRIGGER_CLASS}
				>
					<span className="min-w-0 max-w-[38ch] truncate">{groupLabel}</span>
					{reroute ? (
						// A mark, not a second name. Two truncated model names side by side is
						// less legible than one readable name plus a flag that says it is not
						// the one that was asked for; the tooltip and the menu spell out which.
						<Shuffle
							className="size-3 shrink-0 text-warning"
							aria-label={`Substituted for ${reroute.fromModel ?? chosenLabel}`}
						/>
					) : null}
				</OptionMenuTrigger>
			<OptionMenuContent align="start" className={CHAT_MENU_CLASS} onCloseAutoFocus={restoreComposerFocus}>
				<OptionMenuSub closeOnSelect>
					<OptionMenuSubTrigger label="Model" value={modelLabel} />
					{/* Scroll on an inner strip: the surface utility caps height but wheel
					    events do not reliably reach an outer overflow on nested submenus. */}
					<OptionMenuSubContent scrollable className={CHAT_MENU_CLASS} onFocus={focusModelSearch}>
						<ModelMenuChoices models={catalog}>
							{(matches, searchActiveID, optionID) => matches.map((model) => (
								<OptionMenuItem
									key={model.id}
									id={optionID?.(model.id)}
									active={model.id === settings.model}
									searchActive={model.id === searchActiveID}
									radio
									onSelect={() => onChange({ ...settings, model: model.id, reasoningEffort: undefined })}
									className={cn("text-xs", model.id === settings.model ? "text-foreground" : "text-muted-foreground")}
								>
									{model.displayName}
								</OptionMenuItem>
							))}
						</ModelMenuChoices>
					</OptionMenuSubContent>
				</OptionMenuSub>

				{efforts.length > 0 || savedEffort ? (
					<OptionMenuSub closeOnSelect>
						<OptionMenuSubTrigger
							label={t("settings.models.effort")}
							value={effortDisplayLabel({ value: savedEffort, choices: effortChoices, followLabel: t("settings.models.effort"), t, defaultEffort })}
						/>
						<OptionMenuSubContent className={CHAT_MENU_CLASS}>
							<EffortMenuItems
								value={savedEffort}
								choices={effortChoices}
								availability={availability}
								defaultEffort={defaultEffort}
								compact
								onChange={(value) => onChange({ ...settings, reasoningEffort: value || undefined })}
							/>
						</OptionMenuSubContent>
					</OptionMenuSub>
				) : null}
				{executionMode && onChangeConfigOption ? (
					<PlanModeToggle option={executionMode} planReturn={planReturn} onChange={onChangeConfigOption} />
				) : null}
				{toggles.map((option) => (
					<ConfigToggle key={option.id} option={option} onChange={onChangeConfigOption!} />
				))}
				{extraOptions.length > 0 && onChangeConfigOption ? (
					<MoreOptionsSubmenu options={extraOptions} onChange={onChangeConfigOption} />
				) : null}
			</OptionMenuContent>
		</OptionMenu>
	);
}

export function ClubbedConfigPicker({
	modelOptions,
	effortOptions,
	executionMode,
	planReturn,
	toggles,
	extraOptions,
	disabled,
	onChange,
	groupClaude,
}: {
	groupClaude?: boolean;
	modelOptions: ChatConfigOption[];
	effortOptions: ChatConfigOption[];
	executionMode?: ChatConfigOption;
	planReturn?: string;
	toggles: ChatConfigOption[];
	extraOptions: ChatConfigOption[];
	disabled?: boolean;
	onChange: (optionId: string, value: ChatConfigOptionValue) => void;
}) {
	const { t } = useTranslation();
	const primaryModel = modelOptions.at(0);
	const primaryEffort = effortOptions.at(0);
	const modelLabel = primaryModel ? optionCurrentLabel(primaryModel) : undefined;
	const effortLabel = primaryEffort ? effortDisplayLabel({ ...acpEffortProps(primaryEffort), followLabel: "", t }) : undefined;
	const groupLabel = [modelLabel, effortLabel].filter(Boolean).join(" ") || "More";
	const leftCount =
		modelOptions.length + effortOptions.length + Number(Boolean(executionMode)) + toggles.length + extraOptions.length;
	if (leftCount === 0) return null;
	if (leftCount === 1) {
		if (primaryEffort) {
			return (
				<EffortPicker
					{...acpEffortMenuProps(primaryEffort)}
					disabled={disabled}
					triggerClassName={TRIGGER_CLASS}
					onChange={(value) => onChange(primaryEffort.id, { value })}
				/>
			);
		}
		if (executionMode)
			return (
				<ExecutionModePicker
					option={executionMode}
					planReturn={planReturn}
					disabled={disabled}
					onChange={onChange}
				/>
			);
		const option = primaryModel ?? toggles[0] ?? extraOptions[0];
		if (!option) return null;
		return (
			<ConfigOptionPicker
				option={option}
				disabled={disabled}
				onChange={(value) => onChange(option.id, value)}
				groupClaude={groupClaude}
			/>
		);
	}

	return (
		<OptionMenu>
				<OptionMenuTrigger
					showCaret={false}
					disabled={disabled}
					aria-label="Model and reasoning effort for the next turn"
					data-shortcut-target="model-picker"
					title="Model and reasoning effort for the next turn"
					className={TRIGGER_CLASS}
				>
					<span className="min-w-0 max-w-[38ch] truncate">{groupLabel}</span>
				</OptionMenuTrigger>
			<OptionMenuContent align="start" className={CHAT_MENU_CLASS} onCloseAutoFocus={restoreComposerFocus}>
				{modelOptions.map((option) => (
					<OptionSubmenu key={option.id} option={option} onChange={onChange} scrollable groupClaude={groupClaude} />
				))}
				{effortOptions.map((option) => (
					<EffortOptionSubmenu key={option.id} option={option} onChange={onChange} />
				))}
				{executionMode ? <PlanModeToggle option={executionMode} planReturn={planReturn} onChange={onChange} /> : null}
				{toggles.map((option) => (
					<ConfigToggle key={option.id} option={option} onChange={onChange} />
				))}
				{extraOptions.length > 0 ? (
					<MoreOptionsSubmenu options={extraOptions} onChange={onChange} />
				) : null}
			</OptionMenuContent>
		</OptionMenu>
	);
}

// ACP exposes the provider-owned reset as a "default" choice whose description
// names the concrete level it resolves to.
export function acpEffortMenuProps(option: ChatConfigOption) {
	const defaultChoice = option.choices.find((choice) => choice.value === "default");
	return {
		value: option.currentValue ?? "",
		choices: option.choices.map((choice) => ({ value: choice.value, label: choice.name })),
		defaultValue: defaultChoice?.value ?? null,
		defaultEffort: defaultChoice?.description,
	};
}

export function acpEffortProps(option: ChatConfigOption) {
	const { value, choices, defaultEffort } = acpEffortMenuProps(option);
	return { value: value === "default" ? "" : value, choices, defaultEffort };
}

// ACP exposes the provider-owned reset as a "default" choice. When it names no
// concrete level, nothing is selected, so Tallpine supplies one.
export function acpFallbackEffort(option: ChatConfigOption): string | undefined {
	if (option.currentValue && option.currentValue !== "default") return undefined;
	const concrete = option.choices.filter((choice) => choice.value && choice.value !== "default");
	const described = option.choices.find((choice) => choice.value === "default")?.description?.trim().toLowerCase();
	const reported = concrete.find(
		(choice) => described && (choice.value.toLowerCase() === described || choice.name.toLowerCase() === described),
	)?.value;
	return fallbackEffort(
		concrete.map((choice) => choice.value),
		reported,
	);
}

export function EffortOptionSubmenu({
	option,
	onChange,
}: {
	option: ChatConfigOption;
	onChange: (id: string, value: ChatConfigOptionValue) => void;
}) {
	const { t } = useTranslation();
	const followLabel = t("settings.models.effort");
	return (
		<OptionMenuSub closeOnSelect>
			<OptionMenuSubTrigger label={followLabel} value={effortDisplayLabel({ ...acpEffortProps(option), followLabel, t })} />
			<OptionMenuSubContent className={CHAT_MENU_CLASS}>
				<EffortMenuItems {...acpEffortMenuProps(option)} compact onChange={(value) => onChange(option.id, { value })} />
			</OptionMenuSubContent>
		</OptionMenuSub>
	);
}
