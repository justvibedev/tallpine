import type { Element, ElementContent, Root, RootContent } from "hast";

/**
 * GitHub alert blockquotes (`> [!NOTE]` and friends), rendered with GitHub's
 * own markup so github-markdown-css styles them.
 *
 * Behaves like rehype-github-alerts 4.2.0 (MIT) with its default options. That
 * package imports all of @primer/octicons (1.1 MB of JSON) and an HTML parser
 * to draw five 16px icons, which put both in the session view's bundle; the
 * five paths are inlined here instead.
 */
const ALERTS = [
	{
		keyword: "NOTE",
		title: "Note",
		icon: "info",
		path: "M0 8a8 8 0 1 1 16 0A8 8 0 0 1 0 8Zm8-6.5a6.5 6.5 0 1 0 0 13 6.5 6.5 0 0 0 0-13ZM6.5 7.75A.75.75 0 0 1 7.25 7h1a.75.75 0 0 1 .75.75v2.75h.25a.75.75 0 0 1 0 1.5h-2a.75.75 0 0 1 0-1.5h.25v-2h-.25a.75.75 0 0 1-.75-.75ZM8 6a1 1 0 1 1 0-2 1 1 0 0 1 0 2Z",
	},
	{
		keyword: "IMPORTANT",
		title: "Important",
		icon: "report",
		path: "M0 1.75C0 .784.784 0 1.75 0h12.5C15.216 0 16 .784 16 1.75v9.5A1.75 1.75 0 0 1 14.25 13H8.06l-2.573 2.573A1.458 1.458 0 0 1 3 14.543V13H1.75A1.75 1.75 0 0 1 0 11.25Zm1.75-.25a.25.25 0 0 0-.25.25v9.5c0 .138.112.25.25.25h2a.75.75 0 0 1 .75.75v2.19l2.72-2.72a.749.749 0 0 1 .53-.22h6.5a.25.25 0 0 0 .25-.25v-9.5a.25.25 0 0 0-.25-.25Zm7 2.25v2.5a.75.75 0 0 1-1.5 0v-2.5a.75.75 0 0 1 1.5 0ZM9 9a1 1 0 1 1-2 0 1 1 0 0 1 2 0Z",
	},
	{
		keyword: "WARNING",
		title: "Warning",
		icon: "alert",
		path: "M6.457 1.047c.659-1.234 2.427-1.234 3.086 0l6.082 11.378A1.75 1.75 0 0 1 14.082 15H1.918a1.75 1.75 0 0 1-1.543-2.575Zm1.763.707a.25.25 0 0 0-.44 0L1.698 13.132a.25.25 0 0 0 .22.368h12.164a.25.25 0 0 0 .22-.368Zm.53 3.996v2.5a.75.75 0 0 1-1.5 0v-2.5a.75.75 0 0 1 1.5 0ZM9 11a1 1 0 1 1-2 0 1 1 0 0 1 2 0Z",
	},
	{
		keyword: "TIP",
		title: "Tip",
		icon: "light-bulb",
		path: "M8 1.5c-2.363 0-4 1.69-4 3.75 0 .984.424 1.625.984 2.304l.214.253c.223.264.47.556.673.848.284.411.537.896.621 1.49a.75.75 0 0 1-1.484.211c-.04-.282-.163-.547-.37-.847a8.456 8.456 0 0 0-.542-.68c-.084-.1-.173-.205-.268-.32C3.201 7.75 2.5 6.766 2.5 5.25 2.5 2.31 4.863 0 8 0s5.5 2.31 5.5 5.25c0 1.516-.701 2.5-1.328 3.259-.095.115-.184.22-.268.319-.207.245-.383.453-.541.681-.208.3-.33.565-.37.847a.751.751 0 0 1-1.485-.212c.084-.593.337-1.078.621-1.489.203-.292.45-.584.673-.848.075-.088.147-.173.213-.253.561-.679.985-1.32.985-2.304 0-2.06-1.637-3.75-4-3.75ZM5.75 12h4.5a.75.75 0 0 1 0 1.5h-4.5a.75.75 0 0 1 0-1.5ZM6 15.25a.75.75 0 0 1 .75-.75h2.5a.75.75 0 0 1 0 1.5h-2.5a.75.75 0 0 1-.75-.75Z",
	},
	{
		keyword: "CAUTION",
		title: "Caution",
		icon: "stop",
		path: "M4.47.22A.749.749 0 0 1 5 0h6c.199 0 .389.079.53.22l4.25 4.25c.141.14.22.331.22.53v6a.749.749 0 0 1-.22.53l-4.25 4.25A.749.749 0 0 1 11 16H5a.749.749 0 0 1-.53-.22L.22 11.53A.749.749 0 0 1 0 11V5c0-.199.079-.389.22-.53Zm.84 1.28L1.5 5.31v5.38l3.81 3.81h5.38l3.81-3.81V5.31L10.69 1.5ZM8 4a.75.75 0 0 1 .75.75v3.5a.75.75 0 0 1-1.5 0v-3.5A.75.75 0 0 1 8 4Zm0 8a1 1 0 1 1 0-2 1 1 0 0 1 0 2Z",
	},
] as const;

