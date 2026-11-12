import type { TurnSettingsBarProps } from "./turn-settings/types";
import { useMemo } from "react";
import { ListTodo, Shuffle, TriangleAlert } from "lucide-react";
import { useTranslation } from "react-i18next";
import { OptionMenuItem } from "../ui/option-menu";
import { fallbackEffort, useApplyEffortDefault } from "../../lib/effort";
import { cn } from "../../lib/utils";
import { formatEffortLabel } from "../settings/EffortPicker";
import { agentModelDisplayLabel } from "../../lib/agent-model-choices";
import { agentLabel } from "../../lib/agent-options";
import { AgentModelPicker, ToggleRow, type PickerSource } from "./AgentSwitchPicker";
import { AutoCompactRow } from "./AutoCompactRow";
import { supportsAutoCompact } from "../../lib/auto-compact";
import { providerQualifiedChoice } from "../../lib/model-variants";
import type { ApprovalMode, ChatConfigOption, ChatConfigOptionValue } from "../../types/conversation";
import { claudeChoiceLabels, dedupeChoicesByName, acpFallbackEffort, EffortOptionSubmenu, ModelEffortPicker, ClubbedConfigPicker } from "./turn-settings/model-pickers";
import { isModelOption, partitionConfigOptions, isPlanBinary, isPlanMode, isPlanChoice, isFastOption, isThinkingToggle, optionIsEnabled, setConfigToggle, optionCurrentLabel } from "./turn-settings/config-options";
import { applyModelSetup } from "./turn-settings/apply-model-setup";
import { CODEX_APPROVAL_COPY, APPROVAL_COPY, CODEX_APPROVAL_ORDER, APPROVAL_ORDER, TRIGGER_CLASS } from "./turn-settings/approval-copy";
import { ULTRACODE, Picker } from "./turn-settings/composer-menu";
import { OptionSubmenu, MoreOptionsSubmenu, restoreComposerFocus, ExecutionModePicker, ConfigOptionPicker } from "./turn-settings/config-pickers";
import { permissionIcon, permissionLook, PermissionRow } from "./turn-settings/permission";

/**
 * What the next turn will be sent with: model, reasoning effort, approval mode.
 *
 * All three are per-turn on the provider's side, so choosing one changes the next
 * message and never restarts the agent — the running turn keeps what it was
 * dispatched with. That is why this sits in the composer rather than in settings.
 *
 * The catalog comes from the provider, not from a list in Tallpine. Models are added,
 * renamed, hidden per account and gated by entitlement Tallpine cannot see, so a
 * hardcoded list would be wrong within a week. An agent whose provider cannot
 * enumerate models reports none and the model control hides itself.
 *
 * ACP agents advertise those same dimensions as live session options. They share
 * this chrome rather than each growing a row of pickers: model and thought level
 * club into the left-hand control, while a provider-owned mode (such as planning)
 * stays separate from Tallpine's approval policy. The lists inside are still the
 * provider's; only the grouping of the triggers is Tallpine's.
 */
