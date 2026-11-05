import type { ChatConfigOption, ChatConfigOptionValue } from "../../../types/conversation";
import type { ModelFavorite } from "../../../lib/model-favorites";
import { isFastOption, isThinkingToggle, partitionConfigOptions, toggleConfigOption } from "./config-options";

type ConfigWriter = (optionId: string, value: ChatConfigOptionValue) => unknown;

/**
 * Applies a saved model setup. The effort, Thinking and Fast are set after the model,
 * because the agent offers them per model: each is read from the options the previous
 * write returned, not the outgoing model's. Fast is set either way, so a saved setup
 * without it turns a paid Fast off.
 */
export function applyModelSetup(
	options: ChatConfigOption[],
	modelOptionId: string,
	setup: ModelFavorite,
	onChangeConfigOption: ConfigWriter,
): void {
	let catalog = options;
	const write = async (optionId: string, value: ChatConfigOptionValue) => {
		const next = await onChangeConfigOption(optionId, value);
		if (Array.isArray(next)) catalog = next as ChatConfigOption[];
	};
	const current = () => partitionConfigOptions(catalog, true);
	void (async () => {
		await write(modelOptionId, { value: setup.model });
		const effortOption = current().effort.at(0);
		if (setup.effort && effortOption?.choices.some((choice) => choice.value === setup.effort)) {
			await write(effortOption.id, { value: setup.effort });
		}
		const thinking = current().toggles.find(isThinkingToggle);
		if (thinking && setup.thinking !== undefined) await toggleConfigOption(thinking, setup.thinking, write);
		const fast = current().toggles.find(isFastOption);
		if (fast) await toggleConfigOption(fast, Boolean(setup.fast), write);
	})().catch(() => {});
}
