/** Pure helpers that classify and partition provider config options. */
import { type FocusEvent } from "react";
import { isDefaultPlaceholderLabel } from "../../../lib/agent-model-choices";
import type { ChatConfigOption, ChatConfigOptionValue } from "../../../types/conversation";

export function isFastOption(option: ChatConfigOption): boolean {
	return /(?:^|[\s_-])fast(?:[\s_-]|$)/i.test(`${option.id} ${option.name}`);
}

export function setConfigToggle(
	option: ChatConfigOption,
	enabled: boolean,
	apply: (optionId: string, value: ChatConfigOptionValue) => void,
) {
	if (option.type === "boolean") {
		apply(option.id, { enabled });
		return;
	}
	const next = option.choices.find((choice) => choiceIsEnabled(choice) === enabled);
	if (next) apply(option.id, { value: next.value });
}

/** setConfigToggle for a write the caller waits on; a toggle already set is not written. */
export async function toggleConfigOption(
	option: ChatConfigOption,
	enabled: boolean,
	write: (optionId: string, value: ChatConfigOptionValue) => Promise<void>,
) {
	if (optionIsEnabled(option) === enabled) return;
	let pending: Promise<void> | undefined;
	setConfigToggle(option, enabled, (optionId, value) => { pending = write(optionId, value); });
	await pending;
}

export function focusModelSearch(event: FocusEvent<HTMLDivElement>) {
	if (event.target !== event.currentTarget) return;
	const search = event.currentTarget.querySelector<HTMLInputElement>('input[type="search"]');
	if (search) {
		// Focus search before the menu's roving focus chooses a model.
		event.preventDefault();
		search.focus();
	}
}

export function isModelOption(option: ChatConfigOption): boolean {
	return option.category === "model" || option.id === "model";
}

/**
 * ACP may advertise an `agent` option for its own multi-agent internals. It is
 * not Tallpine's harness switcher and is not a model choice, so exposing it in the
 * composer promises a meaning Tallpine cannot guarantee. Harness changes remain in
 * the dedicated session switcher.
 */
export function isAgentOption(option: ChatConfigOption): boolean {
	return option.id === "agent" || option.category === "agent";
}

export function isEffortOption(option: ChatConfigOption): boolean {
	return option.category === "thought_level" || option.id === "effort";
}

export function isModeOption(option: ChatConfigOption): boolean {
	return option.category === "mode" || option.id === "mode";
}

export function isInlineToggleOption(option: ChatConfigOption): boolean {
	return option.type === "boolean" || isThinkingToggle(option) || isFastOption(option);
}

/** Cursor reports thinking as an on/off choice among its effort settings. */
export function isThinkingToggle(option: ChatConfigOption): boolean {
	return option.id === "thinking" && option.choices.length === 2 && option.choices.some(choiceIsEnabled);
}

export function optionIsEnabled(option: ChatConfigOption): boolean {
	if (option.type === "boolean") return Boolean(option.currentBoolean);
	return choiceIsEnabled(option.choices.find((choice) => choice.value === option.currentValue));
}

export function choiceIsEnabled(choice: ChatConfigOption["choices"][number] | undefined): boolean {
	return Boolean(choice && /(?:^|[\s_-])(on|enabled|true)(?:[\s_-]|$)/i.test(`${choice.name} ${choice.value}`));
}

/**
 * Whether a provider catalog replaces Tallpine's own approval control. A `mode` option
 * that offers only execution modes (OpenCode's build/plan) is not one: taking it
 * for an approval catalog leaves the session with no permission control at all.
 */
export function hasProviderPermissionMode(options: ChatConfigOption[]): boolean {
	return Boolean(partitionConfigOptions(options).mode);
}

