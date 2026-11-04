import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { THEMES } from "./theme-palettes";
import { buildThemeStylesheet, deriveThemeTokens } from "./theme-tokens";

const tokensCss = readFileSync(resolve(__dirname, "../../styles/tokens.css"), "utf8");

describe("theme tokens", () => {
	it("only derives tokens that tokens.css defines", () => {
		const unknown = Object.keys(deriveThemeTokens(THEMES[1].dark, "dark")).filter(
			(name) => !tokensCss.includes(`${name}:`),
		);
		expect(unknown).toEqual([]);
	});

	it("emits a dark and a light block for every named theme, never for Orchestrate", () => {
		const css = buildThemeStylesheet();
		for (const theme of THEMES.slice(1)) {
			expect(css).toContain(`:root[data-style-theme="${theme.id}"] {`);
			expect(css).toContain(`:root[data-style-theme="${theme.id}"][data-theme="light"] {`);
		}
		expect(css).not.toContain('"orchestrate"');
	});

	it("resolves every derived value to a concrete hex color for xterm", () => {
		// One assertion over collected offenders, not one expect per token: the
		// per-token form made ~2000 matcher calls, so a starved worker (the full
		// suite under load) spent its whole timeout budget here and the failure
		// carried no useful message. This also names the bad token on a mismatch.
		const hex = /^#[0-9a-f]{6}([0-9a-f]{2})?$/;
		const offenders: string[] = [];
		for (const theme of THEMES) {
			for (const mode of ["dark", "light"] as const) {
				for (const [name, value] of Object.entries(deriveThemeTokens(theme[mode], mode))) {
					if (!hex.test(value)) offenders.push(`${theme.id}/${mode} ${name}: ${value}`);
				}
			}
		}
		expect(offenders).toEqual([]);
	});
});
