import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import { AGENT_LABELS, AGENT_OPTIONS, type AgentId } from "@tallpine/product-ui";
import type { components } from "../../../api/schema";
import { Button } from "../ui/button";
import { Switch } from "../ui/switch";
import { Popover, PopoverContent, PopoverTrigger } from "../ui/popover";

type ApiRole = components["schemas"]["ProjectSkillRole"];
type ApiAccess = components["schemas"]["ProjectSkillAccess"];

export const SKILL_ROLES = ["worker", "orchestrator", "reviewer"] as const;
export type SkillRole = (typeof SKILL_ROLES)[number];

/**
 * One switch per session role. A role is "inherited" when this layer sets no
 * value; "app" when what it inherits is the app-wide switch (it follows the
 * app default), not the role's built-in default.
 */
export type SkillRoleValue = { on: boolean; inherited: boolean; app?: boolean };

/**
 * A row's per-role values: the draft's explicit override, else the daemon's
 * default for that role (reviewers default off).
 */
export function roleValues(roles: readonly ApiRole[] | undefined, override: (role: SkillRole) => boolean | undefined, fallback: (role: SkillRole) => boolean = (role) => role !== "reviewer"): Record<SkillRole, SkillRoleValue> {
	return Object.fromEntries(SKILL_ROLES.map((role) => {
		const set = override(role);
		const api = roles?.find((r) => r.role === role);
		// A daemon that predates per-role switches sends no roles.
		const unset = api?.default ?? fallback(role);
		return [role, { on: set ?? unset, inherited: set === undefined, app: set === undefined && Boolean(api?.app) }];
	})) as Record<SkillRole, SkillRoleValue>;
}

/** The skillRoles entry of the master switch: off cuts the skill off for every role and agent. */
export const MASTER_KEY = "all";
/** The skillRoles entry of one agent type: off cuts the skill off for that agent in every role. */
const AGENT_KEYS: Record<AgentId, string> = { "claude-code": "agentClaudeCode", codex: "agentCodex", kiro: "agentKiro", "deepseek-harness": "agentDeepseek" };
export const agentKey = (id: AgentId) => AGENT_KEYS[id];

/** A skill's master switch and per-agent switches; unset means on. */
export type SkillAccess = { master: SkillRoleValue; agents: Record<AgentId, SkillRoleValue> };

/** A row's master and agent switches: the draft's override, else the daemon's default (the app's value), else on. */
export function accessValues(access: readonly ApiAccess[] | undefined, override: (key: string) => boolean | undefined): SkillAccess {
	const one = (key: string): SkillRoleValue => {
		const set = override(key);
		const api = access?.find((a) => a.key === key);
		return { on: set ?? api?.default ?? true, inherited: set === undefined, app: set === undefined && Boolean(api?.app) };
	};
	return { master: one(MASTER_KEY), agents: Object.fromEntries(AGENT_OPTIONS.map((id) => [id, one(agentKey(id))])) as Record<AgentId, SkillRoleValue> };
}

/**
 * What the switches do on each harness, naming only the cells that are not
 * enforced: "Reviewer: not applied on DeepSeek. Worker, Orchestrator: advisory on DeepSeek."
 */
export function effectNote(roles: readonly ApiRole[] | undefined, t: TFunction): string | undefined {
	const groups = new Map<string, string[]>();
	for (const role of roles ?? []) {
		for (const effect of ["advisory", "unsupported"] as const) {
			const harnesses = role.effects.filter((e) => e.effect === effect).map((e) => AGENT_LABELS[e.harness as keyof typeof AGENT_LABELS] ?? e.harness);
			if (harnesses.length === 0) continue;
			const key = `${effect}|${harnesses.join(", ")}`;
			groups.set(key, [...(groups.get(key) ?? []), t(`settings.instructions.role.${role.role}`)]);
		}
	}
	if (groups.size === 0) return undefined;
	return [...groups].map(([key, names]) => {
		const [effect, harnesses] = key.split("|");
		return t(`settings.instructions.effect.${effect as "advisory" | "unsupported"}`, { roles: names.join(", "), harnesses });
	}).join(" ");
}

/** A row's own harness note, only when it differs from the note shown once above the list. */
export function rowNote(roles: readonly ApiRole[] | undefined, shared: string | undefined, t: TFunction): string | undefined {
	const note = effectNote(roles, t);
	return note === shared ? undefined : note;
}

const COL = "flex w-16 shrink-0 justify-center";
const AGENTS_COL = "w-32 shrink-0";

/** Per-row summary of the agent-type switches: "All agents", "Claude Code, Codex" or "No agents". */
export function agentSummary(access: SkillAccess, t: TFunction): string {
	const on = AGENT_OPTIONS.filter((id) => access.agents[id].on);
	if (on.length === AGENT_OPTIONS.length) return t("settings.instructions.allAgents");
	if (on.length === 0) return t("settings.instructions.noAgents");
	return on.map((id) => AGENT_LABELS[id]).join(", ");
}

/** The column titles above a list of SkillRoleRows; withAccess matches rows that pass `access`. */
export function SkillRoleHeader({ withAccess }: { withAccess?: boolean }) {
	const { t } = useTranslation();
	return <div className="flex items-center gap-3 px-(--settings-row-inset) pt-2.5 text-xs leading-4 text-settings-muted" data-testid="skill-role-header">
		<span className="min-w-0 flex-1" />
		{withAccess ? <><span className={`${COL}`}>{t("settings.instructions.master")}</span><span className={AGENTS_COL}>{t("settings.instructions.agents")}</span></> : null}
		{SKILL_ROLES.map((role) => <span key={role} className={COL}>{t(`settings.instructions.role.${role}`)}</span>)}
		<span className="w-14 shrink-0" />
	</div>;
}

