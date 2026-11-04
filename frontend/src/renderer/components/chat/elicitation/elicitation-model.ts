/** Types and pure helpers for elicitation forms. */

export type InputAction = "accept" | "decline" | "cancel";
export type InputValue = string | number | boolean | string[];
export type PropertyEntry = [string, Record<string, unknown>];

export function questionPrompt(question: PropertyEntry, message?: string): string {
	const description = question[1].description;
	return (typeof description === "string" && description.trim() ? description : undefined) ?? message ?? propertyLabel(question);
}

export function propertyLabel([name, property]: PropertyEntry): string {
	return typeof property.title === "string" && property.title ? property.title : humanize(name);
}

export function enumOptions(property: Record<string, unknown>): Array<{ value: string; label: string; description?: string }> {
	const source = Array.isArray(property.oneOf)
		? property.oneOf
		: property.type === "array" && isRecord(property.items) && Array.isArray(property.items.anyOf)
			? property.items.anyOf
			: Array.isArray(property.enum)
				? property.enum.map((value) => ({ const: value, title: String(value) }))
				: [];
	return source.flatMap((entry) => {
		if (!isRecord(entry) || typeof entry.const !== "string") return [];
		return [{
			value: entry.const,
			label: typeof entry.title === "string" ? entry.title : entry.const,
			description: typeof entry.description === "string" ? entry.description : undefined,
		}];
	});
}

export function claudeQuestionGroups(properties: PropertyEntry[]): PropertyEntry[][] | undefined {
	if (properties.length === 0) return undefined;

	const groups = new Map<number, { question?: PropertyEntry; custom?: PropertyEntry }>();
	for (const entry of properties) {
		const match = /^question_(\d+)(_custom)?$/.exec(entry[0]);
		if (!match) return undefined;
		const index = Number(match[1]);
		const group = groups.get(index) ?? {};
		if (match[2]) {
			group.custom = entry;
		} else {
			group.question = entry;
		}
		groups.set(index, group);
	}

	const ordered: PropertyEntry[][] = [];
	for (const [, group] of [...groups.entries()].sort(([left], [right]) => left - right)) {
		if (!group.question) return undefined;
		ordered.push(group.custom ? [group.question, group.custom] : [group.question]);
	}
	return ordered;
}

/**
 * Marks the free-text "Other" field an AskUserQuestion bridge (claude-agent-acp)
 * adds after each question. The daemon recognises the same marker
 * (`askUserQuestionCustomAnswerMetaKey` in chatdriver/acp).
 */
export const CUSTOM_ANSWER_META_KEY = "_askUserQuestionCustomAnswer";

/** Each marked question mapped to the field that answers it in free text. */
export function otherAnswerFields(properties: PropertyEntry[]): Map<string, string> {
	const names = new Set(properties.map(([name]) => name));
	const fields = new Map<string, string>();
	for (const [name, property] of properties) {
		const marker = isRecord(property._meta) ? property._meta[CUSTOM_ANSWER_META_KEY] : undefined;
		if (!isRecord(marker) || marker.isCustomAnswer !== true || typeof marker.questionId !== "string") continue;
		if (marker.questionId !== name && names.has(marker.questionId)) fields.set(marker.questionId, name);
	}
	return fields;
}

export function initialValues(properties: PropertyEntry[]): Record<string, InputValue> {
	const values: Record<string, InputValue> = {};
	for (const [name, property] of properties) {
		if (
			typeof property.default === "string" ||
			typeof property.default === "number" ||
			typeof property.default === "boolean"
		) {
			values[name] = property.default;
		} else if (property.type === "array") {
			values[name] = [];
		}
	}
	return values;
}

export function safeExternalURL(raw: string): URL | undefined {
	try {
		const url = new URL(raw);
		return url.protocol === "https:" || url.protocol === "http:" ? url : undefined;
	} catch {
		return undefined;
	}
}

export function toggleValue(values: string[], value: string): string[] {
	return values.includes(value) ? values.filter((item) => item !== value) : [...values, value];
}

export function isEmpty(value: InputValue | undefined): boolean {
	return value === undefined || value === "" || (Array.isArray(value) && value.length === 0);
}

/** Empty, or text that is only whitespace, which the adapter ignores as an Other answer. */
export function isBlank(value: InputValue | undefined): boolean {
	return typeof value === "string" ? value.trim() === "" : isEmpty(value);
}

export function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null;
}

export function humanize(value: string): string {
	return value.replace(/_/g, " ").replace(/^./, (letter) => letter.toUpperCase());
}

