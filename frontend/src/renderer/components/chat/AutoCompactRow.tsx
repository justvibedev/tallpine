import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
	AUTO_COMPACT_PERCENTS,
	AUTO_COMPACT_PERCENT_MAX,
	AUTO_COMPACT_PERCENT_MIN,
	parseAutoCompactPercent,
	type AutoCompactChoice,
} from "../../lib/auto-compact";
import type { TurnSettings } from "../../types/conversation";

export interface AutoCompactControl {
	/** Unset leaves it to the Settings default, else the agent. */
	value?: TurnSettings["autoCompactWindow"];
	/** The agent is working or already restarting. */
	disabled?: boolean;
	onChange: (value: TurnSettings["autoCompactWindow"]) => void;
	/** The Settings default for this agent, named on the Auto option. */
	defaultChoice?: string;
}

const TOKEN_CHOICES: { value: AutoCompactChoice; label: string }[] = [
	{ value: "200k", label: "200k" },
	{ value: "1m", label: "1M" },
];

const CUSTOM = "custom";

/**
 * The compaction choices as a select: Auto, a percent of the model window, or a
 * token preset. "Custom" takes any other whole percent from 20 to 95; a saved
 * percent that is not one of the listed steps shows as its own option.
 */
export function AutoCompactSelect({
	value,
	disabled,
	onChange,
	defaultChoice,
	id,
	className,
}: AutoCompactControl & { id?: string; className?: string }) {
	const { t } = useTranslation();
	const isOffStep = Boolean(value?.endsWith("%")) && !(AUTO_COMPACT_PERCENTS as readonly string[]).includes(value as string);
	const [custom, setCustom] = useState(false);
	useEffect(() => { if (!isOffStep) setCustom(false); }, [isOffStep, value]);
	return (
		<span className={className ?? "ml-auto flex items-center gap-1"}>
			<select
				id={id}
				aria-label={t("agentSwitch.autoCompact")}
				value={custom ? CUSTOM : (value ?? "")}
				disabled={disabled}
				onChange={(event) => {
					if (event.target.value === CUSTOM) { setCustom(true); return; }
					setCustom(false);
					onChange((event.target.value || undefined) as TurnSettings["autoCompactWindow"]);
				}}
				className="rounded-md bg-interactive-active px-2 py-0.5 text-foreground focus-visible:outline-2 focus-visible:outline-ring disabled:cursor-default disabled:opacity-60"
			>
				<option value="">{defaultChoice ? t("agentSwitch.autoCompactDefault", { choice: defaultChoice.toUpperCase() }) : t("agentSwitch.autoCompactAuto")}</option>
				{AUTO_COMPACT_PERCENTS.map((percent) => (
					<option key={percent} value={percent}>{t("agentSwitch.autoCompactPercent", { percent })}</option>
				))}
				{isOffStep && !custom ? <option value={value}>{t("agentSwitch.autoCompactPercent", { percent: value })}</option> : null}
				<option value={CUSTOM}>{t("agentSwitch.autoCompactCustom")}</option>
				{TOKEN_CHOICES.map((choice) => (
					<option key={choice.value} value={choice.value}>{choice.label}</option>
				))}
			</select>
			{custom ? <CustomPercentInput disabled={disabled} initial={parseAutoCompactPercent(value)} onCommit={(percent) => { setCustom(false); onChange(`${percent}%`); }} /> : null}
		</span>
	);
}

/** A whole percent from 20 to 95, committed on Enter or blur and only when it is in range. */
function CustomPercentInput({ initial, disabled, onCommit }: { initial?: number; disabled?: boolean; onCommit: (percent: number) => void }) {
	const { t } = useTranslation();
	const [text, setText] = useState(initial === undefined ? "" : String(initial));
	const percent = /^\d{1,2}$/.test(text) ? Number(text) : undefined;
	const valid = percent !== undefined && percent >= AUTO_COMPACT_PERCENT_MIN && percent <= AUTO_COMPACT_PERCENT_MAX;
	const commit = () => { if (valid) onCommit(percent); };
	return (
		<input
			autoFocus
			inputMode="numeric"
			aria-label={t("agentSwitch.autoCompactCustomLabel", { min: AUTO_COMPACT_PERCENT_MIN, max: AUTO_COMPACT_PERCENT_MAX })}
			aria-invalid={text !== "" && !valid}
			placeholder="%"
			value={text}
			disabled={disabled}
			maxLength={2}
			onChange={(event) => setText(event.target.value)}
			onBlur={commit}
			onKeyDown={(event) => { if (event.key === "Enter") { event.preventDefault(); commit(); } }}
			className="w-12 rounded-md bg-interactive-active px-2 py-0.5 text-foreground aria-[invalid=true]:outline aria-[invalid=true]:outline-error focus-visible:outline-2 focus-visible:outline-ring"
		/>
	);
}

/** A menu row: where the agent compacts on its own. */
export function AutoCompactRow({
	title,
	unsupported,
	...control
}: AutoCompactControl & { title: string; unsupported?: boolean }) {
	const { t } = useTranslation();
	return (
		<div className="flex h-9 items-center gap-2 px-3 text-xs" title={title}>
			<span className="text-muted-foreground">{t("agentSwitch.autoCompact")}</span>
			{unsupported ? (
				<span className="ml-auto text-muted-foreground">{t("agentSwitch.autoCompactUnsupported")}</span>
			) : (
				<AutoCompactSelect {...control} />
			)}
		</div>
	);
}
