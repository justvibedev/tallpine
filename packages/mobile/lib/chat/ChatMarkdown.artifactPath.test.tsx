import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

const { platform, theme } = vi.hoisted(() => ({
	platform: { OS: "ios" },
	theme: { accent: "#f4f5f7", accentBorder: "rgba(244,245,247,0.28)", highlight: "#79b0dc", selection: "rgba(121,176,220,0.28)" },
}));

vi.mock("react-native", () => ({
	NativeModules: {},
	findNodeHandle: (view: unknown) => view,
	Image: "Image",
	Platform: platform,
	Pressable: "Pressable",
	ScrollView: "ScrollView",
	StyleSheet: { create: (styles: unknown) => styles, hairlineWidth: 1 },
	Text: "Text",
	View: "View",
}));
vi.mock("../icons", () => ({ Feather: "Feather" }));
vi.mock("@bsky.app/react-native-uitextview", () => ({ UITextView: "UITextView" }));
vi.mock("expo-clipboard", () => ({ setStringAsync: vi.fn() }));
vi.mock("../haptics", () => ({ haptics: { tap: vi.fn(), select: vi.fn(), success: vi.fn() } }));
vi.mock("../openGitHub", () => ({ openGitHub: vi.fn() }));
vi.mock("../ThemeProvider", () => ({
	useTheme: () => theme,
	useThemedStyles: (factory: (theme: Record<string, string>) => unknown) => factory(theme),
}));

import { ChatLinkProvider, ChatMarkdown } from "./ChatMarkdown";

const path = "/Users/me/.tallpine/data/artifacts/sess-1/reports/out.md";

function render(body: string, open: ((entry: string) => void) | undefined, sessionId = "sess-1", continuousSelection = false) {
	let tree!: ReactTestRenderer;
	act(() => {
		tree = create(<ChatLinkProvider onLinkOpen={vi.fn()} onArtifactOpen={open} artifactSessionId={sessionId}><ChatMarkdown text={body} continuousSelection={continuousSelection} /></ChatLinkProvider>);
	});
	return tree;
}
const links = (tree: ReactTestRenderer) => tree.root.findAll((n) => n.props.accessibilityRole === "link" && typeof n.props.onPress === "function");

describe("artifact paths in replies", () => {
	it.each([false, true])("opens a bare absolute artifact path by its entry (continuousSelection=%s)", (continuous) => {
		const open = vi.fn();
		const tree = render(`Wrote ${path}.`, open, "sess-1", continuous);
		const [link] = links(tree);
		act(() => link.props.onPress());
		expect(open).toHaveBeenCalledWith("reports/out.md");
	});
	it.each([false, true])("opens a path in inline code (continuousSelection=%s)", (continuous) => {
		const open = vi.fn();
		const tree = render(`See \`${path}\``, open, "sess-1", continuous);
		act(() => links(tree)[0].props.onPress());
		expect(open).toHaveBeenCalledWith("reports/out.md");
	});
	it("leaves another session's path as plain text", () => {
		const tree = render(`Wrote ${path}`, vi.fn(), "other");
		expect(links(tree)).toHaveLength(0);
	});
	it("leaves paths plain without an artifact opener", () => {
		expect(links(render(`Wrote ${path}`, undefined))).toHaveLength(0);
	});
	it("does not link tilde, command, relative or other-session code spans", () => {
		for (const code of ["~/.tallpine/data/artifacts/sess-1/a.md", `cat ${path} | head`, "foo/artifacts/sess-1/a.md", "/x/artifacts/other/a.md"]) {
			expect(links(render(`See \`${code}\``, vi.fn()))).toHaveLength(0);
		}
	});
	it("does not link a relative path fragment in prose", () => {
		expect(links(render("Edit packages/mobile/artifacts/sess-1/y.ts now", vi.fn()))).toHaveLength(0);
	});
	it("renders another session's path without link styling", () => {
		const tree = render(`Wrote ${path}`, vi.fn(), "other");
		expect(tree.root.findAll((n) => typeof n.type === "string" && JSON.stringify(n.props.style ?? "").includes("underline"))).toHaveLength(0);
	});
	it("keeps an unopenable path in the prose group with the native text view", () => {
		const text = `Wrote ${path}`;
		const tree = render(text, vi.fn(), "other");
		expect(tree.root.findAll((n) => n.props.uiTextView === false)).toHaveLength(0);
		const linked = render(text, vi.fn(), "sess-1");
		expect(linked.root.findAll((n) => n.props.uiTextView === false).length).toBeGreaterThan(0);
	});
	it("does not link scheme-less URLs or take trailing brackets", () => {
		for (const text of ["//cdn.example.com/artifacts/sess-1/a.png", "s3://bucket/artifacts/sess-1/a.md", "ftp://host/artifacts/sess-1/x"]) {
			expect(links(render(`Got ${text}`, vi.fn()))).toHaveLength(0);
		}
		const open = vi.fn();
		const bracketed = render("[/x/artifacts/sess-1/a.md]", open);
		act(() => links(bracketed)[0].props.onPress());
		expect(open).toHaveBeenCalledWith("a.md");
	});
});
