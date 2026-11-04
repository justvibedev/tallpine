/** Props and shared types for the agent model combobox. */
import { type ReactNode } from "react";
import type { AgentModelCatalog } from "../../../hooks/useAgentModelsQuery";
import { type ModelTuningControlsProps } from "../ModelTuningControls";

export type ModelEffortSelection = Pick<ModelTuningControlsProps,
	"effort" | "effortsWithoutModel" | "onEffortChange" | "onEffortReset" | "onValidityChange" | "roleLabel"
>;

export type AgentModel = NonNullable<AgentModelCatalog["models"]>[number];

export type AgentModelComboboxProps = {
	value: string;
	models: AgentModel[];
	allowCustom?: boolean;
	customModelEntry?: AgentModelCatalog["customModelEntry"];
	agentLabel?: string;
	onRefresh?: () => void | Promise<void>;
	refreshing?: boolean;
	refreshError?: string;
	retryAt?: string | null;
	onChange: (value: string) => void;
	onCustom?: (value: string) => void;
	/** Shown when the agent does not report a concrete model. */
	emptyLabel?: string;
	/** Claude Code lists its newest model per family and folds the rest under "Other models". */
	agentId?: string;
	triggerLabel?: string;
	triggerClassName?: string;
	menuAlign?: "start" | "center" | "end";
	menuClassName?: string;
	renderTrigger?: (label: string) => ReactNode;
	/** Persists explicit model choices for this agent and pins them below the current model. */
	recentScope?: string;
	/** Flat model names with no groups or badges.
	 *  Search still shows once the catalog passes MODEL_SEARCH_THRESHOLD,
	 *  same as non-compact mode; only the grouping/decoration is stripped. For
	 *  contexts where the menu should read like a simple choice, not a
	 *  model-management surface. */
	compact?: boolean;
	/** Keep the effort level inside the menu only, off the trigger label. */
	showEffortInTrigger?: boolean;
	/** Callers opt into a combined model and reasoning-effort menu. */
	tuning?: ModelEffortSelection;
	disabled?: boolean;
	"aria-label": string;
};