/**
 * One compact line of the Instructions and skills page: a plugin, skill or
 * option with a switch for each session role, aligned under SkillRoleHeader.
 * The project's overrides are the only state; a role without one shows the
 * account value, marked as such. Harness notes show as an icon with a tooltip.
 */
export function SkillRoleRow({ id, name, description, detail, tooltip, note, values, access, disabledRoles, onChange, onAccessChange, onReset }: {
	id: string;
	name: string;
	description?: string;
	/** A path or version line, shown in the description's tooltip. */
	detail?: string;
	/** Secondary text shown only on hover of the name (a skill's path). */
	tooltip?: string;
	/** What the switches do per harness (effectNote). */
	note?: string;
	values: Record<SkillRole, SkillRoleValue>;
	/** The master and agent-type switches; omitted for rows that have none (plugins, options). */
	access?: SkillAccess;
	/** Roles whose switch is read-only, with why (shown as the switch title). */
	disabledRoles?: Partial<Record<SkillRole, string>>;
	onChange: (role: SkillRole, on: boolean) => void;
	/** Sets the master switch (key "all") or an agent's (agentKey). */
	onAccessChange?: (key: string, on: boolean) => void;
	/** Drops this layer's switches so every role inherits again; shown while one is set. */
	onReset?: () => void;
}) {
	const { t } = useTranslation();
	const followsApp = SKILL_ROLES.filter((role) => values[role].app && !disabledRoles?.[role]).map((role) => t(`settings.instructions.role.${role}`));
	const accessSet = access ? [access.master, ...Object.values(access.agents)].some((v) => !v.inherited) : false;
	const overridden = accessSet || SKILL_ROLES.some((role) => !values[role].inherited && !disabledRoles?.[role]);
	const masterOff = access ? !access.master.on : false;
	const hints = [followsApp.length > 0 ? { testid: `skill-role-app-${id}`, text: t("settings.instructions.followsApp", { roles: followsApp.join(", ") }) } : null, note ? { testid: `skill-role-effect-${id}`, text: note } : null].filter((h) => h !== null);
	const subtitle = [description, detail].filter(Boolean).join("\n");
	return <div className="settings-row-bar items-center gap-3 py-1.5" data-testid={`skill-role-row-${id}`}>
		<div className="min-w-0 flex-1" title={[tooltip ? `${name}\n${tooltip}` : name, subtitle].filter(Boolean).join("\n\n")}>
			<div className="flex items-center gap-1.5">
				<span className="min-w-0 truncate text-sm leading-5 text-settings-label">{name}</span>
				{hints.map((h) => <span key={h.testid} tabIndex={0} className="shrink-0 cursor-help rounded text-xs leading-4 text-settings-muted focus-visible:outline focus-visible:outline-1" title={h.text}><span aria-hidden="true">ⓘ</span><span className="sr-only" data-testid={h.testid}>{h.text}</span></span>)}
			</div>
			{(description || detail) && <p className="truncate text-xs leading-4 text-settings-muted">{description ?? detail}</p>}
		</div>
		{access && onAccessChange ? <>
			<label className={COL} title={access.master.inherited ? (access.master.app ? t("settings.instructions.roleAppDefault") : t("settings.instructions.roleDefault")) : undefined}>
				<Switch aria-label={t("settings.instructions.masterSwitch", { name })} checked={access.master.on} onCheckedChange={(on) => onAccessChange(MASTER_KEY, on)} />
			</label>
			<div className={AGENTS_COL}>
				<Popover>
					<PopoverTrigger asChild>
						<button type="button" className="max-w-full truncate rounded-md px-1.5 py-0.5 text-xs leading-4 text-settings-muted hover:bg-muted" data-testid={`skill-agents-${id}`} aria-label={t("settings.instructions.agentsGroup", { name })}>{agentSummary(access, t)}</button>
					</PopoverTrigger>
					<PopoverContent align="start" className="flex w-56 flex-col gap-2 p-3" role="group" aria-label={t("settings.instructions.agentsGroup", { name })}>
						{AGENT_OPTIONS.map((agent) => <label key={agent} className="flex items-center justify-between gap-2 text-sm text-settings-label" title={access.agents[agent].inherited ? (access.agents[agent].app ? t("settings.instructions.roleAppDefault") : t("settings.instructions.roleDefault")) : undefined}>
							<span>{AGENT_LABELS[agent]}</span>
							<Switch aria-label={t("settings.instructions.agentSwitch", { name, agent: AGENT_LABELS[agent] })} checked={access.agents[agent].on} disabled={masterOff} onCheckedChange={(on) => onAccessChange(agentKey(agent), on)} />
						</label>)}
					</PopoverContent>
				</Popover>
			</div>
		</> : null}
		{SKILL_ROLES.map((role) => {
			const value = values[role];
			const roleName = t(`settings.instructions.role.${role}`);
			const inheritedLabel = value.app ? t("settings.instructions.roleAppDefault") : t("settings.instructions.roleDefault");
			return <label key={role} className={COL} title={disabledRoles?.[role] ?? (value.inherited ? inheritedLabel : undefined)}>
				<Switch aria-label={t("settings.instructions.roleSwitch", { name, role: roleName })} checked={value.on} disabled={Boolean(disabledRoles?.[role]) || masterOff} onCheckedChange={(on) => onChange(role, on)} />
				{value.inherited ? <span className="sr-only">{inheritedLabel}</span> : null}
			</label>;
		})}
		<span className="flex w-14 shrink-0 justify-end">{onReset && overridden ? <Button size="sm" variant="ghost" type="button" aria-label={t("settings.instructions.resetSkill", { name })} onClick={onReset}>{t("settings.instructions.reset")}</Button> : null}</span>
	</div>;
}
