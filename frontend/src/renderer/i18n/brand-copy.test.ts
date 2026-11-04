import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { createAppI18n } from "./instance";
import { APP_LOCALES } from "./locales";
import { PRODUCT_NAME } from "../../shared/brand";

const LEGACY_NAME = /\bAO\b|Agent Orchestrator/;
// Importing from the official Agent Orchestrator install names that product on purpose.
const INTENTIONAL_UPSTREAM_KEY = /(^|\.)importAo\./;
const QUIT_CLOSE_PREFIXES = ["quitConfirm.", "tray."];

// Lowercase `ao` naming the CLI or the state folder (any language). Portuguese "ao" is an ordinary
// word ("ao lado"), so only a command/folder context matches: a command word next to it, or a leading dot.
const LEGACY_CLI_OR_FOLDER = /(?:\b(?:command|commande|comando|Befehl|命令|명령|コマンド)\s+ao\b|\bao\s+(?:command|commande|comando|Befehl|命令|명령|コマンド)|\bao(?=\s*(?:コマンド|명령|命令))|(?<![\w/])\.ao\b)/;

function catalog(locale: string): Record<string, string | string[]> {
	return JSON.parse(readFileSync(join(__dirname, `${locale}.json`), "utf8"));
}

describe("product naming in locale copy", () => {
	for (const locale of APP_LOCALES) {
		it(`${locale}: no value names the CLI or state folder as ao`, () => {
			const offenders = Object.entries(catalog(locale))
				.filter(([k, v]) => !INTENTIONAL_UPSTREAM_KEY.test(k) && [v].flat().some((s) => LEGACY_CLI_OR_FOLDER.test(s)))
				.map(([k]) => k);
			expect(offenders).toEqual([]);
		});
	}

	for (const locale of APP_LOCALES) {
		it(`${locale}: no catalog string carries the legacy AO / Agent Orchestrator name`, () => {
			const offenders = Object.entries(catalog(locale))
				.filter(([k, v]) => !INTENTIONAL_UPSTREAM_KEY.test(k) && [v].flat().some((s) => LEGACY_NAME.test(s)))
				.map(([k]) => k);
			expect(offenders).toEqual([]);
		});

		it(`${locale}: quit and tray copy renders the brand constant`, () => {
			const raw = catalog(locale);
			const i18n = createAppI18n(locale, { [locale]: raw } as never);
			for (const key of Object.keys(raw).filter((k) => QUIT_CLOSE_PREFIXES.some((p) => k.startsWith(p)))) {
				expect((i18n.t as (k: string, o: object) => string)(key, { count: 2 })).not.toMatch(LEGACY_NAME);
			}
			expect(i18n.t("quitConfirm.title")).toContain(PRODUCT_NAME);
		});
	}
});

// Files whose "AO" is deliberate: wire/protocol text matched against backend or agent
// output, agent-facing prompts and errors, the official install's bundle paths, a demo
// fixture.
const INTENTIONAL_SOURCE_FILES = [
	"main/stale-app-copies.ts",
	"main/agent-browser-",
	"main/browser-view-host/",
	"main/browser-view-host-test-helpers.ts",
	"components/TerminalPane.tsx",
	"components/chat/AutomationMessage.tsx",
	"components/chat/ComposerSuggestMenu.tsx",
	"components/chat/WorkerUpdates.tsx",
	"components/chat/ChatComposer.tsx",
	"components/chat/IndependentSideChats.tsx",
	"components/chat/SessionChatSurface.tsx",
	"components/SessionInspector/reviewHelpers.tsx",
	"lib/session-reviews.ts",
	"lib/chat-fixture.ts",
	"shared/browser-annotations.ts",
];

function sourceFiles(dir: string): string[] {
	return readdirSync(dir).flatMap((name) => {
		const full = join(dir, name);
		if (statSync(full).isDirectory()) return name === "docs" ? [] : sourceFiles(full);
		return /\.tsx?$/.test(name) && !/\.(test|integration|generated)\./.test(name) && !name.includes("generated") ? [full] : [];
	});
}

describe("product naming in user-visible source strings", () => {
	it("has no hardcoded AO / Agent Orchestrator outside comments, logs and the intentional files", () => {
		const root = join(__dirname, "..", "..");
		const offenders: string[] = [];
		for (const file of sourceFiles(root)) {
			const rel = file.slice(root.length + 1);
			if (INTENTIONAL_SOURCE_FILES.some((f) => rel.includes(f))) continue;
			readFileSync(file, "utf8").split("\n").forEach((line, i) => {
				if (!LEGACY_NAME.test(line) || /^\s*(\/\/|\*|\/\*)/.test(line)) return;
				if (/console\.|\blog\b|X-AO-|importAo/.test(line)) return;
				offenders.push(`${rel}:${i + 1} ${line.trim().slice(0, 100)}`);
			});
		}
		expect(offenders).toEqual([]);
	});
});
