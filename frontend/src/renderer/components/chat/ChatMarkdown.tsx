/**
 * Markdown for agent prose.
 *
 * Agents write markdown — headings, lists, tables, fenced code — and rendering it
 * as preformatted text was showing the syntax instead of the structure. `## Plan`
 * appeared as literal hashes, a table as a wall of pipes, a code block bracketed by
 * visible backticks.
 *
 * Two deliberate choices about what this does NOT do:
 *
 *   - Raw HTML is escaped, not rendered. Agent output is only as trustworthy as the
 *     files it just read, so `rehype-raw` is deliberately absent; markdown-only is
 *     the whole sanitization story and there is no schema to get wrong, with one
 *     exception: ```mermaid fences render as diagrams through `MermaidBlock`,
 *     whose SVG passes through mermaid's strict mode plus DOMPurify with
 *     active-content tags forbidden. Syntax
 *     highlighting keeps that property — it renders a token tree as elements
 *     rather than injecting the highlighter's HTML string.
 *   - Nothing here re-parses or re-orders content. It renders exactly the text the
 *     daemon stored, so the rendered form and the record cannot disagree. That is
 *     also why "copy as markdown" copies the stored text and not a re-serialized
 *     version of the DOM.
 *
 * Streaming is fine unstyled-first: an unclosed fence renders as a plain code
 * block with partial content, and gains its colours when the closing fence
 * arrives. See `lib/code-highlight.ts` for why it is not tokenized before then.
 */

import {
	createContext,
	memo,
	useContext,
	useEffect,
	useMemo,
	useState,
	type ComponentProps,
	type MouseEvent as ReactMouseEvent,
	type ReactNode,
} from "react";
import Markdown, { defaultUrlTransform, type Components, type Options } from "react-markdown";
import remarkGfm from "remark-gfm";
import { WrapText } from "lucide-react";
import { cn } from "../../lib/utils";
import { isHostLocalWebLink } from "../../lib/loopback";
import { canonicalLanguage } from "../../lib/code-highlight";
import { fenceOf } from "../../lib/markdown-fence";
import { rehypeStreamFade } from "../../lib/rehype-stream-fade";
import { compactEmoji } from "./compact-emoji";
import { remarkLocalPaths } from "../../lib/remark-local-paths";
import { isLocalPathCandidate } from "../../../shared/local-path";
import { copyLinkText, isOpenLinkClick } from "../../lib/copy-link";
import { LocalPathMenu } from "../LocalPathMenu";
import { linkedLocalPath, openLocalPathOnClick, performLocalPath } from "../../lib/local-path-click";
import { useUiStore } from "../../stores/ui-store";
import { useTranslation } from "react-i18next";
import { hasInAppLinkScheme } from "../../../shared/session-link-parse";
import { artifactLinkForPath, findSessionLinks, isTallpineLink, parseArtifactLink, isSessionLink, remarkSessionLinks } from "../../lib/session-links";
import { useSessionLinkSource } from "../../lib/use-session-link-navigation";
import {
	isPotentialWorkspaceFileLink,
	isWebLink,
	openLinkInSystemBrowser,
	workspaceFilePath,
} from "../../lib/external-link-policy";
import { AppLink } from "../AppLink";
import { ArtifactLinkPreviewCard, SessionLinkPreviewCard } from "../SessionLinkPreviewCard";
import {
	explicitWorkspaceFilePath,
	findWorkspaceFilePath,
	workspaceFileOpenTarget,
	workspaceFileReferenceLine,
} from "../../lib/workspace-file-path";
import { HighlightedCode } from "./HighlightedCode";
import { MermaidBlock } from "./MermaidBlock";
import { CopyButton } from "./CopyButton";
import { ChatImage, ChatImageGallery, ChatImageLinkScope, isImageOnlyParagraph } from "./ChatImage";
import "./code-theme.css";

// Activity titles live inside disclosure buttons: keep inline formatting, but
// unwrap block elements and links so provider text cannot nest interactive UI.
const TITLE_ELEMENTS = ["code", "em", "strong", "del"];
const TITLE_COMPONENTS: Components = {
	code: ({ children }) => <code className="font-mono text-[0.95em]">{children}</code>,
};

export const ActivityTitle = memo(function ActivityTitle({ text }: { text: string }) {
	return (
		<Markdown allowedElements={TITLE_ELEMENTS} unwrapDisallowed components={TITLE_COMPONENTS}>
			{text}
		</Markdown>
	);
});

