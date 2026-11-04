import { agentModelDisplayLabel } from "../../lib/agent-model-choices";
import type { ChatConfigOption } from "../../types/conversation";

/**
 * The model a session is actually running, read from the agent's own model
 * option: the id plus the name the composer's picker shows. A chat on the
 * provider default has no saved model, but its agent still reports one.
 */
export function liveModel(harness: string | undefined, configOptions: ChatConfigOption[] | undefined): { id: string; label: string } | undefined {
	const option = configOptions?.find((entry) => entry.category === "model" || entry.id === "model");
	const id = option?.currentValue;
	if (!option || !id || id === "default") return undefined;
	const name = option.choices.find((choice) => choice.value === id)?.name;
	return { id, label: agentModelDisplayLabel(harness, name ?? id) };
}
