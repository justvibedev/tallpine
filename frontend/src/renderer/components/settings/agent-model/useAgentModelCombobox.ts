import { useCallback, useLayoutEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useSuppressStrayFocusRing } from "../../../hooks/useSuppressStrayFocusRing";
import { isConcreteModelID, splitClaudeModels } from "../../../lib/agent-model-choices";
import { fallbackEffort, useApplyEffortDefault } from "../../../lib/effort";
import { collapseModelVariants, findModelVariant, listsModelVariants, pickModelVariant, type ModelVariant } from "../../../lib/model-variants";
import { formatEffortLabel } from "../EffortPicker";
import { useModelTuning } from "../ModelTuningControls";
import type { AgentModelComboboxProps } from "./agent-model-props";
import { ignoreEffortChange, MODEL_SEARCH_THRESHOLD, MAX_VISIBLE_MODELS } from "./model-constants";
import { readRecentModels, rememberRecentModel } from "./recent-models";
import { normalizeSearch, buildModelSearchIndex, rankInitialModels, searchModelIndex, groupModels, IndexedModel } from "./model-search";

/** Model, effort, recent and search state for the agent model combobox. */
export function useAgentModelCombobox(props: AgentModelComboboxProps) {
	const {
		value,
		models,
		allowCustom,
		customModelEntry,
		onRefresh,
		refreshing = false,
		onChange,
		emptyLabel,
		triggerLabel,
		recentScope,
		compact = false,
		agentId,
		tuning,
		disabled = false,
	} = props;
	const { t } = useTranslation();
	const concreteModels = useMemo(
		() => models.filter((model) => isConcreteModelID(model.id)),
		[models],
	);
	const explicitModel = isConcreteModelID(value) ? value : "";
	// Cursor lists one id per model setting: each model shows once, but the variant's id is saved.
	const families = useMemo(
		() => (listsModelVariants(agentId) ? collapseModelVariants(concreteModels.map((model) => ({ id: model.id, label: model.label }))) : undefined),
		[agentId, concreteModels],
	);
	const listedModels = useMemo(() => families
		? families.map((family) => ({ ...concreteModels.find((model) => model.id === family.defaultVariant.id)!, id: family.id, label: family.label, provider: family.group }))
		: concreteModels, [concreteModels, families]);
	const { selected: effortModel, invalidEffort } = useModelTuning({
		models: concreteModels,
		model: explicitModel,
		effort: tuning?.effort ?? "",
		effortsWithoutModel: tuning?.effortsWithoutModel,
		onEffortChange: tuning?.onEffortChange ?? ignoreEffortChange,
		onEffortReset: tuning?.onEffortReset,
		onValidityChange: tuning?.onValidityChange,
	});
	const effortOptions = effortModel?.efforts?.filter((effort) => effort && effort.toLowerCase() !== "default") ?? [];
	const explicitEffort = tuning?.effort?.toLowerCase() === "default" ? "" : tuning?.effort;
	const showEffort = Boolean(tuning && (effortOptions.length || explicitEffort));
	const providerEffort = effortModel?.defaultEffort;
	const defaultEffort = providerEffort && effortOptions.includes(providerEffort) ? providerEffort : "";
	// No provider default for these levels: Tallpine picks one and saves it when the
	// model is chosen, so the control never reads as unset.
	const tallpineDefaultEffort = fallbackEffort(effortOptions, providerEffort);
	const effectiveEffort = explicitEffort || defaultEffort || tallpineDefaultEffort || "";
	useApplyEffortDefault(
		explicitModel,
		tuning && !explicitEffort ? tallpineDefaultEffort : undefined,
		tuning?.onEffortChange ?? ignoreEffortChange,
		{ disabled: disabled || !tuning },
	);
	const currentEffortLabel = effectiveEffort ? formatEffortLabel(effectiveEffort, t) : t("settings.models.effortNotReported");
	const entryMode = customModelEntry ?? (allowCustom ? "direct" : "none");
	const allowDirectCustom = entryMode === "direct";
	const [search, setSearch] = useState("");
	const [menuOpen, setMenuOpen] = useState(false);
	const [effortMenuOpen, setEffortMenuOpen] = useState(false);
	const [awaitingEffort, setAwaitingEffort] = useState(false);
	const [refreshFailed, setRefreshFailed] = useState(false);
	const [refreshingLocal, setRefreshingLocal] = useState(false);
	const [sessionRecentModels, setSessionRecentModels] = useState<Record<string, string[]>>({});
	const recentKey = recentScope ?? "";
	const storedRecentModels = useMemo(() => readRecentModels(recentScope), [recentScope]);
	const recentModelIDs = recentScope ? (sessionRecentModels[recentKey] ?? storedRecentModels) : [];
	const normalizedSearch = normalizeSearch(search);
	const searchIndex = useMemo(() => buildModelSearchIndex(listedModels), [listedModels]);
	const defaultModel = concreteModels.find((model) => model.isDefault)?.id || "";
	const effectiveModel = explicitModel || defaultModel;
	const variant = families ? findModelVariant(families, effectiveModel) : undefined;
	// The row a Cursor variant id shows as is its model's.
	const shownModel = variant?.family.id ?? effectiveModel;
	const selected = searchIndex.byID.get(normalizeSearch(shownModel));
	const hasMultipleProviders = useMemo(
		() =>
			new Set(
				concreteModels
					.map((model) => model.provider?.trim().toLocaleLowerCase())
					.filter((provider): provider is string => Boolean(provider)),
			).size > 1,
		[concreteModels],
	);

	const claude = useMemo(
		() => (agentId === "claude-code" ? splitClaudeModels(searchIndex.models) : undefined),
		[agentId, searchIndex],
	);
	const [otherOpen, setOtherOpen] = useState(false);
	const selectedIsOther = Boolean(claude?.other.some((item) => item.id === selected?.id));
	const showOther = otherOpen || selectedIsOther;
	// Count what the menu lists up front: Claude's folded "Other models" don't
	// count until expanded, so four visible models never get a search box.
	const listedModelCount = claude && !showOther ? claude.current.length : concreteModels.length;
	const showSearch = allowDirectCustom || listedModelCount >= MODEL_SEARCH_THRESHOLD;

	const rankedModels = useMemo(() => {
		if (!normalizedSearch) {
			const base = claude ? (showOther ? [...claude.current, ...claude.other] : claude.current) : searchIndex.models;
			// Compact mode reads as a plain, stable list — picking a model
			// shouldn't reorder it to the top on the next open.
			return compact ? base : rankInitialModels(base, shownModel, recentModelIDs);
		}
		return searchModelIndex(searchIndex, normalizedSearch).models;
	}, [claude, compact, shownModel, normalizedSearch, recentModelIDs, searchIndex, showOther]);

	const visibleModels = rankedModels.slice(0, MAX_VISIBLE_MODELS);
	const groups = useMemo(
		() =>
			compact
				? [{ key: "all", label: "", kind: "provider" as const, models: visibleModels }]
				: groupModels(visibleModels, normalizedSearch === "", shownModel, recentModelIDs, {
						pinned: t("settings.models.currentDefaults"),
						recent: t("settings.models.recent"),
					}),
		[compact, shownModel, normalizedSearch, recentModelIDs, t, visibleModels],
	);
	const customSearchValue = search.trim();
	const showCustomSearchAction = allowDirectCustom && customSearchValue !== "" && rankedModels.length === 0;
	// With no identified model, nothing is selected and the menu just lists models.
	const currentLabel = (triggerLabel ?? selected?.label ?? explicitModel) || emptyLabel || t("settings.models.selectModel");
	const scrollRef = useRef<HTMLDivElement>(null);
	const searchInputRef = useRef<HTMLInputElement>(null);
	const effortTriggerRef = useRef<HTMLDivElement>(null);
	const [canScrollDown, setCanScrollDown] = useState(false);
	const updateScrollCue = useCallback(() => {
		const element = scrollRef.current;
		setCanScrollDown(Boolean(element && element.scrollHeight - element.scrollTop > element.clientHeight + 1));
	}, []);
	useLayoutEffect(() => {
		if (!menuOpen) {
			setCanScrollDown(false);
			return;
		}
		updateScrollCue();
		const element = scrollRef.current;
		if (!element || typeof ResizeObserver === "undefined") return;
		const observer = new ResizeObserver(updateScrollCue);
		observer.observe(element);
		return () => observer.disconnect();
	}, [groups.length, menuOpen, normalizedSearch, showCustomSearchAction, updateScrollCue, visibleModels.length]);
	const onCloseAutoFocus = useSuppressStrayFocusRing(menuOpen);
	const selectModel = (picked: string, want?: Partial<ModelVariant>) => {
		// A Cursor model keeps the current effort, thinking and speed where it has them.
		const family = families?.find((entry) => entry.id === picked) ?? (want ? variant?.family : undefined);
		const modelID = family ? pickModelVariant(family, { ...(variant?.variant ?? family.defaultVariant), ...want }).id : picked;
		if (recentScope) {
			const next = rememberRecentModel(recentScope, modelID);
			setSessionRecentModels((current) => ({ ...current, [recentScope]: next }));
		}
		onChange(modelID === defaultModel ? "" : modelID);
	};
	const selectCatalogModel = (event: Event, item: IndexedModel) => {
		const family = families?.find((entry) => entry.id === item.id);
		const openEffort = family
			? Boolean(tuning) && (family.efforts.length > 1 || family.hasThinkingChoice)
			: Boolean(tuning && item.model.efforts?.some((effort) => effort && effort.toLowerCase() !== "default"));
		if (openEffort) event.preventDefault();
		selectModel(item.id);
		setEffortMenuOpen(openEffort);
		setAwaitingEffort(openEffort);
		if (!openEffort) setMenuOpen(false);
	};
	const refreshBusy = refreshing || refreshingLocal;
	const showManualRefresh = Boolean(
		onRefresh && (concreteModels.length === 0 || (normalizedSearch !== "" && rankedModels.length === 0)),
	);
	const runRefresh = () => {
		if (!onRefresh || refreshBusy) return;
		setRefreshFailed(false);
		setRefreshingLocal(true);
		void Promise.resolve(onRefresh()).catch(() => setRefreshFailed(true)).finally(() => setRefreshingLocal(false));
	};

	return {
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
		selected,
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
	};
}