/** GitHub-flavoured markdown: tables, strikethrough, task lists, autolinks. */
const PLUGINS = [remarkGfm, remarkSessionLinks, remarkLocalPaths];
// Reduced motion gets plain text, not a span per character that never animates.
const prefersReducedMotion = () => window.matchMedia?.("(prefers-reduced-motion: reduce)").matches ?? false;
// How long a settled reply keeps its fade spans, so the last characters (and the final
// flush of buffered text) finish fading in before they are dropped.
const FADE_SHED_MS = 450;
// Characters older than this behind the end of the reply have long finished their
// 300 ms fade (a stream does not outrun it by this much), so they render plain. Without
// a bound every update re-created, and re-rendered, one span per character of the
// reply so far, up to the block cap in `rehypeStreamFade`.
const STREAM_FADE_WINDOW = 600;

/**
 * Whether the prose is still arriving, for the fences inside it.
 *
 * A context rather than a prop because `COMPONENTS` has to stay module-level:
 * rebuilding that map per render would defeat react-markdown's own memoization
 * and re-parse every message on every poll.
 */
const StreamingProse = createContext(false);
const InsideMarkdownLink = createContext(false);
const SafeOriginContent = createContext(false);
const REMOTE_PREVIEW_UNAVAILABLE = "This link points to the remote host. Preview is unavailable on this device.";
const OpenChatLink = createContext<{
	open?: (url: string) => void;
	openFile?: (path: string, line?: number) => void;
	remoteHost?: boolean;
	sessionLinkHostId?: string;
	openSession?: (url: string) => void;
	workspacePaths: string[];
}>({ workspacePaths: [] });
const EMPTY_WORKSPACE_PATHS: string[] = [];

export function ChatLinkProvider({
	onLinkOpen,
	onFileOpen,
	remoteHost,
	sessionLinkHostId,
	onSessionLinkOpen,
	workspacePaths = EMPTY_WORKSPACE_PATHS,
	children,
}: {
	onLinkOpen?: (url: string) => void;
	onFileOpen?: (path: string, line?: number) => void;
	remoteHost?: boolean;
	sessionLinkHostId?: string;
	onSessionLinkOpen?: (url: string) => void;
	workspacePaths?: string[];
	children: ReactNode;
}) {
	const value = useMemo(
		() => ({ open: onLinkOpen, openFile: onFileOpen, openSession: onSessionLinkOpen, remoteHost, sessionLinkHostId, workspacePaths }),
		[onLinkOpen, onFileOpen, onSessionLinkOpen, remoteHost, sessionLinkHostId, workspacePaths],
	);
	return <OpenChatLink.Provider value={value}>{children}</OpenChatLink.Provider>;
}

function chatUrlTransform(url: string, key: string): string | undefined {
	// react-markdown correctly strips unknown schemes, but a Windows absolute
	// path resembles one (C:). Preserve only hrefs that look like local paths;
	// the click still goes through the workspace-confined preview endpoint.
	// Unknown tallpine:// forms survive as links so a click can say they are unsupported instead of doing nothing.
	if (isTallpineLink(url) || (key === "href" && hasInAppLinkScheme(url))) return url;
	if (key === "href" && isPotentialWorkspaceFileLink(url)) return url;
	return defaultUrlTransform(url);
}

/**
 * True while the surrounding chat is rendering its first commit. A stable ref,
 * not a value, so ending the opening never re-renders every reply.
 */
export const ChatOpeningRef = createContext<{ readonly current: boolean }>({ current: false });

