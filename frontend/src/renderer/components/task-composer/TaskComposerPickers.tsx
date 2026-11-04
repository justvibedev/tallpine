/** Effort, agent and model pickers rendered inside the TaskComposer menus. */
import { type TaskComposerAgentControl, type TaskComposerEffortControl, type TaskComposerModelControl } from "@tallpine/product-ui";
import { type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { RequiredAgentField } from "../CreateProjectAgentSheet";
import { agentModelDisplayLabel } from "../../lib/agent-model-choices";
import { NativeModePicker } from "../NativeModePicker";
import { AgentModelCombobox } from "../settings/AgentModelCombobox";
import { EffortPicker, type EffortAvailability } from "../settings/EffortPicker";

// The agent, model and effort menus share one compact width.
export const COMPOSER_MENU_WIDTH = "w-[min(14rem,calc(100vw-2rem))]! min-w-0! max-w-[calc(100vw-2rem)]!";

export function TaskEffortPicker({
	disabled,
	label,
	onChange,
	options,
	value,
	defaultEffort,
	availability,
	footer,
}: TaskComposerEffortControl & { defaultEffort?: string; availability: EffortAvailability; footer?: ReactNode }) {
	return (
		<EffortPicker
			label={label}
			disabled={disabled}
			value={value.toLowerCase() === "default" ? "" : value}
			choices={options.map((option) => ({ value: option }))}
			defaultEffort={defaultEffort}
			availability={availability}
			onChange={onChange}
			triggerClassName="composer-chip composer-toolbar-option w-fit"
			menuClassName={COMPOSER_MENU_WIDTH}
			footer={footer}
		/>
	);
}

// Lists only harnesses that can run, plus a way to manage their logins.
export function DesktopAgentControl({ hostId, ...control }: TaskComposerAgentControl & { hostId?: string }) {
	const { t } = useTranslation();
	return (
		<RequiredAgentField
			{...control}
			hostId={hostId}
			managementLabel={t("agentSelector.manage").replace(/[.…]+$/u, "")}
			variant="chip"
			triggerClassName="composer-toolbar-option w-fit"
			contentClassName={COMPOSER_MENU_WIDTH}
		/>
	);
}

export function TaskModelPicker({
	agentId,
	agentLabel,
	catalog,
	disabled,
	value,
	mode,
	onModelChange,
	onModeChange,
	onRefresh,
	showFollowAgentAction,
}: TaskComposerModelControl & { onRefresh: () => Promise<void>; showFollowAgentAction: boolean }) {
	const { t } = useTranslation();

	// Keep the no-agent placeholder until there is an agent to configure.
	if (agentId === "") {
		return (
			<span
				className="composer-chip composer-toolbar-option w-fit cursor-not-allowed justify-start opacity-50"
				aria-disabled="true"
				aria-label={t("newTask.model")}
			>
				<span className="truncate text-settings-muted">{t("newTask.selectAgent")}</span>
			</span>
		);
	}

	if (catalog?.selectionMode === "mode") {
		return (
			<NativeModePicker
				models={catalog.models ?? []}
				mode={mode}
				onModeChange={onModeChange}
				disabled={disabled}
				showFollowAgentAction={showFollowAgentAction}
				triggerClassName="composer-chip composer-toolbar-option w-fit"
				menuClassName={COMPOSER_MENU_WIDTH}
			/>
		);
	}

	const displayModels = (catalog?.models ?? []).map((item) => {
		if (item.id === "auto") return { ...item, label: t("settings.models.autoRouteLabel") };
		return { ...item, label: agentModelDisplayLabel(agentId, item.label) };
	});
	const selectCatalogModel = (nextModel: string) => {
		onModelChange(nextModel);
	};

	return (
		<AgentModelCombobox
			key={agentId}
			aria-label={t("newTask.model")}
			value={value}
			models={displayModels}
			allowCustom={false}
			customModelEntry={catalog?.customModelEntry === "configured" ? "configured" : "none"}
			agentLabel={agentLabel}
			onRefresh={onRefresh}
			refreshing={catalog?.refreshState === "queued" || catalog?.refreshState === "refreshing"}
			refreshError={catalog?.refreshError}
			retryAt={catalog?.retryAt}
			disabled={disabled || agentId === ""}
			agentId={agentId}
			onChange={selectCatalogModel}
			compact
			recentScope={agentId}
			triggerClassName="composer-chip composer-toolbar-option w-fit"
			menuAlign="start"
			menuClassName={COMPOSER_MENU_WIDTH}
			renderTrigger={(label) => <span className="min-w-0 truncate text-control text-foreground" title={label}>{label}</span>}
		/>
	);
}
