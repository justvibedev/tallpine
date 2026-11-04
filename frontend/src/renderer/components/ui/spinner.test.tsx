import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, resolve } from "node:path";
import { render } from "@testing-library/react";
import { Loader2 } from "lucide-react";
import { describe, expect, it } from "vitest";
import { Spinner } from "./spinner";

function sources(dir: string): string[] {
	return readdirSync(dir).flatMap((name) => {
		const path = join(dir, name);
		if (statSync(path).isDirectory()) return name === "node_modules" ? [] : sources(path);
		return /\.tsx$/.test(name) && !/\.test\./.test(name) ? [path] : [];
	});
}

describe("Spinner (B331)", () => {
	it("animates the wrapper span, never the svg", () => {
		const { container } = render(<Spinner icon={Loader2} className="size-3" data-testid="s" />);
		const span = container.querySelector("span");
		expect(span?.className).toContain("animate-spin");
		expect(span?.querySelector("svg")?.getAttribute("class") ?? "").not.toContain("animate-spin");
	});

	it("a labelled Spinner is an accessible image; an unlabelled one is hidden", () => {
		const { getByRole, container, rerender } = render(<Spinner icon={Loader2} aria-label="Generating" />);
		expect(getByRole("img", { name: "Generating" })).toBeTruthy();
		rerender(<Spinner icon={Loader2} />);
		expect(container.querySelector("span")?.getAttribute("aria-hidden")).toBe("true");
	});

	it("animate-spin only ever sits on a plain html element, never on an icon component (svg root animations run on the main thread)", () => {
		const roots = [resolve(__dirname, "../.."), resolve(__dirname, "../../../../../packages/product-ui/src")];
		const offenders: string[] = [];
		for (const file of roots.flatMap(sources)) {
			const text = readFileSync(file, "utf8");
			for (const m of text.matchAll(/animate-spin/g)) {
				if (text.slice(m.index - 7, m.index) === "before:") continue; // CSS border spinner, not an svg
				const tag = text.slice(text.lastIndexOf("<", m.index) + 1).match(/^[A-Za-z][\w.]*/)?.[0] ?? "";
				if (!/^(span|div|i)$/.test(tag) && !file.endsWith("ui/spinner.tsx")) offenders.push(`${file}:${text.slice(0, m.index).split("\n").length} <${tag}>`);
			}
		}
		expect(offenders).toEqual([]);
	});

	it("the Working shimmer animates only opacity/transform (compositor), never paint properties", () => {
		const css = readFileSync(resolve(__dirname, "../../styles.css"), "utf8");
		const keyframes = [...css.matchAll(/@keyframes (chat-working-[\w-]+) \{([\s\S]*?)\n\}/g)];
		expect(keyframes.length).toBeGreaterThan(0);
		for (const [, name, body] of keyframes) {
			expect(body, name).not.toMatch(/background|color|mask|--[\w-]+:/);
		}
		// Settle fade must not animate opacity (it would restart from a fixed value and blink).
		const fade = keyframes.find(([, name]) => name === "chat-working-fade")?.[2] ?? "";
		expect(fade).toMatch(/filter:\s*opacity/);
		expect(fade).not.toMatch(/(^|[\s{;])opacity:/);
		const settle = css.match(/\.chat-working-shimmer\[data-settling\]::after \{([\s\S]*?)\}/)?.[1] ?? "";
		expect(settle).toContain("chat-working-breathe");
	});
});