export const ChatMarkdown = memo(function ChatMarkdown({
	text,
	streaming = false,
	muted = false,
	className,
	safeOrigin = false,
}: {
	text: string;
	streaming?: boolean;
	/**
	 * Prose that is not the answer. Reasoning is the case: it reads alongside the
	 * agent's reply and must not compete with it, so it steps back a tone rather than
	 * being dimmed with opacity — which would wash out code and links too.
	 */
	muted?: boolean;
	className?: string;
	safeOrigin?: boolean;
}) {
	// Replies opened from history mount settled and never wrap; live ones keep the fade a
	// beat past the end of the stream.
	const [fading, setFading] = useState(streaming);
	if (streaming && !fading) setFading(true);
	// A reply already on screen when its chat opens (switching to a chat
	// mid-stream) is not new; only characters that arrive afterwards fade in. A
	// reply that arrives later fades from its first character.
	const chatOpening = useContext(ChatOpeningRef);
	const [settledOffset] = useState(() => (chatOpening.current ? text.length : 0));
	const fadeFrom = Math.max(settledOffset, text.length - STREAM_FADE_WINDOW);
	const streamingRehype = useMemo<Options["rehypePlugins"]>(() => [[rehypeStreamFade, { settledOffset: fadeFrom }]], [fadeFrom]);
	useEffect(() => {
		if (streaming) return;
		const timer = setTimeout(() => setFading(false), FADE_SHED_MS);
		return () => clearTimeout(timer);
	}, [streaming]);

	return (
		<StreamingProse.Provider value={streaming}>
			<SafeOriginContent.Provider value={safeOrigin}>
			<div
				className={cn(
					"chat-md leading-[1.58]",
					muted ? "text-sm text-muted-foreground" : "text-sm text-foreground",
					className,
				)}
			>
				<Markdown
					remarkPlugins={PLUGINS}
					rehypePlugins={fading && !prefersReducedMotion() ? streamingRehype : undefined}
					components={COMPONENTS}
					urlTransform={chatUrlTransform}
				>
					{text}
				</Markdown>
			</div>
			</SafeOriginContent.Provider>
		</StreamingProse.Provider>
	);
});

/**
 * Wrapping is a reading preference, not a property of one block: a fence that
 * scrolled out of view and back must not silently lose it, and a reader who
 * turned wrapping on is usually about to want it again — so the last choice seeds
 * the next block. Deliberately not persisted; reflowing every block of a
 * conversation on launch because of a choice made days ago is worse than starting
 * from the readable default.
 */
let preferredWrap = false;

/**
 * A fenced code block: language on the left, quiet actions on the right, its own
 * scroll.
 *
 * The block scrolls rather than wrapping because a wrapped line of code is harder
 * to read than a scrolled one, and it must never widen the conversation column.
 * The toggle is there for the case that argument loses — a single 300-character
 * line, where scrolling means losing the rest of the block.
 */
function CodeBlock({ code, language }: { code: string; language?: string }) {
	const streaming = useContext(StreamingProse);
	const grammar = canonicalLanguage(language);
	const [wrap, setWrap] = useState(preferredWrap);

	return (
		<div
			className="chat-code group/code my-2.5 overflow-hidden rounded-lg border border-border bg-surface"
			data-wrap={wrap ? "true" : "false"}
		>
			<div className="flex items-center gap-2 border-b border-border bg-raised/40 px-2.5 py-1">
				<span className="text-caption text-muted-foreground">
					{language || "text"}
				</span>
				{/* Hover-revealed, and focus-revealed so the keyboard can reach it. The
				    timeline's whole point is that prose dominates; a permanent row of
				    controls on every fence is the opposite. */}
				<div className="ml-auto flex items-center gap-0.5 opacity-0 transition-opacity duration-150 focus-within:opacity-100 group-hover/code:opacity-100">
					<button
						type="button"
						onClick={() => {
							preferredWrap = !wrap;
							setWrap(!wrap);
						}}
						aria-pressed={wrap}
						aria-label="Wrap long lines"
						title="Wrap long lines"
						className={cn(
							"flex size-7 items-center justify-center rounded-md transition-[background-color,color,transform] hover:bg-interactive-hover hover:text-foreground",
							wrap ? "text-accent" : "text-muted-foreground",
						)}
					>
						<WrapText aria-hidden="true" className="size-3" />
					</button>
					<CopyButton text={code} label="Copy code" />
				</div>
			</div>
			<pre className="scrollbar-none overflow-x-auto px-3 py-2.5">
				<code className="font-mono text-xs leading-[1.6] text-foreground">
					<HighlightedCode code={code} language={grammar} streaming={streaming} />
				</code>
			</pre>
		</div>
	);
}