export function TurnSettingsBar({
	models,
	settings,
	harness,
	showApprovalMode = true,
	approvalModes,
	reroute,
	onChange,
	onRememberPermissions,
	rememberPermissionsPending,
	rememberPermissionsError,
	rememberedPermissionMode,
	configOptions,
	onChangeConfigOption,
	configPending,
	error,
	disabled,
	autoSelectEffortOnOpen = false,
	agentSwitch,
	beforePicker,
	modelsLoading,
	stopped,
	lastModel,
	children,
}: TurnSettingsBarProps) {
	const { t } = useTranslation();
	const displayModels = useMemo(
		() => models.map((model) => ({
			...model,
			displayName: agentModelDisplayLabel(harness, model.displayName),
		})),
		[harness, models],
	);
	const claudeLabels = useMemo(() => (harness === "claude-code" ? claudeChoiceLabels(displayModels) : undefined), [harness, displayModels]);
	const displayConfigOptions = useMemo(
		() => (configOptions ?? []).map((option) => isModelOption(option) ? {
			...option,
			// Claude Code can list one model twice (alias and start id); it shows once.
			choices: dedupeChoicesByName(option.choices.map((choice) => providerQualifiedChoice(choice, harness ?? "")).map((choice) => ({
				...choice,
				name: claudeLabels?.(choice.value, choice.name) ?? agentModelDisplayLabel(harness, choice.name),
				description: choice.description
					? agentModelDisplayLabel(harness, choice.description)
					: choice.description,
			})), option.currentValue),
		} : option),
		[claudeLabels, configOptions, harness],
	);
	const selected = displayModels.find((model) => model.id === settings.model);
	const fallback = settings.model ? undefined : displayModels.find((model) => model.default);
	// A catalog miss must not relabel an explicit choice or borrow another model's
	// effort settings. Custom or newly available models may not be listed yet.
	const chosenLabel =
		selected?.displayName ?? settings.model ?? fallback?.displayName ?? "Choose a model";
	const rerouted = reroute
		? displayModels.find((model) => model.id === reroute.toModel)?.displayName ?? reroute.toModel
		: undefined;
	const modelLabel = rerouted ?? chosenLabel;
	const availableEfforts = (selected ?? fallback)?.efforts ?? [];
	const efforts = availableEfforts.filter((effort) => effort.toLowerCase() !== "default");
	const selectedEffort =
		settings.reasoningEffort && settings.reasoningEffort !== "default"
			? settings.reasoningEffort
			: (selected ?? fallback)?.defaultEffort;
	const effortLabel = selectedEffort === "default" ? undefined : selectedEffort;
	const approvalCopy = harness === "codex" ? CODEX_APPROVAL_COPY : APPROVAL_COPY;
	const approvalOrder = harness === "codex" ? CODEX_APPROVAL_ORDER : APPROVAL_ORDER;
	const approvalLabel = approvalCopy[settings.approvalMode ?? "default"].label;
	const modelGroupLabel = effortLabel
		? `${modelLabel} ${formatEffortLabel(effortLabel, t)}`
		: modelLabel;
	const grouped = partitionConfigOptions(displayConfigOptions, true);
	const optionDisabled = Boolean(disabled || configPending || rememberPermissionsPending);
	// A choice the installed CLI is too old for is shown, disabled, but never sent.
	const choiceUnavailable = (optionId: string, id: unknown) =>
		Boolean(configOptions?.find((option) => option.id === optionId)?.choices.find((choice) => choice.value === id)?.unavailableReason);
	const applyOption = (optionId: string, value: ChatConfigOptionValue) => {
		if (!onChangeConfigOption || ("value" in value && choiceUnavailable(optionId, value.value))) return;
		void Promise.resolve(onChangeConfigOption(optionId, value)).catch(() => {});
	};
	const nativeModelMenu = Boolean(onChange && displayModels.length > 0 && grouped.model.length === 0);
	// A model with effort levels but no provider default would otherwise show no
	// selected effort. Tallpine picks a level and sets it, so what the picker shows is
	// what the next turn uses.
	const acpEffortOption = grouped.effort.at(0);
	useApplyEffortDefault(
		`${harness}:${grouped.model.map((option) => option.currentValue).join(":")}`,
		acpEffortOption ? acpFallbackEffort(acpEffortOption) : undefined,
		(value) => {
			if (acpEffortOption) applyOption(acpEffortOption.id, { value });
		},
		{ disabled: optionDisabled || !onChangeConfigOption, applyOnMount: autoSelectEffortOnOpen },
	);
	const nativeEffortUnset = !settings.reasoningEffort || settings.reasoningEffort === "default";
	useApplyEffortDefault(
		`${harness}:${settings.model ?? fallback?.id}`,
		// Only for a model this agent lists: just after a switch the settings can still be
		// the previous agent's, and saving an effort would carry its model onto the new one.
		nativeModelMenu && nativeEffortUnset && (selected ?? fallback) ? fallbackEffort(efforts, (selected ?? fallback)?.defaultEffort) : undefined,
		(value) => onChange?.({ ...settings, reasoningEffort: value }),
		{ disabled: optionDisabled || !onChange, applyOnMount: autoSelectEffortOnOpen },
	);
	const modeOption = grouped.mode;
	const inlineExecutionMode =
		grouped.executionMode && isPlanBinary(grouped.executionMode) ? grouped.executionMode : undefined;
	const standaloneExecutionMode =
		grouped.executionMode && !isPlanBinary(grouped.executionMode) ? grouped.executionMode : undefined;
	const planning = isPlanMode(grouped.executionMode);
	// Leaving Plan returns to the approval mode the session was on, not to the
	// provider's bare agent mode, which would silently drop the policy while the
	// picker still displayed it.
	const planReturn = modeOption?.choices.find(
		(choice) => choice.permissionMode === (settings.approvalMode ?? "default"),
	)?.value;
	const clubbedLeft =
		grouped.model.length > 0 ||
		grouped.effort.length > 0 ||
		Boolean(inlineExecutionMode) ||
		grouped.toggles.length > 0 ||
		grouped.extra.length > 0;
	const rememberMode = modeOption
		? modeOption.choices.find((choice) => choice.value === modeOption.currentValue)?.permissionMode
		: settings.approvalMode ?? "default";
	// Codex's default is Full access, which the project stores as bypass.
	const remembered = rememberMode !== undefined && rememberedPermissionMode !== undefined && (
		rememberedPermissionMode === rememberMode ||
		(harness === "codex" && rememberMode === "default" && rememberedPermissionMode === "bypass-permissions")
	);
	const rememberAction = onRememberPermissions && rememberMode && !planning ? (
		<ToggleRow
			label="Remember for this project"
			checked={remembered}
			onChange={optionDisabled ? undefined : (on) => {
				void Promise.resolve(onRememberPermissions(on ? rememberMode : null)).catch(() => {});
			}}
			className="mt-1 border-t border-border pt-2"
		/>
	) : null;
	const unified = Boolean(agentSwitch && harness);
	// The unified bar keeps the permission picker beside the Plan chip.
	const showPermissions = unified || !planning;
	const showRightDropdown = Boolean(children || (showPermissions && ((showApprovalMode && onChange) || modeOption)));
	const planOn = planning || Boolean(settings.planMode);
	const planLeaveValue = planReturn ?? grouped.executionMode?.choices.find((choice) => !isPlanChoice(choice))?.value;
	const leavePlan = () => {
		if (settings.planMode) onChange?.({ ...settings, planMode: undefined });
		else if (grouped.executionMode && planLeaveValue) applyOption(grouped.executionMode.id, { value: planLeaveValue });
	};
	// While planning, a provider whose one mode setting holds plan shows the mode it returns to.
	const shownModeOption = modeOption && planning && planLeaveValue
		? { ...modeOption, currentValue: planLeaveValue }
		: modeOption;
	// With another agent picked, the permission control is the mode it will start in,
	// which it may not share with the chat (Kimi runs only in its default, Pi only bypassed).
	const pending = agentSwitch?.pending;
	const pendingModes = pending ? agentSwitch?.pendingPermissionModes : undefined;
	const pendingAgent = pending ? agentLabel(pending.harness) : "";
	const pendingRuns = (mode: ApprovalMode) => !pendingModes || pendingModes.includes(mode);
	const chatMode = shownModeOption?.choices.find((choice) => choice.value === shownModeOption.currentValue)?.permissionMode
		?? settings.approvalMode ?? "default";
	const pendingWanted = pending?.approvalMode ?? agentSwitch?.chatPermissionMode ?? chatMode;
	const pendingMode = pendingRuns(pendingWanted) ? pendingWanted : pendingModes?.[0] ?? pendingWanted;
	const [pendingCopy, pendingOrder] = pending?.harness === "codex" ? [CODEX_APPROVAL_COPY, CODEX_APPROVAL_ORDER] as const : [APPROVAL_COPY, APPROVAL_ORDER] as const;
	const startsInNote = pendingMode !== pendingWanted
		? t("agentSwitch.startsInMode", { agent: pendingAgent, mode: pendingCopy[pendingWanted].label, target: pendingCopy[pendingMode].label })
		: undefined;
	const primaryModelOption = grouped.model.at(0);
	const fastOption = grouped.toggles.find(isFastOption);
	const thinkingOption = grouped.toggles.find(isThinkingToggle);
	const fastControls = fastOption && onChangeConfigOption
		? { fast: optionIsEnabled(fastOption), onToggleFast: (on: boolean) => setConfigToggle(fastOption, on, applyOption) }
		: {};
	const nativeFastControls = (selected ?? fallback)?.supportsFast && onChange
		? { fast: Boolean(settings.fastMode), onToggleFast: (on: boolean) => onChange({ ...settings, fastMode: on || undefined }) }
		: fastControls;
	const { ultracode, autoCompact } = agentSwitch ?? {};
	// Ultracode runs at Extra High, so it is only for models that have that level.
	const offersUltracode = Boolean(
		ultracode && harness === "claude-code" && onChangeConfigOption &&
		acpEffortOption?.choices.some((choice) => choice.value === "xhigh"),
	);
	const autoCompactSupport = supportsAutoCompact(harness, primaryModelOption?.currentValue, false);
	const offersAutoCompact = Boolean(autoCompact) && autoCompactSupport !== "no";
	// A model the installed CLI is too old for is shown, disabled, but never sent.
	const changeNativeModel = (next: typeof settings) => {
		if (!displayModels.find((model) => model.id === next.model)?.unavailableReason) onChange?.(next);
	};
	const current: PickerSource = nativeModelMenu && onChange
		? {
			models: (harness === "claude-code" ? displayModels.filter((model) => model.id !== "default") : displayModels)
				.map((model) => ({ id: model.id, label: model.displayName, unavailableReason: model.unavailableReason })),
			modelId: settings.model ?? fallback?.id,
			modelLabel,
			// Fast mode is kept only where the newly picked model offers it.
			onPickModel: (id) => changeNativeModel({
				...settings,
				model: id,
				reasoningEffort: undefined,
				fastMode: displayModels.find((model) => model.id === id)?.supportsFast ? settings.fastMode : undefined,
			}),
			efforts: efforts.map((value) => ({ value, label: formatEffortLabel(value, t) })),
			effort: effortLabel,
			onPickEffort: (value) => onChange({ ...settings, reasoningEffort: value }),
			onPickSetup: (setup) => changeNativeModel({
				...settings,
				model: setup.model,
				reasoningEffort: setup.effort,
				fastMode: (setup.fast && displayModels.find((model) => model.id === setup.model)?.supportsFast) || undefined,
			}),
			...nativeFastControls,
		}
		: {
			models: (primaryModelOption?.choices ?? [])
				.map((choice) => ({
					id: choice.value, label: choice.name, group: choice.groupName || choice.group,
					aliases: (choice as ChatConfigOption["choices"][number] & { aliases?: string[] }).aliases,
					unavailableReason: choice.unavailableReason,
				})),
			modelId: primaryModelOption?.currentValue,
			modelLabel: primaryModelOption ? optionCurrentLabel(primaryModelOption) : agentLabel(harness ?? ""),
			onPickModel: primaryModelOption && onChangeConfigOption ? (id) => applyOption(primaryModelOption.id, { value: id }) : undefined,
			// The effort, Thinking and Fast are set after the model, because the agent offers them
			// per model: each is read from the options the previous write returned, not the outgoing
			// model's. Fast is set either way, so a saved setup without it turns a paid Fast off.
			onPickSetup: primaryModelOption && onChangeConfigOption
				? (setup) => {
					if (choiceUnavailable(primaryModelOption.id, setup.model)) return;
					ultracode?.onChange(false);
					applyModelSetup(configOptions ?? [], primaryModelOption.id, setup, onChangeConfigOption);
				}
				: undefined,
			efforts: [
				...(acpEffortOption?.choices ?? [])
					.filter((choice) => choice.value && choice.value !== "default")
					.map((choice) => ({ value: choice.value, label: choice.name })),
				...(offersUltracode ? [{ value: ULTRACODE, label: "Ultracode" }] : []),
			],
			effort: offersUltracode && ultracode?.on
				? ULTRACODE
				: acpEffortOption?.currentValue && acpEffortOption.currentValue !== "default"
					? acpEffortOption.currentValue
					: undefined,
			onPickEffort: acpEffortOption && onChangeConfigOption
				? (value) => {
					ultracode?.onChange(value === ULTRACODE);
					applyOption(acpEffortOption.id, { value: value === ULTRACODE ? "xhigh" : value });
				}
				: undefined,
			...fastControls,
			...(thinkingOption && onChangeConfigOption
				? { thinking: optionIsEnabled(thinkingOption), onToggleThinking: (on: boolean) => setConfigToggle(thinkingOption, on, applyOption) }
				: {}),
		};
	// Plan and on/off settings live in the add menu; here is anything past the first model and effort.
	const hasPickerExtras = unified && Boolean(onChangeConfigOption) && (offersAutoCompact || grouped.extra.length > 0 ||
		(!nativeModelMenu && (grouped.model.length > 1 || grouped.effort.length > 1)));
	const pickerExtras = hasPickerExtras ? (
		<>
			{offersAutoCompact && autoCompact ? (
				<AutoCompactRow
					{...autoCompact}
					unsupported={autoCompactSupport === "unsupported"}
					title={t("agentSwitch.autoCompactHint")}
				/>
			) : null}
			{nativeModelMenu ? null : grouped.model.slice(1).map((option) => (
				<OptionSubmenu key={option.id} option={option} onChange={applyOption} scrollable />
			))}
			{nativeModelMenu ? null : grouped.effort.slice(1).map((option) => (
				<EffortOptionSubmenu key={option.id} option={option} onChange={applyOption} />
			))}
			{grouped.extra.length > 0 ? <MoreOptionsSubmenu options={grouped.extra} onChange={applyOption} /> : null}
		</>
	) : null;

	return (
		<div role="group" aria-label="Turn settings" className="flex min-w-0 flex-1 flex-col gap-0.5">
			<div className="flex h-7 min-w-0 flex-1 items-center justify-between gap-2">
				<div className="flex h-7 min-w-0 flex-1 items-center gap-0.5">
					{unified && agentSwitch && harness ? (
						<>
							{beforePicker}
							<AgentModelPicker
								currentHarness={harness}
								current={current}
								agentSwitch={agentSwitch}
								disabled={optionDisabled}
								// Only an agent that sends back its own settings has any to load; Tallpine's would just flicker.
								loading={configPending && !nativeModelMenu}
								loadingModels={modelsLoading || stopped}
								onPickSaved={stopped && onChange ? (model) => onChange({ ...settings, model, reasoningEffort: undefined }) : undefined}
								savedModel={settings.model || lastModel}
								title={reroute ? `The provider answered with ${rerouted} instead of ${reroute.fromModel ?? chosenLabel}` : undefined}
								mark={reroute ? <Shuffle className="size-3 shrink-0 text-warning" aria-hidden="true" /> : undefined}
								extras={pickerExtras}
								onCloseAutoFocus={restoreComposerFocus}
							/>
						</>
					) : null}
					{!unified && nativeModelMenu && onChange ? (
						<ModelEffortPicker
							models={harness === "claude-code" ? displayModels.filter((model) => model.id !== "default") : displayModels}
							settings={settings}
							onChange={onChange}
							disabled={optionDisabled}
							modelLabel={modelLabel}
							groupLabel={modelGroupLabel}
							efforts={efforts}
							defaultEffort={(selected ?? fallback)?.defaultEffort}
							availability={(selected ?? fallback)?.efforts === undefined ? "unknown" : efforts.length ? "supported" : "unsupported"}
							reroute={reroute}
							rerouted={rerouted}
							chosenLabel={chosenLabel}
							executionMode={inlineExecutionMode}
							planReturn={planReturn}
							toggles={grouped.toggles}
							extraOptions={grouped.extra}
							onChangeConfigOption={onChangeConfigOption ? applyOption : undefined}
						/>
					) : null}

					{!unified && onChangeConfigOption && clubbedLeft && !nativeModelMenu ? (
						<ClubbedConfigPicker
							groupClaude={harness === "claude-code"}
							modelOptions={grouped.model}
							effortOptions={grouped.effort}
							executionMode={inlineExecutionMode}
							planReturn={planReturn}
							toggles={grouped.toggles}
							extraOptions={grouped.extra}
							disabled={optionDisabled}
							onChange={applyOption}
						/>
					) : null}

					{standaloneExecutionMode && onChangeConfigOption ? (
						<ExecutionModePicker
							option={standaloneExecutionMode}
							planReturn={planReturn}
							disabled={optionDisabled}
							onChange={applyOption}
						/>
					) : null}
				</div>

				{showRightDropdown || children || (unified && planOn) ? (
					<div className="flex h-7 shrink-0 items-center gap-1">
						{children}
						{showPermissions && pending && agentSwitch ? (
							<Picker
								label={pendingCopy[pendingMode].label}
								title={startsInNote ?? `Approval policy for ${pendingAgent}`}
								disabled={optionDisabled}
								leadIcon={permissionIcon(permissionLook(pending.harness, pendingMode))}
								showCaret
								alert={Boolean(startsInNote)}
							>
								{startsInNote ? (
									<p role="note" className="flex items-start gap-2 px-3 py-2 text-[11px] leading-snug text-destructive">
										<TriangleAlert aria-hidden="true" className="mt-0.5 size-3.5 shrink-0" />
										<span>{startsInNote}</span>
									</p>
								) : null}
								{pendingOrder.map((mode) => (
									<PermissionRow
										key={mode}
										mode={permissionLook(pending.harness, mode)}
										title={pendingCopy[mode].label}
										description={pendingRuns(mode) ? pendingCopy[mode].description : t("agentSwitch.modeUnsupported", { agent: pendingAgent })}
										active={mode === pendingMode}
										disabled={!pendingRuns(mode)}
										onSelect={() => agentSwitch.onPendingChange({ ...pending, approvalMode: mode })}
									/>
								))}
							</Picker>
						) : showPermissions && shownModeOption && onChangeConfigOption ? (
							<ConfigOptionPicker
								option={shownModeOption}
								disabled={optionDisabled}
								onChange={(value) => applyOption(shownModeOption.id, value)}
								footer={rememberAction}
								leadIcon={unified ? permissionIcon(shownModeOption.choices.find((choice) => choice.value === shownModeOption.currentValue)?.permissionMode) : undefined}
								showCaret
							/>
						) : showApprovalMode && onChange ? (
							<Picker
								label={approvalLabel}
													title="Approval policy for the next turn"
													disabled={optionDisabled}
								leadIcon={unified ? permissionIcon(permissionLook(harness, settings.approvalMode ?? "default")) : undefined}
								showCaret
							>
								{approvalOrder.filter((mode) => !approvalModes || approvalModes.includes(mode)).map((mode) => (
									unified ? (
										<PermissionRow
											key={mode}
											mode={permissionLook(harness, mode)}
											title={approvalCopy[mode].label}
											description={approvalCopy[mode].description}
											active={mode === (settings.approvalMode ?? "default")}
											onSelect={() => onChange({ ...settings, approvalMode: mode })}
										/>
									) : (
									<OptionMenuItem
										key={mode}
										active={mode === (settings.approvalMode ?? "default")}
										radio
										onSelect={() => onChange({ ...settings, approvalMode: mode }, "approvalMode")}
										className={cn("text-xs")}
									>
										<span
											className={cn(
														"text-xs",
												mode === (settings.approvalMode ?? "default")
													? "text-foreground"
													: "text-muted-foreground",
											)}
										>
											{approvalCopy[mode].label}
										</span>
									</OptionMenuItem>
									)
								))}
								{rememberAction}
							</Picker>
						) : null}
						{unified && planOn ? (
							<button
								type="button"
								disabled={optionDisabled}
								onClick={leavePlan}
								aria-label="Plan mode is on. Turn it off"
								title="Plan mode is on. Click to turn it off"
								className={cn(TRIGGER_CLASS, "inline-flex items-center px-2 text-foreground")}
							>
								<ListTodo aria-hidden="true" className="size-3.5 shrink-0" />
								<span>Plan</span>
							</button>
						) : null}
					</div>
				) : null}
			</div>
			{rememberPermissionsError ? (
				<p role="alert" className="px-1 text-xs text-destructive">{rememberPermissionsError}</p>
			) : null}
			{error ? (
				<p role="alert" className="px-1 text-xs leading-snug text-destructive">
					{error}
				</p>
			) : null}
		</div>
	);
}
