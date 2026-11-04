import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import type { AgentPolicyField } from "../../hooks/useAgentPolicy";
import { Switch } from "../ui/switch";
import { AgentPolicyOptionControl } from "./AgentPolicyOptionControl";
import { AgentPolicyLock } from "./AgentPolicyLock";
import { SettingsOptionMenu, type SettingsOption } from "./SettingsOptionMenu";
import { SettingsInlineInput, SettingsRow } from "./SettingsRow";

type Props = {
	field: AgentPolicyField;
	disabled: boolean;
	/** Current value of the sibling harness field, for a model picker. */
	siblingHarness?: string;
	onChange: (value: unknown) => void;
};

function parseList(text: string): string[] {
	return text
		.split(",")
		.map((part) => part.trim())
		.filter(Boolean);
}

/**
 * One policy setting as a Settings row, chosen by the setting's kind. Labels
 * come from the metadata's i18n keys, so a new rule needs no UI code here
 * unless it introduces a new kind.
 */
export function AgentPolicyFieldRow({ field, disabled, siblingHarness = "", onChange }: Props) {
	// Keys arrive from the daemon's rule metadata, not from this file's literals.
	const t = useTranslation().t as unknown as (key: string) => string;
	const readOnly = disabled || field.locked;
	const label = t(field.labelKey);
	const [draft, setDraft] = useState("");
	const text = Array.isArray(field.value) ? field.value.join(", ") : String(field.value ?? "");
	useEffect(() => setDraft(text), [text]);

	let control: React.ReactNode;
	switch (field.kind) {
		case "enum": {
			const options: SettingsOption<string>[] = (field.enum ?? []).map((value) => ({
				value,
				label: field.optionLabelKeys?.[value] ? t(field.optionLabelKeys[value]) : value,
			}));
			control = <SettingsOptionMenu aria-label={label} value={String(field.value)} options={options} onChange={onChange} disabled={readOnly} />;
			break;
		}
		case "bool":
			control = <Switch aria-label={label} checked={field.value === true} disabled={readOnly} onCheckedChange={onChange} />;
			break;
		default:
			if (field.kind === "string" && field.optionSource) {
				control = <AgentPolicyOptionControl field={field} label={label} harness={siblingHarness} readOnly={readOnly} onChange={onChange} />;
				break;
			}
			control = field.locked ? (
				<span className="text-control text-settings-muted">{text || "—"}</span>
			) : (
				<SettingsInlineInput
					id={`agent-policy-${field.name}`}
					label={label}
					value={draft}
					onChange={setDraft}
					onCommit={(value) => onChange(field.kind === "int" ? Number(value) : field.kind === "string" ? value.trim() : parseList(value))}
				/>
			);
	}
	return (
		<SettingsRow label={label} description={field.descriptionKey ? t(field.descriptionKey) : undefined}>
			<div className="flex items-center gap-2">
				<AgentPolicyLock source={field.source} />
				{control}
			</div>
		</SettingsRow>
	);
}
