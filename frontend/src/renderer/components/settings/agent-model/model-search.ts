/** Model search index, ranking and grouping for the agent model combobox. */
import { modelChoiceLabel } from "../../../lib/agent-model-choices";
import { cn } from "../../../lib/utils";
import { AgentModel } from "./agent-model-props";

export type IndexedModel = {
	model: AgentModel;
	id: string;
	label: string;
	provider: string;
	normalizedID: string;
	normalizedLabel: string;
	normalizedProvider: string;
	index: number;
};

export type ModelSearchIndex = {
	models: IndexedModel[];
	byID: Map<string, IndexedModel>;
	providerBuckets: Map<string, { models: IndexedModel[]; indexes: Set<number> }>;
	trigramPostings: Map<string, Set<number>>;
};

export type ModelSearchResult = {
	models: IndexedModel[];
	candidateCount: number;
	strategy: "direct" | "provider-index" | "text-index" | "fuzzy-fallback";
};


export function normalizeSearch(value: string): string {
	return value.trim().toLocaleLowerCase();
}

export function shouldShowModelID(item: IndexedModel, siblings: IndexedModel[], search: string): boolean {
	if (item.id === item.label) return false;

	const label = normalizeSearch(item.label);
	const duplicateLabel = siblings.some(
		(candidate) => candidate.id !== item.id && normalizeSearch(candidate.label) === label,
	);
	if (duplicateLabel) return true;

	return search !== "" && normalizeSearch(item.id).includes(search) && !label.includes(search);
}

export function providerFromModelID(modelID: string): string {
	const slash = modelID.indexOf("/");
	return slash > 0 ? modelID.slice(0, slash) : "";
}

export function buildModelSearchIndex(models: AgentModel[]): ModelSearchIndex {
	const indexedModels = models.map((model, index) => {
		const label = modelChoiceLabel(model);
		const provider = model.provider?.trim() || providerFromModelID(model.id) || "Other";
		return {
			model,
			id: model.id,
			label,
			provider,
			normalizedID: normalizeSearch(model.id),
			normalizedLabel: normalizeSearch(label),
			normalizedProvider: normalizeSearch(provider),
			index,
		};
	});
	const byID = new Map(indexedModels.map((item) => [item.normalizedID, item]));
	const providerBuckets = new Map<string, { models: IndexedModel[]; indexes: Set<number> }>();
	const trigramPostings = new Map<string, Set<number>>();

	for (const item of indexedModels) {
		const providerKeys = new Set([
			item.normalizedProvider,
			normalizeSearch(providerFromModelID(item.id)),
		]);
		for (const providerKey of providerKeys) {
			if (!providerKey) continue;
			const bucket = providerBuckets.get(providerKey) ?? { models: [], indexes: new Set<number>() };
			bucket.models.push(item);
			bucket.indexes.add(item.index);
			providerBuckets.set(providerKey, bucket);
		}

		const itemTrigrams = new Set([
			...trigrams(item.normalizedID),
			...trigrams(item.normalizedLabel),
			...trigrams(item.normalizedProvider),
		]);
		for (const trigram of itemTrigrams) {
			const posting = trigramPostings.get(trigram) ?? new Set<number>();
			posting.add(item.index);
			trigramPostings.set(trigram, posting);
		}
	}

	return { models: indexedModels, byID, providerBuckets, trigramPostings };
}

export function searchModelIndex(index: ModelSearchIndex, query: string): ModelSearchResult {
	const normalizedQuery = normalizeSearch(query);
	const directMatch = index.byID.get(normalizedQuery);
	if (directMatch) {
		return { models: [directMatch], candidateCount: 1, strategy: "direct" };
	}

	const provider = providerQualifier(normalizedQuery);
	const providerBucket = provider ? index.providerBuckets.get(provider) : undefined;
	const universe = providerBucket?.models ?? index.models;
	const indexedCandidates = trigramCandidates(index, normalizedQuery, providerBucket?.indexes);
	if (indexedCandidates.length > 0) {
		return {
			models: rankMatches(indexedCandidates, normalizedQuery),
			candidateCount: indexedCandidates.length,
			strategy: providerBucket ? "provider-index" : "text-index",
		};
	}

	return {
		models: rankMatches(universe, normalizedQuery),
		candidateCount: universe.length,
		strategy: "fuzzy-fallback",
	};
}