export function partitionConfigOptions(options: ChatConfigOption[], includeExecutionModes = true): {
	model: ChatConfigOption[];
	effort: ChatConfigOption[];
	/** Provider plan/agent modes, placed with model selection rather than permissions. */
	executionMode: ChatConfigOption | undefined;
	/** Compact boolean controls, such as Fast Mode, rendered beside Plan Mode. */
	toggles: ChatConfigOption[];
	mode: ChatConfigOption | undefined;
	extra: ChatConfigOption[];
} {
	const primaryModel: ChatConfigOption[] = [];
	const otherModel: ChatConfigOption[] = [];
	const effort: ChatConfigOption[] = [];
	const toggles: ChatConfigOption[] = [];
	const extra: ChatConfigOption[] = [];
	let executionMode: ChatConfigOption | undefined;
	let mode: ChatConfigOption | undefined;
	for (const rawOption of options) {
		const option = rawOption.type === "select"
			? isEffortOption(rawOption)
				? rawOption
				: resolveImplicitChoice(rawOption)
			: rawOption;
		if (option.type === "select" && option.choices.length === 0 && !isEffortOption(option)) continue;
		if (isAgentOption(option)) continue;
		if (isModelOption(option)) {
			if (option.category === "model" || option.id === "model") primaryModel.push(option);
			else otherModel.push(option);
			continue;
		}
		if (isEffortOption(option) && !isThinkingToggle(option)) {
			if (!option.choices.some((choice) => choice.value && choice.value !== "default")) continue;
			effort.push(option);
			continue;
		}
		if (isInlineToggleOption(option)) {
			toggles.push(option);
			continue;
		}
		if (isModeOption(option) && !mode) {
			// Classify before synthesizing Claude's Agent choice so it cannot promote approval choices.
			const executionValues = executionChoiceValues(option.choices);
			const permissionChoices = option.choices.filter((choice) => !executionValues.has(choice.value));
			const executionChoices = includeExecutionModes ? addAgentModeChoice(
				option.choices.filter((choice) => executionValues.has(choice.value)),
				permissionChoices,
			) : [];
			if (executionChoices.length > 0) {
				executionMode = withChoices(option, executionChoices);
				if (permissionChoices.length > 0) mode = withChoices(option, permissionChoices);
			} else {
				mode = option;
			}
			continue;
		}
		extra.push(option);
	}
	return { model: [...primaryModel, ...otherModel], effort, executionMode, toggles, mode, extra };
}

export function resolveImplicitChoice(option: ChatConfigOption): ChatConfigOption {
	const mapped = isModeOption(option) ? {
		...option,
		choices: option.choices.map((choice) =>
			choice.permissionMode === "default" && isDefaultPlaceholderLabel(choice.name)
				? { ...choice, name: "Use agent permissions" }
				: choice,
		),
	} : option;
	// A mode named Default is the agent's own beside its other own modes (Kimi's Default, Plan,
	// Auto, YOLO); it means "the agent decides" only alone or beside modes matched to Tallpine's.
	const placeholderMode = (choice: ChatConfigOption["choices"][number]) =>
		choice.permissionMode !== "default" &&
		(isDefaultPlaceholderLabel(choice.name) || /^use agent permissions$/i.test(choice.name.trim())) &&
		(mapped.choices.length === 1 || mapped.choices.some((other) => other !== choice && other.permissionMode));
	const implicit = mapped.choices.find((choice) =>
		choice.value === "default" && (!isModeOption(mapped) || placeholderMode(choice)),
	);
	if (!implicit) return mapped;
	if (isModeOption(mapped)) return { ...mapped, choices: mapped.choices.filter((choice) => choice !== implicit) };
	const concrete = mapped.choices.find((choice) =>
		choice.value !== implicit.value && choice.name.toLowerCase() === implicit.description?.trim().toLowerCase(),
	);
	const followLabel = isModelOption(mapped) ? "Use agent model" : "Use agent setting";
	if (concrete && mapped.currentValue !== concrete.value) {
		const label = isDefaultPlaceholderLabel(concrete.name) ? concrete.value : concrete.name;
		// The row stands for the hidden model too, so a favourite saved on it still shows as offered.
		const aliases = [...((implicit as typeof implicit & { aliases?: string[] }).aliases ?? []), concrete.value];
		return {
			...mapped,
			choices: mapped.choices.filter((choice) => choice !== concrete).map((choice) =>
				choice === implicit ? { ...choice, name: label, aliases } : choice,
			),
		};
	}
	// A model picker lists models only; the agent's own choice is not a row of its own.
	if (isModelOption(mapped) && mapped.currentValue !== implicit.value) {
		return { ...mapped, choices: mapped.choices.filter((choice) => choice !== implicit) };
	}
	return {
		...mapped,
		choices: mapped.choices.map((choice) => choice === implicit && isDefaultPlaceholderLabel(choice.name)
			? { ...choice, name: concrete ? `${followLabel} (${concrete.name})` : followLabel }
			: choice),
	};
}

