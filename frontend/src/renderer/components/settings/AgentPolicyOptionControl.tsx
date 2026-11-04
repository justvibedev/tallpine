import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import type { AgentPolicyField } from "../../hooks/useAgentPolicy";
import { agentModelsQueryOptions } from "../../hooks/useAgentModelsQuery";
import { useOfferedModels } from "../../hooks/useOfferedModels";
import { AGENT_OPTIONS, agentLabel } from "../../lib/agent-options";
import { agentModelDisplayLabel, isConcreteModelID } from "../../lib/agent-model-choices";
import { SettingsOptionMenu, type SettingsOption } from "./SettingsOptionMenu";

type Props = {
	field: AgentPolicyField;
	label: string;
	/** Value of the harness field a model field depends on. */
	harness: string;
	readOnly: boolean;
	onChange: (value: string) => void;
};

/**
 * A policy string setting that declares an option source: a picker of the
 * supported agent harnesses, or of the models of the locked harness. Empty means
 * the project decides, matching the other policy dropdowns.
 */
export function AgentPolicyOptionControl({ field, label, harness, readOnly, onChange }: Props) {
	const { t } = useTranslation();
	const value = String(field.value ?? "");
	const unlocked = t("settings.agentPolicy.projectDecides");
	if (field.optionSource === "harness") {
		const known: string[] = [...AGENT_OPTIONS];
		const options: SettingsOption<string>[] = [
			{ value: "", label: unlocked },
			// A managed value outside the list stays visible so it can be replaced.
			...(value && !known.includes(value) ? [{ value, label: value }] : []),
			...known.map((id) => ({ value: id, label: agentLabel(id) })),
		];
		return <SettingsOptionMenu aria-label={label} value={value} options={options} onChange={onChange} disabled={readOnly} />;
	}
	return <PolicyModelControl label={label} harness={harness} value={value} readOnly={readOnly} unlocked={unlocked} onChange={onChange} />;
}

function PolicyModelControl({
	label,
	harness,
	value,
	readOnly,
	unlocked,
	onChange,
}: {
	label: string;
	harness: string;
	value: string;
	readOnly: boolean;
	unlocked: string;
	onChange: (value: string) => void;
}) {
	const query = useQuery({ ...agentModelsQueryOptions(harness, ""), enabled: harness !== "" });
	const { models: offered } = useOfferedModels(harness, query.data?.models, { keep: [value] });
	if (harness === "") {
		return (
			<SettingsOptionMenu
				aria-label={label}
				value={value}
				// A model-only lock is valid: keep it visible and clearable.
				options={[{ value: "", label: unlocked }, ...(value ? [{ value, label: value }] : [])]}
				disabled={readOnly || value === ""}
				onChange={onChange}
			/>
		);
	}
	const options: SettingsOption<string>[] = [
		{ value: "", label: unlocked },
		...offered
			.filter((item) => isConcreteModelID(item.id))
			.map((item) => ({ value: item.id, label: agentModelDisplayLabel(harness, item.label) })),
	];
	// A managed model the catalog does not list stays visible and replaceable.
	if (value && !options.some((o) => o.value === value)) options.push({ value, label: value });
	return <SettingsOptionMenu aria-label={label} value={value} options={options} disabled={readOnly || (query.isFetching && !query.data)} onChange={onChange} />;
}