export function rankInitialModels(models: IndexedModel[], selectedID: string, recentIDs: string[]): IndexedModel[] {
	const byID = new Map(models.map((item) => [normalizeSearch(item.id), item]));
	const result: IndexedModel[] = [];
	const added = new Set<string>();
	const append = (item: IndexedModel | undefined) => {
		if (!item || added.has(item.id)) return;
		added.add(item.id);
		result.push(item);
	};

	append(byID.get(normalizeSearch(selectedID)));
	for (const item of models) {
		if (item.model.isDefault) append(item);
	}
	for (const recentID of recentIDs) {
		append(byID.get(normalizeSearch(recentID)));
	}
	for (const item of models) append(item);
	return result;
}

export function providerQualifier(query: string): string {
	const slash = query.indexOf("/");
	return slash > 0 ? query.slice(0, slash) : "";
}

export function trigrams(value: string): string[] {
	if (value.length < 3) return [];
	const result: string[] = [];
	for (let index = 0; index <= value.length - 3; index += 1) {
		result.push(value.slice(index, index + 3));
	}
	return result;
}

export function trigramCandidates(
	index: ModelSearchIndex,
	query: string,
	providerIndexes: Set<number> | undefined,
): IndexedModel[] {
	const queryTrigrams = [...new Set(trigrams(query))];
	if (queryTrigrams.length === 0) return [];
	const postings = queryTrigrams.map((trigram) => index.trigramPostings.get(trigram));
	if (postings.some((posting) => !posting)) return [];
	const completePostings = postings as Set<number>[];
	const smallestPosting = completePostings.reduce((smallest, posting) =>
		posting.size < smallest.size ? posting : smallest,
	);
	const matches: IndexedModel[] = [];
	for (const modelIndex of smallestPosting) {
		if (providerIndexes && !providerIndexes.has(modelIndex)) continue;
		if (completePostings.every((posting) => posting.has(modelIndex))) {
			matches.push(index.models[modelIndex]);
		}
	}
	return matches;
}

export function rankMatches(models: IndexedModel[], query: string): IndexedModel[] {
	return models
		.map((item) => ({ item, score: modelMatchScore(item, query) }))
		.filter((match): match is { item: IndexedModel; score: number } => match.score !== null)
		.sort((a, b) => a.score - b.score || a.item.index - b.item.index)
		.map((match) => match.item);
}

export function modelMatchScore(item: IndexedModel, query: string): number | null {
	const id = item.normalizedID;
	const label = item.normalizedLabel;
	const provider = item.normalizedProvider;
	if (id === query) return 0;
	if (id.startsWith(query)) return 10;
	if (label.startsWith(query)) return 20;
	if (provider.startsWith(query)) return 30;
	if (id.includes(query)) return 40;
	if (label.includes(query)) return 50;
	if (provider.includes(query)) return 60;
	const fuzzyScores = [id, label, provider]
		.map((candidate) => fuzzySubsequenceScore(candidate, query))
		.filter((score): score is number => score !== null);
	return fuzzyScores.length === 0 ? null : 100 + Math.min(...fuzzyScores);
}

export function fuzzySubsequenceScore(haystack: string, needle: string): number | null {
	let searchAt = 0;
	let score = 0;
	for (const character of needle) {
		const foundAt = haystack.indexOf(character, searchAt);
		if (foundAt === -1) return null;
		score += foundAt - searchAt;
		searchAt = foundAt + 1;
	}
	return score;
}

export type ModelGroup = {
	key: string;
	label: string;
	kind: "pinned" | "recent" | "provider";
	models: IndexedModel[];
};

export function groupModels(
	models: IndexedModel[],
	showPinned: boolean,
	selectedID: string,
	recentIDs: string[],
	labels: { pinned: string; recent: string },
) {
	const groups = new Map<string, ModelGroup>();
	const recentSet = new Set(recentIDs);
	for (const item of models) {
		const pinned = showPinned && (item.id === selectedID || item.model.isDefault);
		const recent = showPinned && !pinned && recentSet.has(item.id);
		const kind: ModelGroup["kind"] = pinned ? "pinned" : recent ? "recent" : "provider";
		const key = kind === "provider" ? `provider:${item.provider}` : kind;
		const group = groups.get(key) ?? {
			key,
			label: kind === "pinned" ? labels.pinned : kind === "recent" ? labels.recent : item.provider,
			kind,
			models: [],
		};
		group.models.push(item);
		groups.set(key, group);
	}
	return [...groups.values()];
}

export function modelItemClass(selected: boolean): string {
	return cn(
		"settings-menu-item min-w-0 cursor-default outline-none",
		"focus:bg-settings-menu-selected focus:text-settings-title",
		"data-highlighted:bg-settings-menu-selected data-highlighted:text-settings-title",
		selected && "border-settings-menu bg-settings-menu-selected text-settings-title",
	);
}