function MarkdownLink({ href, children, className, localPath }: { href?: string; children?: ReactNode; className?: string; localPath?: string }) {
	const { t } = useTranslation();
	const safeOriginContent = useContext(SafeOriginContent);
	const { open: onLinkOpen, openFile: onFileOpen, openSession: onSessionLinkOpen, remoteHost, sessionLinkHostId, workspacePaths } = useContext(OpenChatLink);
	const filePath = href && onFileOpen
		? workspaceFilePath(href, workspacePaths) ?? findWorkspaceFilePath(href, workspacePaths) ?? explicitWorkspaceFilePath(href)
		: undefined;
	const sessionLink = Boolean(href && isTallpineLink(href));
	// A local artifact link is a file on this machine: click reveals it, the menu offers Open / Copy path.
	const artifactPath = !remoteHost && href && parseArtifactLink(href) ? href : undefined;
	const unknownTallpineLink = Boolean(href && hasInAppLinkScheme(href) && !sessionLink);
	if (safeOriginContent && !sessionLink) return <>{children}</>;
	const openInFiles = filePath && href && !/\.html?$/i.test(filePath) ? workspaceFileOpenTarget(href, filePath) : undefined;
	// The absolute local path a plain click opens: an artifact path written as code, or a link straight to a local file.
	const clickPath = remoteHost || safeOriginContent || unknownTallpineLink ? undefined : localPath ?? linkedLocalPath(href);
	if (remoteHost && href && (isHostLocalWebLink(href) || (isPotentialWorkspaceFileLink(href) && !openInFiles))) {
		return <span className="text-muted-foreground" title={REMOTE_PREVIEW_UNAVAILABLE}>
			{children}<span className="sr-only"> (remote preview unavailable)</span>
		</span>;
	}
	const browserLink = href ? !sessionLink && (isWebLink(href) || (!remoteHost && (!!filePath || isPotentialWorkspaceFileLink(href)))) : false;
	return (
		<AppLink
			href={href}
			localPath={remoteHost || safeOriginContent ? undefined : localPath ?? artifactPath}
			onBrowserOpen={onLinkOpen}
			inAppLink={href ? () => browserLink : undefined}
			filePath={filePath && href ? workspaceFileOpenTarget(href, filePath) : filePath}
			onFileOpen={onFileOpen}
			hoverPreview={sessionLink
				? () => isSessionLink(href!)
					? <SessionLinkPreviewCard href={href!} sourceHostId={sessionLinkHostId} />
					: <ArtifactLinkPreviewCard href={href!} sourceHostId={sessionLinkHostId} />
				: undefined}
			onClick={(event) => {
				if (unknownTallpineLink) {
					event.preventDefault();
					useUiStore.getState().showGlobalToast(t("path.unsupportedLink"), undefined, "error");
					return;
				}
				if (clickPath && !isOpenLinkClick(event)) {
					// Plain click opens a local file in its default app (a directory or missing path is copied).
					event.preventDefault();
					void openLocalPathOnClick(clickPath);
					return;
				}
				if (href && !sessionLink && !isOpenLinkClick(event) && (artifactPath || openInFiles || browserLink)) {
					// Plain click copies a web link or relative file reference; Cmd/Ctrl+click opens it.
					event.preventDefault();
					copyLinkText(localPath ?? href);
					return;
				}
				if (artifactPath) {
					event.preventDefault();
					void performLocalPath(artifactPath, "reveal");
					return;
				}
				if (href && sessionLink) {
					event.preventDefault();
					onSessionLinkOpen?.(href);
					return;
				}
				if (openInFiles && onFileOpen) {
					event.preventDefault();
					const line = workspaceFileReferenceLine(href ?? "");
					if (line == null) onFileOpen(openInFiles);
					else onFileOpen(openInFiles, line);
					return;
				}
				if (href && !browserLink) {
					event.preventDefault();
					void openLinkInSystemBrowser(href);
				}
			}}
			target="_blank"
			rel="noreferrer noopener"
			className={className ?? "text-markdown-link underline decoration-markdown-link/45 underline-offset-2 transition-colors hover:text-markdown-link-hover hover:decoration-markdown-link-hover/75"}
		>
			<ChatImageLinkScope>
				<InsideMarkdownLink.Provider value>{children}</InsideMarkdownLink.Provider>
			</ChatImageLinkScope>
		</AppLink>
	);
}

/** Render one safe in-app session or artifact link using the surrounding ChatLinkProvider. */
export function SessionLabelLink({ href, children, className }: { href: string; children: ReactNode; className?: string }) {
	return <MarkdownLink href={href} className={className}>{children}</MarkdownLink>;
}