type Alert = (typeof ALERTS)[number];

function alertIcon(alert: Alert): Element {
	return {
		type: "element",
		tagName: "svg",
		properties: {
			className: ["octicon", `octicon-${alert.icon}`, "mr-2"],
			viewBox: "0 0 16 16",
			version: "1.1",
			width: 16,
			height: 16,
			ariaHidden: "true",
		},
		children: [{ type: "element", tagName: "path", properties: { d: alert.path }, children: [] }],
	};
}

function isElement(node: RootContent | undefined): node is Element {
	return node?.type === "element";
}

// The blockquote as an alert, or null when it is an ordinary quote. Mirrors
// the package's rules: the first paragraph must open with `[!TYPE]` alone on
// its line, and a marker with no body stays a quote.
function toAlert(node: Element): Element | null {
	const firstParagraph = node.children.find(
		(child): child is Element => child.type === "element" && child.tagName === "p",
	);
	const header = firstParagraph?.children[0];
	if (!firstParagraph || header?.type !== "text") return null;
	const match = /\[!(.*?)\]/.exec(header.value);
	if (!match) return null;
	const rest = header.value.length > match[0].length ? header.value.replace(match[0], "") : "";
	if (rest.trim() !== "" && !rest.startsWith("\n") && !rest.startsWith("\r")) return null;
	const alert = ALERTS.find(({ keyword }) => keyword === match[1].toUpperCase());
	if (!alert) return null;

	const remaining = firstParagraph.children.slice(1);
	const body = rest.replace(/^(\r\n|\r|\n)/, "");
	if (body === "" && remaining.length === 0 && node.children.length < 4) return null;

	const paragraph: ElementContent[] = [];
	if (isElement(remaining[0]) && remaining[0].tagName === "br") {
		paragraph.push(...remaining.slice(2));
	} else {
		if (body !== "") paragraph.push({ type: "text", value: body });
		paragraph.push(...remaining);
	}
	const children: ElementContent[] = [
		{
			type: "element",
			tagName: "p",
			properties: { className: ["markdown-alert-title"] },
			children: [alertIcon(alert), { type: "text", value: alert.title }],
		},
	];
	if (paragraph.length > 0) children.push({ type: "element", tagName: "p", properties: {}, children: paragraph });
	children.push(...node.children.slice(2));
	return {
		type: "element",
		tagName: "div",
		properties: { className: ["markdown-alert", `markdown-alert-${alert.keyword.toLowerCase()}`] },
		children,
	};
}

function transform(parent: Root | Element): void {
	parent.children.forEach((child, index) => {
		if (child.type !== "element") return;
		const replacement = child.tagName === "blockquote" ? toAlert(child) : null;
		if (replacement) parent.children[index] = replacement;
		transform(replacement ?? child);
	});
}

export function rehypeGithubAlerts() {
	return (tree: Root) => transform(tree);
}
