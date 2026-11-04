import { readdirSync, readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { appCatalogs } from "../../i18n/all-messages";
import { APP_LOCALES } from "../../i18n/locales";

const here = path.resolve(process.cwd(), "src/renderer/components/backlog");
const sidebarDir = path.resolve(process.cwd(), "src/renderer/components/sidebar");
const sources = [
	...readdirSync(here).filter((name) => name.endsWith(".tsx") && !name.endsWith(".test.tsx")).map((name) => path.join(here, name)),
	path.resolve(process.cwd(), "src/renderer/components/Sidebar.tsx"),
	...readdirSync(sidebarDir).filter((name) => name.endsWith(".tsx") && !name.endsWith(".test.tsx")).map((name) => path.join(sidebarDir, name)),
	path.resolve(process.cwd(), "src/renderer/components/ShellTopbar.tsx"),
];
const text = sources.map((file) => readFileSync(file, "utf8")).join("\n");

// Keys built from a value at runtime; every prefix used in code must be listed here with its values.
const DYNAMIC: Record<string, readonly string[]> = {
	"backlog.state.": ["inbox", "open", "done", "dropped"],
	"backlog.filter.": ["all", "inbox", "open", "held", "archive"],
	"backlog.scope.": ["local", "pr", "pr-merge"],
	"backlog.origin.": ["human", "orchestrator", "worker", "reviewer"],
	"backlog.link.kind.": ["session", "pr", "issue", "artifact", "memory", "item", "blocked_by"],
	"backlog.pr.state.": ["open", "draft", "merged", "closed"],
	"backlog.toast.": ["done", "dropped", "accepted", "reopened"],
	"backlog.empty.": ["none", "filtered", "archive"],
	"backlog.run.status.": ["planning", "running", "paused", "stopping", "done"],
	"backlog.run.item.": ["queued", "active", "in_review", "needs_you", "landed", "held", "failed"],
	"backlog.run.planStatus.": ["drafting", "awaiting_answers", "writing", "awaiting_approval", "approved", "failed"],
};
const PLURAL = new Set(["backlog.stale"]);
// Keys c2 adds outside the backlog components: the shortcut label and the two palette rows.
const EXTRA_KEYS = ["shortcut.backlog-capture", "command.addToBacklog", "command.addQueryToBacklog"];
const EXTRA_PLURAL: Record<string, readonly string[]> = { es: ["many"], fr: ["many"], "pt-BR": ["many"] };

const literalKeys = [...text.matchAll(/\bt\("(backlog\.[A-Za-z.]+)"/g)].map((match) => match[1]);
const dynamicPrefixes = [...text.matchAll(/\bt\(`(backlog\.[^$`]*)\$\{/g)].map((match) => match[1]);

function expectedKeys(locale?: string): string[] {
	const keys = new Set<string>();
	for (const key of literalKeys) {
		if (PLURAL.has(key)) {
			for (const category of ["one", "other", ...(locale ? EXTRA_PLURAL[locale] ?? [] : [])]) keys.add(`${key}_${category}`);
		} else keys.add(key);
	}
	for (const key of EXTRA_KEYS) keys.add(key);
	for (const prefix of dynamicPrefixes) for (const value of DYNAMIC[prefix] ?? []) keys.add(`${prefix}${value}`);
	return [...keys].sort();
}

describe("backlog i18n", () => {
	it("finds the keys the code uses", () => {
		expect(literalKeys.length).toBeGreaterThan(50);
		expect(dynamicPrefixes.length).toBeGreaterThan(5);
	});

	it("lists every runtime-built key prefix", () => {
		for (const prefix of new Set(dynamicPrefixes)) expect(DYNAMIC, `add ${prefix} to DYNAMIC`).toHaveProperty([prefix]);
	});

	it.each(APP_LOCALES)("has every backlog key, non-empty, in %s", (locale) => {
		const catalog = appCatalogs[locale] as unknown as Record<string, string>;
		const missing = expectedKeys(locale).filter((key) => !catalog[key]);
		expect(missing).toEqual([]);
	});

	it("keeps the placeholders of each message in every locale", () => {
		const placeholders = (value: string) => [...value.matchAll(/\{\{(\w+)\}\}/g)].map((match) => match[1]).sort();
		const english = appCatalogs.en as unknown as Record<string, string>;
		for (const locale of APP_LOCALES) {
			const catalog = appCatalogs[locale] as unknown as Record<string, string>;
			for (const key of expectedKeys()) expect(placeholders(catalog[key]), `${locale} ${key}`).toEqual(placeholders(english[key]));
		}
	});
});