function MarkdownImage({ src, alt }: { src?: string | Blob; alt?: string }) {
	if (useContext(SafeOriginContent)) return <span className="text-muted-foreground">{alt || (typeof src === "string" ? src : "")}</span>;
	const { remoteHost } = useContext(OpenChatLink);
	if (remoteHost && typeof src === "string" && isHostLocalWebLink(src)) {
		return <span className="text-muted-foreground" title={REMOTE_PREVIEW_UNAVAILABLE}>{alt || src}</span>;
	}
	return <ChatImage src={src} alt={alt} />;
}

const ORIGIN_PREVIEW_ELEMENTS = ["a", "br", "code", "del", "em", "img", "strong"];
const ORIGIN_PREVIEW_COMPONENTS: Components = {
	a: ({ href, children }) => href && isTallpineLink(href)
		? <MarkdownLink href={href}>{children}</MarkdownLink>
		: <>{children}</>,
	img: ({ alt }) => <span className="text-muted-foreground">{alt}</span>,
	code: ({ children }) => <code className="font-mono text-[0.95em] text-markdown-code">{children}</code>,
};

/**
 * A collapsed cross-boundary report preview. Inline formatting and canonical
 * session links remain useful, while block UI, external links, and remote image
 * fetches stay inert until the reader expands the report.
 */
export const OriginPreviewMarkdown = memo(function OriginPreviewMarkdown({ text }: { text: string }) {
	return (
		<div className="chat-md whitespace-pre-wrap text-sm leading-relaxed text-muted-foreground">
			<Markdown
				allowedElements={ORIGIN_PREVIEW_ELEMENTS}
				unwrapDisallowed
				remarkPlugins={PLUGINS}
				components={ORIGIN_PREVIEW_COMPONENTS}
				urlTransform={chatUrlTransform}
			>
				{text}
			</Markdown>
		</div>
	);
});

/** Linkify canonical session URLs without interpreting any surrounding text as Markdown. */
export const SessionLinkedText = memo(function SessionLinkedText({ text }: { text: string }) {
	const links = findSessionLinks(text);
	if (links.length === 0) return text;
	const content: ReactNode[] = [];
	let cursor = 0;
	for (const link of links) {
		if (link.start > cursor) content.push(text.slice(cursor, link.start));
		content.push(
			<MarkdownLink key={`${link.start}:${link.text}`} href={link.text}>
				{link.text}
			</MarkdownLink>,
		);
		cursor = link.end;
	}
	if (cursor < text.length) content.push(text.slice(cursor));
	return <>{content}</>;
});

/**
 * A mermaid fence with the streaming state it was rendered under.
 *
 * A separate component rather than a hook call in the `pre` override because
 * overrides are not components: hooks are illegal there, and `COMPONENTS`
 * must stay module-level so react-markdown's memoization survives polling.
 */
function MermaidFence({ code }: { code: string }) {
	const streaming = useContext(StreamingProse);
	const { open: onLinkOpen, remoteHost } = useContext(OpenChatLink);
	const blockHostLocalLink = (event: ReactMouseEvent<HTMLDivElement>) => {
		const href = (event.target as Element).closest?.("a[href]")?.getAttribute("href");
		if (!href || !isHostLocalWebLink(href)) return;
		event.preventDefault();
		event.stopPropagation();
	};
	const diagram = <MermaidBlock code={code} streaming={streaming} onLinkOpen={onLinkOpen} />;
	return remoteHost
		? <div onClickCapture={blockHostLocalLink} onAuxClickCapture={blockHostLocalLink} onContextMenuCapture={blockHostLocalLink}>{diagram}</div>
		: diagram;
}

/** A path this chat can open in Files, and how to open it (at a line when given); undefined when it cannot. */
function useWorkspaceFile(path: string | undefined) {
	const { openFile, workspacePaths } = useContext(OpenChatLink);
	const filePath = path && openFile ? findWorkspaceFilePath(path, workspacePaths) : undefined;
	if (!path || !filePath || !openFile) return undefined;
	const target = workspaceFileOpenTarget(path, filePath);
	return { filePath, open: (line?: number) => (line == null ? openFile(target) : openFile(target, line)) };
}

/**
 * A workspace file named outside markdown: opens in Files at the line when this
 * chat can find it, as an inline code path does; otherwise plain text.
 */
