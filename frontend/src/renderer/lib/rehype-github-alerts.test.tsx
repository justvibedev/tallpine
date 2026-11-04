import Markdown from "react-markdown";
import { renderToStaticMarkup } from "react-dom/server";
import remarkGfm from "remark-gfm";
import { describe, expect, it } from "vitest";
import { rehypeGithubAlerts } from "./rehype-github-alerts";

function html(markdown: string): string {
	return renderToStaticMarkup(
		<Markdown remarkPlugins={[remarkGfm]} rehypePlugins={[rehypeGithubAlerts]}>
			{markdown}
		</Markdown>,
	);
}

describe("rehypeGithubAlerts", () => {
	it.each([
		["NOTE", "note", "Note", "info"],
		["TIP", "tip", "Tip", "light-bulb"],
		["IMPORTANT", "important", "Important", "report"],
		["WARNING", "warning", "Warning", "alert"],
		["CAUTION", "caution", "Caution", "stop"],
	])("renders [!%s] with GitHub's alert markup and octicon", (keyword, kind, title, icon) => {
		const output = html(`> [!${keyword}]\n> Read this.`);
		expect(output).toContain(`<div class="markdown-alert markdown-alert-${kind}">`);
		expect(output).toContain(
			`<p class="markdown-alert-title"><svg class="octicon octicon-${icon} mr-2" viewBox="0 0 16 16" version="1.1" width="16" height="16" aria-hidden="true"><path d="`,
		);
		expect(output).toContain(`</svg>${title}</p>`);
		expect(output).toContain("<p>Read this.</p>");
		expect(output).not.toContain("[!");
	});

	it("matches the keyword case-insensitively and keeps later paragraphs", () => {
		const output = html("> [!tip]\n> one\n>\n> two");
		expect(output).toContain('class="markdown-alert markdown-alert-tip"');
		expect(output).toMatch(/<p>one<\/p>\s*<p>two<\/p>\s*<\/div>/);
	});

	it("turns nested alert quotes into alerts", () => {
		expect(html("> outer\n> > [!WARNING]\n> > inner")).toMatch(
			/<blockquote>[\s\S]*<div class="markdown-alert markdown-alert-warning">[\s\S]*<p>inner<\/p>/,
		);
	});

	it.each([
		["a plain quote", "> plain quote"],
		["an unknown type", "> [!UNKNOWN]\n> body"],
		["text after the marker on its line", "> [!NOTE] trailing\n> body"],
		["a marker with no body", "> [!CAUTION]"],
	])("leaves %s as a blockquote", (_case, markdown) => {
		const output = html(markdown);
		expect(output).toContain("<blockquote>");
		expect(output).not.toContain("markdown-alert");
	});
});