/** Ask is an execution mode only when the same option advertises Agent; otherwise it is an approval policy. */
export function executionChoiceValues(choices: ChatConfigOption["choices"]): Set<string> {
	const values = new Set<string>();
	const hasAgent = choices.some((choice) => executionChoiceMatches(choice, "agent"));
	for (const choice of choices) {
		if (executionChoiceMatches(choice, "plan|agent|build")) {
			values.add(choice.value);
			continue;
		}
		if (hasAgent && executionChoiceMatches(choice, "ask")) values.add(choice.value);
	}
	return values;
}

// Match complete mode labels so custom agent names cannot masquerade as native modes.
export function executionChoiceMatches(choice: ChatConfigOption["choices"][number], word: string): boolean {
	return [choice.value, choice.name].some((value) =>
		new RegExp(`^(?:${word})(?:[\\s_-]mode)?$`, "i").test(value.trim()),
	);
}

export function choiceMatches(
	choice: Pick<ChatConfigOption["choices"][number], "name" | "value">,
	word: string,
): boolean {
	return new RegExp(`(?:^|[\\s_-])(?:${word})(?:[\\s_-]|$)`, "i").test(`${choice.name} ${choice.value}`);
}

/**
 * Claude currently sends Plan as an execution mode but describes its ordinary
 * interactive posture as Manual. Present the latter as Agent Mode next to Plan,
 * without inventing a value: choosing it still sends the provider's own Manual
 * value. Other harnesses that advertise Agent Mode explicitly stay untouched.
 */
export function addAgentModeChoice(
	executionChoices: ChatConfigOption["choices"],
	permissionChoices: ChatConfigOption["choices"],
): ChatConfigOption["choices"] {
	// OpenCode names its ordinary agent mode "build", so its catalog already has
	// one and must not gain a synthetic duplicate.
	if (executionChoices.some((choice) => executionChoiceMatches(choice, "agent|build"))) {
		return executionChoices;
	}
	const standard = permissionChoices.find((choice) => choiceMatches(choice, "manual"))
		?? permissionChoices.find((choice) => choiceMatches(choice, "default|standard"));
	return standard
		? [{ ...standard, name: "Agent Mode", description: "Standard agent execution" }, ...executionChoices]
		: executionChoices;
}

export function isPlanBinary(option: ChatConfigOption): boolean {
	return option.choices.length === 2 && option.choices.some(isPlanChoice);
}

export function executionModeLabel(option: ChatConfigOption): string {
	if (isPlanBinary(option)) return isPlanMode(option) ? "Plan Mode" : "Agent Mode";
	return option.choices.find((choice) => choice.value === option.currentValue)?.name ?? option.name;
}

export function isPlanMode(option: ChatConfigOption | undefined): boolean {
	return Boolean(option?.currentValue && isPlanChoice({ value: option.currentValue, name: option.currentValue }));
}

export function isPlanChoice(choice: Pick<ChatConfigOption["choices"][number], "name" | "value">): boolean {
	// Custom agent IDs can contain "plan" without enabling native planning.
	return [choice.value, choice.name].some((value) => /^plan(?:[\s_-]mode)?$/i.test(value.trim()));
}

export function withChoices(
	option: ChatConfigOption,
	choices: ChatConfigOption["choices"],
	name = option.name,
): ChatConfigOption {
	const currentValue = choices.some((choice) => choice.value === option.currentValue)
		? option.currentValue
		: undefined;
	return { ...option, name, currentValue, choices };
}

export function optionCurrentLabel(option: ChatConfigOption): string {
	if (option.type === "boolean") return option.currentBoolean ? "On" : "Off";
	if (option.currentValue === "default" && !option.choices.some((choice) => choice.value === "default")) {
		if (isEffortOption(option)) return "Effort not reported";
		if (isModelOption(option)) return "Model not reported";
		return "Selection not reported";
	}
	return option.choices.find((choice) => choice.value === option.currentValue)?.name
		?? option.currentValue
		?? option.name;
}
