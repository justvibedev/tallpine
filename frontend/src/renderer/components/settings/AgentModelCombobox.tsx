import { Check, ChevronDown, Loader2, RefreshCw, Search } from "lucide-react";
import { cn } from "../../lib/utils";
import { ToggleRow } from "../chat/AgentSwitchPicker";
import { formatEffortLabel } from "./EffortPicker";
import { OptionMenuItem, OptionMenuSub, OptionMenuSubContent, OptionMenuSubTrigger } from "../ui/option-menu";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuLabel,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "../ui/dropdown-menu";
import { Spinner } from "../ui/spinner";

import { useAgentModelCombobox } from "./agent-model/useAgentModelCombobox";
import type { AgentModelComboboxProps } from "./agent-model/agent-model-props";
import { modelItemClass, shouldShowModelID } from "./agent-model/model-search";
import { MAX_VISIBLE_MODELS } from "./agent-model/model-constants";

export function AgentModelCombobox(props: AgentModelComboboxProps) {
	const {
		agentLabel,
		onRefresh,
		refreshError,
		retryAt,
		onCustom,
		triggerClassName,
		menuAlign = "end",
		menuClassName,
		renderTrigger,
		compact = false,
		showEffortInTrigger = true,
		tuning,
		disabled = false,
		"aria-label": ariaLabel,
	} = props;
	const combo = useAgentModelCombobox(props);
	const {
		allowDirectCustom,
		awaitingEffort,
		canScrollDown,
		claude,
		currentEffortLabel,
		currentLabel,
		customSearchValue,
		defaultEffort,
		effectiveEffort,
		effortMenuOpen,
		effortOptions,
		effortTriggerRef,
		entryMode,
		groups,
		hasMultipleProviders,
		invalidEffort,
		menuOpen,
		normalizedSearch,
		onCloseAutoFocus,
		otherOpen,
		rankedModels,
		refreshBusy,
		refreshFailed,
		runRefresh,
		scrollRef,
		search,
		searchInputRef,
		selectCatalogModel,
		selectModel,
		selectedIsOther,
		setAwaitingEffort,
		setEffortMenuOpen,
		setMenuOpen,
		setOtherOpen,
		setRefreshFailed,
		setSearch,
		showCustomSearchAction,
		showEffort,
		showManualRefresh,
		showSearch,
		shownModel,
		t,
		updateScrollCue,
		variant,
		visibleModels,
	} = combo;

	return (
		<DropdownMenu
			open={menuOpen}
			onOpenChange={(open) => {
				setMenuOpen(open);
				if (open) {
					setSearch("");
					setOtherOpen(false);
				} else {
					setRefreshFailed(false);
					setEffortMenuOpen(false);
					setAwaitingEffort(false);
				}
			}}
		>
			<DropdownMenuTrigger asChild disabled={disabled}>
				<button
					type="button"
					className={cn(
						"group/agent-model-trigger settings-option-trigger max-w-full min-w-0 hover:text-settings-label focus:outline-none focus-visible:outline-none focus-visible:ring-0 data-[state=open]:outline-none data-[state=open]:ring-0",
						disabled && "cursor-not-allowed opacity-50",
						triggerClassName,
					)}
					aria-label={ariaLabel}
					disabled={disabled}
				>
					{renderTrigger ? (
						renderTrigger(currentLabel)
					) : (
						<span className="min-w-0 truncate">{currentLabel}</span>
					)}
					{showEffort && showEffortInTrigger && <span className="shrink-0 text-settings-muted"> · {currentEffortLabel}</span>}
					{variant?.variant.effort && tuning && showEffortInTrigger && <span className="shrink-0 text-settings-muted"> · {formatEffortLabel(variant.variant.effort, t)}</span>}
					<ChevronDown
						className="size-icon-sm shrink-0 opacity-70 transition-transform duration-300 ease-out group-data-[state=open]/agent-model-trigger:rotate-180"
						aria-hidden="true"
					/>
				</button>
			</DropdownMenuTrigger>
			<DropdownMenuContent
				align={menuAlign}
				onCloseAutoFocus={onCloseAutoFocus}
				onKeyDownCapture={(event) => {
					if (!showSearch || !menuOpen || event.target === searchInputRef.current) return;
					if (event.target instanceof HTMLElement && (event.target.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(event.target.tagName))) return;

					if (event.key === "Backspace") {
						event.preventDefault();
						event.stopPropagation();
						searchInputRef.current?.focus();
						setSearch((current) => Array.from(current).slice(0, -1).join(""));
					} else if (event.key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey) {
						event.preventDefault();
						event.stopPropagation();
						searchInputRef.current?.focus();
						setSearch((current) => current + event.key);
					}
				}}
				className={cn("settings-menu-surface max-h-select-menu-max! w-[min(22rem,calc(100vw-2rem))] overflow-hidden! rounded-(--radius-settings-panel) border-settings-menu bg-settings-menu", menuClassName)}
			>
				{(showSearch || showManualRefresh) && (
					<div className="flex shrink-0 items-center gap-1">
						{showSearch && (
							<div className="relative min-w-0 flex-1">
								<Search
									className="pointer-events-none absolute left-3.5 top-1/2 size-icon-sm -translate-y-1/2 text-settings-muted"
									aria-hidden="true"
								/>
								<input
									ref={searchInputRef}
									type="search"
									aria-label={t("settings.models.searchAria", { label: ariaLabel.toLocaleLowerCase() })}
									value={search}
									onChange={(event) => setSearch(event.target.value)}
									onKeyDown={(event) => {
										if (event.key === "ArrowDown") {
											event.preventDefault();
											event.stopPropagation();
											scrollRef.current?.querySelector<HTMLElement>("[role='menuitem']")?.focus();
										} else if (event.key === "Enter") {
											event.preventDefault();
											event.stopPropagation();
											scrollRef.current?.querySelector<HTMLElement>("[role='menuitem']")?.click();
										} else if (event.key !== "Escape") {
											event.stopPropagation();
										}
									}}
									placeholder={t(
										hasMultipleProviders
											? "settings.models.searchModelsOrProvidersPlaceholder"
											: "settings.models.searchPlaceholder",
									)}
									className="menu-search-input pl-8!"
								/>
							</div>
						)}
						{showManualRefresh && (
							<DropdownMenuItem
								className="flex size-8 shrink-0 items-center justify-center rounded-md text-settings-muted hover:bg-settings-menu-selected hover:text-settings-label disabled:cursor-not-allowed disabled:opacity-50"
								aria-label={refreshBusy ? t("settings.models.refreshing") : t("settings.models.refresh")}
								disabled={refreshBusy}
								onSelect={(event) => {
									event.preventDefault();
									runRefresh();
								}}
							>
								{refreshBusy ? (
									<Spinner icon={Loader2} className="size-icon-sm" />
								) : (
									<RefreshCw className="size-icon-sm" aria-hidden="true" />
								)}
							</DropdownMenuItem>
						)}
					</div>
				)}
				{(refreshError || refreshFailed) && (
					<div className="flex items-center gap-2 px-2 pb-1 text-xs text-settings-muted" aria-live="polite">
						<DropdownMenuItem
							className="truncate text-warning underline underline-offset-2"
							title={refreshError}
							onSelect={(event) => {
								event.preventDefault();
								runRefresh();
							}}
							disabled={refreshBusy}
						>
							{t("settings.models.retry")}
							{retryAt
								? ` · ${new Date(retryAt).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })}`
								: ""}
						</DropdownMenuItem>
					</div>
				)}

				<div className="relative grid min-h-0 flex-1 grid-rows-[minmax(0,1fr)] overflow-hidden">
					<div
						ref={scrollRef}
						className="model-menu-scroll min-h-0 overflow-y-auto overscroll-contain"
						onScroll={updateScrollCue}
					>
						{groups.map((group, groupIndex) => (
							<div key={group.key}>
								{!compact && (groupIndex > 0 || normalizedSearch === "") && <DropdownMenuSeparator />}
								{!compact && <DropdownMenuLabel className="normal-case tracking-normal">{group.label}</DropdownMenuLabel>}
								{group.models.map((item) =>
									compact ? (
										<DropdownMenuItem
											key={item.id}
											onSelect={(event) => selectCatalogModel(event, item)}
											className={modelItemClass(item.id === shownModel)}
											aria-current={tuning && item.id === shownModel ? true : undefined}
										>
											<span className="truncate text-settings-label">{item.label}</span>
											{tuning && item.id === shownModel && <Check className="ml-auto size-icon-sm shrink-0" aria-hidden="true" />}
										</DropdownMenuItem>
									) : (
										<DropdownMenuItem
											key={item.id}
											onSelect={(event) => selectCatalogModel(event, item)}
											className={modelItemClass(item.id === shownModel)}
										>
										<div className="flex min-w-0 flex-1 items-center gap-3">
											<div className="min-w-0 flex-1">
												<span className="truncate text-settings-label">{item.label}</span>
													{shouldShowModelID(item, visibleModels, normalizedSearch) && (
														<p className="truncate text-xs text-settings-muted">{item.id}</p>
													)}
												</div>
												{group.kind !== "provider" && item.provider !== "Other" && (
													<span className="shrink-0 text-xs text-settings-muted">{item.provider}</span>
												)}
											</div>
										</DropdownMenuItem>
									),
								)}
							</div>
						))}

						{claude?.other.length && normalizedSearch === "" && !selectedIsOther ? (
							<DropdownMenuItem
								onSelect={(event) => {
									event.preventDefault();
									setOtherOpen((open) => !open);
								}}
								className={cn(modelItemClass(false), "[&_svg]:size-icon-sm")}
								aria-expanded={otherOpen}
							>
								<span className="truncate text-settings-muted">{t("settings.models.otherModels")}</span>
								<ChevronDown className={cn("ml-auto opacity-70 transition-transform", otherOpen && "rotate-180")} aria-hidden="true" />
							</DropdownMenuItem>
						) : null}

						{showCustomSearchAction && (
							<DropdownMenuItem onSelect={() => onCustom?.(customSearchValue)} className={modelItemClass(false)}>
								{t("settings.models.useCustom", { model: customSearchValue })}
							</DropdownMenuItem>
						)}
						{normalizedSearch !== "" && rankedModels.length === 0 && !allowDirectCustom && (
							<p className="px-2 py-1.5 text-xs text-settings-muted">{t("settings.models.noMatches")}</p>
						)}
						{normalizedSearch === "" && entryMode !== "direct" && !(compact && entryMode === "none") && (
							<>
								<DropdownMenuSeparator />
								<div className="space-y-1 px-2 py-1.5 text-xs text-settings-muted">
									<p className="text-settings-label">{t("settings.models.cantFind")}</p>
									<p>
										{entryMode === "configured"
											? t("settings.models.configureThenRefresh", {
													agent: agentLabel || t("settings.models.selectedAgent"),
												})
											: t("settings.models.unavailable")}
									</p>
									{onRefresh && !refreshError && !showManualRefresh && (
										<DropdownMenuItem
											className="text-settings-label underline underline-offset-2 disabled:cursor-not-allowed disabled:opacity-50"
											onSelect={(event) => {
												event.preventDefault();
												runRefresh();
											}}
											disabled={refreshBusy}
										>
											{refreshBusy ? t("settings.models.refreshing") : t("settings.models.refresh")}
										</DropdownMenuItem>
									)}
								</div>
							</>
						)}
						{showSearch && !compact && (
							<p className="px-2 py-1.5 text-xs text-settings-muted" aria-live="polite">
								{t("settings.models.matchingCount", {
									visible: visibleModels.length.toLocaleString(),
									total: rankedModels.length.toLocaleString(),
								})}
								{normalizedSearch === "" && rankedModels.length > MAX_VISIBLE_MODELS
									? t("settings.models.typeToNarrow")
									: ""}
							</p>
						)}
					</div>
					<div
						className={cn("model-menu-overflow-cue", canScrollDown ? "opacity-100" : "opacity-0")}
						aria-hidden="true"
					/>
				</div>
				{/* Cursor's effort and thinking open beside the model like other agents' effort (no fast mode here, as for them). */}
				{variant && tuning && (variant.family.efforts.length > 1 || variant.family.hasThinkingChoice) && (
					<div className="shrink-0">
						<DropdownMenuSeparator />
						<OptionMenuSub open={effortMenuOpen} onOpenChange={(open) => {
							if (open || !awaitingEffort) setEffortMenuOpen(open);
						}}>
							<OptionMenuSubTrigger label={t("settings.models.reasoningEffort", { defaultValue: "Reasoning effort" })} value={variant.variant.effort ? formatEffortLabel(variant.variant.effort, t) : undefined} />
							<OptionMenuSubContent>
								{variant.family.efforts.map((effort) => (
									<OptionMenuItem key={effort} role="menuitemradio" aria-checked={effort === variant.variant.effort} active={effort === variant.variant.effort}
										onSelect={() => { selectModel(variant.variant.id, { effort }); setAwaitingEffort(false); setEffortMenuOpen(false); setMenuOpen(false); }} className="gap-3 text-xs">
										{formatEffortLabel(effort, t)}
										{effort === variant.variant.effort && <Check className="ml-auto size-icon-sm shrink-0" aria-hidden="true" />}
									</OptionMenuItem>
								))}
								{variant.family.hasThinkingChoice && variant.family.efforts.length > 1 && <DropdownMenuSeparator />}
								<ToggleRow label={t("agentSwitch.thinking")} checked={variant.family.hasThinkingChoice ? variant.variant.thinking : undefined} onChange={(thinking) => selectModel(variant.variant.id, { thinking })} />
							</OptionMenuSubContent>
						</OptionMenuSub>
					</div>
				)}
				{showEffort && tuning && (
					<div className="shrink-0">
						<DropdownMenuSeparator />
						<OptionMenuSub open={effortMenuOpen} onOpenChange={(open) => {
							if (open || !awaitingEffort) setEffortMenuOpen(open);
						}}>
							<OptionMenuSubTrigger ref={effortTriggerRef} label={t("settings.models.reasoningEffort", { defaultValue: "Reasoning effort" })} value={currentEffortLabel} />
							<OptionMenuSubContent onEscapeKeyDown={(event) => {
								event.preventDefault();
								event.stopPropagation();
								setAwaitingEffort(false);
								setEffortMenuOpen(false);
								effortTriggerRef.current?.focus();
							}}>
								{effortOptions.map((effort) => (
									<OptionMenuItem key={effort} role="menuitemradio" aria-checked={effort === effectiveEffort}
										active={effort === effectiveEffort} onSelect={() => {
											tuning.onEffortChange(effort === defaultEffort ? "" : effort);
											setEffortMenuOpen(false);
											setAwaitingEffort(false);
											setMenuOpen(false);
										}} className="gap-3 text-xs">
										{formatEffortLabel(effort, t)}
										{effort === effectiveEffort && <Check className="ml-auto size-icon-sm shrink-0" aria-hidden="true" />}
									</OptionMenuItem>
								))}
							</OptionMenuSubContent>
						</OptionMenuSub>
					</div>
				)}
			</DropdownMenuContent>
			{tuning && invalidEffort && <p role="alert" className="px-1 text-xs leading-row text-warning">
				{t("settings.models.unsupportedTuning", { role: tuning.roleLabel ? `${tuning.roleLabel} ` : "" })}
			</p>}
		</DropdownMenu>
	);
}