export function FileReference({ path, line, className, children }: { path: string; line?: number; className?: string; children: ReactNode }) {
	const file = useWorkspaceFile(path);
	if (!file) return <span className={className} title={path}>{children}</span>;
	return (
		<button type="button" className={className} title={path} onClick={(event) => (isOpenLinkClick(event) ? file.open(line) : copyLinkText(path))}>
			{children}
		</button>
	);
}

/** The workspaces this chat's session links resolve against: the local daemon's, a remote host's, or Cloud's. */
export function useChatLinkWorkspaces() {
	const { sessionLinkHostId } = useContext(OpenChatLink);
	return useSessionLinkSource(sessionLinkHostId).workspaces;
}

/** Inline code naming a listed session artifact, linked to that artifact. */
export function ArtifactPathCode({ text, children, fallback }: { text: string; children: ReactNode; fallback?: ReactNode }) {
	const workspaces = useChatLinkWorkspaces();
	const href = artifactLinkForPath(text, workspaces);
	return href ? <MarkdownLink href={href} localPath={isLocalPathCandidate(text) ? text : undefined}>{children}</MarkdownLink> : <>{fallback ?? children}</>;
}

function InlineCode({ children }: { children?: ReactNode }) {
	const { openSession, remoteHost } = useContext(OpenChatLink);
	const insideLink = useContext(InsideMarkdownLink);
	const safeOrigin = useContext(SafeOriginContent);
	const text = typeof children === "string" ? children : undefined;
	const file = useWorkspaceFile(text);
	const code = (
		<code className="rounded bg-surface px-[5px] py-[2px] font-mono text-xs text-markdown-code">
			{children}
		</code>
	);
	if (insideLink) return code;
	if (!file) {
		const local = text && !remoteHost && !safeOrigin && isLocalPathCandidate(text) ? <LocalPathMenu path={text}>{code}</LocalPathMenu> : code;
		return text?.includes("/") && openSession ? <ArtifactPathCode text={text} fallback={local}>{code}</ArtifactPathCode> : local;
	}
	return (
		<button
			type="button"
			onClick={(event) => {
				if (isOpenLinkClick(event)) file.open(workspaceFileReferenceLine(text ?? ""));
				// An absolute path opens in its default app, as anywhere else in chat; a relative one is copied.
				else if (text && !remoteHost && isLocalPathCandidate(text)) void openLocalPathOnClick(text);
				else copyLinkText(text ?? "");
			}}
			aria-label={`Open ${file.filePath} in Files`}
			className="inline rounded text-left transition-colors hover:bg-interactive-hover"
		>
			{code}
		</button>
	);
}

type LocalPathSpanProps = ComponentProps<"span"> & { node?: unknown };

/**
 * react-markdown hands every override a fresh `node` (the hast element) on each
 * parse, although the span reads nothing from it. Compare the rest, so a
 * streaming reply that re-parses on every delta leaves its earlier
 * per-character fade spans (`rehypeStreamFade`) alone instead of re-rendering
 * every one of them.
 */
export function sameSpanProps(previous: LocalPathSpanProps, next: LocalPathSpanProps): boolean {
	// Called once per span per update, so it walks the props without allocating.
	const before = previous as Record<string, unknown>;
	const after = next as Record<string, unknown>;
	let count = 0;
	for (const key in after) {
		if (key === "node") continue;
		count += 1;
		if (!Object.is(before[key], after[key])) return false;
	}
	for (const key in before) if (key !== "node") count -= 1;
	return count === 0;
}

/** Plain-text path marked by `remarkLocalPaths`; any other span renders unchanged. */
const LocalPathSpan = memo(function LocalPathSpan(props: LocalPathSpanProps) {
	const { node: _node, children, ...rest } = props;
	const { remoteHost } = useContext(OpenChatLink);
	const insideLink = useContext(InsideMarkdownLink);
	const safeOrigin = useContext(SafeOriginContent);
	const path = (rest as Record<string, unknown>)["data-local-path"];
	if (typeof path !== "string" || remoteHost || insideLink || safeOrigin) return <span {...rest}>{children}</span>;
	return <LocalPathMenu path={path}>{children}</LocalPathMenu>;
}, sameSpanProps);

