/** Props of the TurnSettingsBar. */
import type { ReactNode } from "react";
import type { AgentSwitchProps } from "../AgentSwitchPicker";
import type { ApprovalMode, ChatConfigOption, ChatConfigOptionValue, ChatModel, ModelReroute, TurnSettings } from "../../../types/conversation";

export interface TurnSettingsBarProps {
	models: ChatModel[];
	settings: TurnSettings;
	/** The active provider selects its own supported permission vocabulary. */
	harness?: string;
	/** Cloud permissions are fixed for the session, not a per-turn setting. */
	showApprovalMode?: boolean;
	approvalModes?: ApprovalMode[];
	/**
	 * The provider answered with a different model than the one chosen. Separate from
	 * `settings` all the way down: settings are what the user asked for, this is what
	 * replied, and folding them together is how the control ends up advertising a
	 * model that is not the one producing the answers.
	 */
	reroute?: ModelReroute;
	/** `picked` is set when the user chose that setting from its own menu. */
	onChange?: (next: TurnSettings, picked?: keyof TurnSettings) => void;
	/** `null` forgets the project's remembered mode. */
	onRememberPermissions?: (mode: ApprovalMode | null) => Promise<unknown> | void;
	rememberPermissionsPending?: boolean;
	rememberPermissionsError?: string;
	rememberedPermissionMode?: ApprovalMode;
	/** Controls advertised by an ACP agent for this exact live session. */
	configOptions?: ChatConfigOption[];
	onChangeConfigOption?: (
		optionId: string,
		value: ChatConfigOptionValue,
	) => Promise<unknown> | void;
	/** Prevent overlapping writes because provider responses replace the catalog. */
	configPending?: boolean;
	/**
	 * Pick an effort as soon as this opens when the model reports levels but no
	 * default. Only for a conversation with no turns yet, so reopening an existing
	 * one never changes the effort it was running with. Choosing another model
	 * always selects one regardless.
	 */
	autoSelectEffortOnOpen?: boolean;
	error?: string;
	disabled?: boolean;
	/** Turns the model control into one picker for agent, model and effort, laid out before the permissions. */
	agentSwitch?: AgentSwitchProps;
	/** Drawn just before that picker, such as the context meter. */
	beforePicker?: ReactNode;
	/** The agent is starting again; the picker names no model until it reports its own. */
	modelsLoading?: boolean;
	/** The agent is not running: the bar shows the saved model and a pick is kept for its next start. */
	stopped?: boolean;
	/** The model the chat's agent last reported running, when none is saved. */
	lastModel?: string;
	/** Inline controls on the right model row, before the mode/approval picker — queue vs steer. */
	children?: ReactNode;
}