const COMPONENTS: Components = {
	// Headings step down in size but stay in the conversation's voice — an agent's
	// "## Findings" is a paragraph label, not a page title.
	h1: ({ children }) => (
		<h3 className="mb-1.5 mt-4 text-subtitle font-semibold leading-snug text-foreground first:mt-0">
			{children}
		</h3>
	),
	h2: ({ children }) => (
		<h4 className="mb-1.5 mt-3.5 text-sm font-semibold leading-snug text-foreground first:mt-0">
			{children}
		</h4>
	),
	h3: ({ children }) => (
		<h5 className="mb-1 mt-3 text-sm font-semibold leading-snug text-foreground first:mt-0">
			{children}
		</h5>
	),
	h4: ({ children }) => (
		<h6 className="mb-1 mt-3 text-xs font-semibold text-muted-foreground first:mt-0">
			{children}
		</h6>
	),

	// A paragraph of nothing but images is a set of pictures, not prose.
	p: ({ children, node }) =>
		isImageOnlyParagraph(node) ? (
			<ChatImageGallery>{children}</ChatImageGallery>
		) : (
			<p className="my-2 first:mt-0 last:mb-0">{compactEmoji(children)}</p>
		),

	ul: ({ children }) => <ul className="my-2 ml-4 list-disc space-y-1 first:mt-0">{children}</ul>,
	// Keep the source's first number: a reply of "6." is one item numbered 6, not 1.
	ol: ({ children, start }) => (
		<ol start={start} className="my-2 ml-4 list-decimal space-y-1 first:mt-0">{children}</ol>
	),
	li: ({ children, className }) => (
		// A task-list item drops its bullet: the checkbox is the marker.
		<li className={cn("marker:text-muted-foreground", className?.includes("task-list-item") && "list-none")}>
			{compactEmoji(children)}
		</li>
	),
	// Read-only on purpose. The checkbox reflects what the agent wrote; clicking it
	// would imply Tallpine could change the agent's plan, which it cannot.
	input: ({ checked, type }) =>
		type === "checkbox" ? (
			<input
				type="checkbox"
				checked={Boolean(checked)}
				readOnly
				aria-label={checked ? "done" : "not done"}
				className="mr-1.5 -ml-4 size-3 translate-y-[1px] accent-accent"
			/>
		) : null,

	// A fenced block arrives as `pre > code`, with the language — when the agent
	// gave one — as a class on the inner element. It is claimed here rather than in
	// `code` because a fence with no language carries no class at all, and matching
	// on the class alone rendered those as inline code.
	pre: ({ children }) => {
		const fence = fenceOf(children);
		if (!fence) return <>{children}</>;
		// Mermaid is a diagram, not source: it renders through the sandboxed SVG
		// path rather than the highlighter. See `MermaidBlock.tsx`.
		if (fence.language?.toLowerCase() === "mermaid") return <MermaidFence code={fence.code} />;
		return <CodeBlock code={fence.code} language={fence.language} />;
	},
	// Only inline code reaches here; `pre` above takes every fence.
	code: InlineCode,
	span: LocalPathSpan,

	// Wide tables scroll inside their own container so the conversation column
	// never scrolls sideways.
	table: ({ children }) => (
		<div className="my-2.5 overflow-x-auto rounded-lg border border-border">
			<table className="w-full border-collapse text-xs">{children}</table>
		</div>
	),
	thead: ({ children }) => <thead className="bg-raised/40">{children}</thead>,
	th: ({ children }) => (
		<th className="border-b border-border px-2.5 py-1.5 text-left font-medium text-muted-foreground">
			{compactEmoji(children)}
		</th>
	),
	td: ({ children }) => (
		<td className="border-b border-border/60 px-2.5 py-1.5 align-top">{compactEmoji(children)}</td>
	),

	blockquote: ({ children }) => (
		<blockquote className="my-2.5 border-l-2 border-border-strong pl-3 text-muted-foreground">
			{children}
		</blockquote>
	),
	hr: () => <hr className="my-3 border-border" />,
	strong: ({ children }) => <strong className="font-semibold text-foreground">{children}</strong>,
	del: ({ children }) => <del className="text-muted-foreground">{children}</del>,

	// Match terminal links: a plain HTTP(S) click uses the session's Tallpine Browser;
	// Cmd/Ctrl- or Option/Alt-click and non-web schemes use the system browser,
	// and right-click offers the system browser and copying the address.
	a: MarkdownLink,

	img: MarkdownImage,
};
