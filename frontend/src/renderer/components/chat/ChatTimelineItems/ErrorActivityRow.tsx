import { AppLink } from "../../AppLink";
import { type ReactNode } from "react";
import { isWebLink, openLinkInSystemBrowser } from "../../../lib/external-link-policy";
import { type ConversationActivity } from "../../../types/conversation";

/**
 * A provider error item — reconnect storms, credit exhaustion, and similar.
 *
 * Codex (and peers) often put the whole JSON envelope in `message` / `summary`.
 * Rendering that as a generic activity label made one long unbreakable line that
 * widened the chat column under the sidebars, and five reconnect attempts painted
 * five walls of red JSON. This row unwraps the human parts and always wraps inside
 * the column.
 *
 * Not a live region: the enclosing timeline is already a `role="log"`, and the
 * controller banner announces a terminal failure. Marking every historical
 * reconnect row as `role="alert"` would interrupt a screen reader once per attempt.
 */
export function ErrorActivityRow({ activity }: { activity: ConversationActivity }) {
	const { headline, detail } = providerErrorCopy(activity);
	const actionUrl = String(activity.detail?.actionUrl ?? "").trim();
	const standaloneActionUrl = actionUrl && !detail?.includes(actionUrl) ? actionUrl : undefined;
	return (
		<div className="flex min-w-0 max-w-full items-baseline overflow-hidden py-0.5 text-xs leading-snug text-muted-foreground">
			<span className="wrap-anywhere min-w-0 whitespace-pre-wrap">
				<span>{linkifiedProviderErrorText(headline)}</span>
				{detail ? (
					<>
						{" — "}
						<span className="text-muted-foreground/80">{linkifiedProviderErrorText(detail)}</span>
					</>
				) : null}
				{standaloneActionUrl ? (
					<>
						{detail ? " " : " — "}
						{isWebLink(standaloneActionUrl) ? (
							<ProviderErrorLink href={standaloneActionUrl} />
						) : (
							<span className="text-muted-foreground/80">{standaloneActionUrl}</span>
						)}
					</>
				) : null}
			</span>
		</div>
	);
}

const providerErrorWebUrlPattern = /\bhttps?:\/\/[^\s<>"'`]+/giu;

const trailingProviderUrlPunctuation = /[),.;!?}\]]+$/u;

function ProviderErrorLink({ href }: { href: string }) {
	return (
		<AppLink
			href={href}
			target="_blank"
			rel="noreferrer noopener"
			onClick={(event) => {
				event.preventDefault();
				void openLinkInSystemBrowser(href);
			}}
			className="text-markdown-link underline decoration-markdown-link/45 underline-offset-2 transition-colors hover:text-markdown-link-hover hover:decoration-markdown-link-hover/75"
		>
			{href}
		</AppLink>
	);
}

/** Render provider prose verbatim, activating only literal HTTP(S) URLs. */
export function linkifiedProviderErrorText(text: string): ReactNode[] {
	const parts: ReactNode[] = [];
	let cursor = 0;
	for (const match of text.matchAll(providerErrorWebUrlPattern)) {
		const start = match.index;
		const rawUrl = match[0];
		const href = rawUrl.replace(trailingProviderUrlPunctuation, "");
		if (start > cursor) parts.push(text.slice(cursor, start));
		if (href && isWebLink(href)) {
			parts.push(<ProviderErrorLink key={`${start}-${href}`} href={href} />);
			if (href.length < rawUrl.length) parts.push(rawUrl.slice(href.length));
		} else {
			parts.push(rawUrl);
		}
		cursor = start + rawUrl.length;
	}
	if (cursor < text.length) parts.push(text.slice(cursor));
	return parts;
}

/**
 * Prefer the provider's short status line and `additionalDetails` over a raw JSON
 * dump. Falls back to the stored text when it is already plain language.
 */
export function providerErrorCopy(activity: ConversationActivity): {
	headline: string;
	detail?: string;
} {
	const candidates = [activity.detail?.message, activity.summary, activity.detail?.error];
	for (const candidate of candidates) {
		const raw = String(candidate ?? "").trim();
		if (!raw) continue;
		const unwrapped = unwrapProviderErrorJson(raw);
		if (unwrapped) return unwrapped;
	}

	const headline = String(activity.detail?.message ?? activity.summary ?? "").trim();
	const extra = String(activity.detail?.error ?? "").trim();
	if (!headline) return { headline: extra || "Provider error" };
	if (extra && extra !== headline) return { headline, detail: extra };
	return { headline };
}

function unwrapProviderErrorJson(raw: string): { headline: string; detail?: string } | undefined {
	const parsed = parseJsonObjectSuffix(raw);
	const err = parsed
		? parsed.error && typeof parsed.error === "object" && !Array.isArray(parsed.error)
			? (parsed.error as Record<string, unknown>)
			: parsed
		: undefined;
	const message =
		typeof err?.message === "string"
			? err.message.trim()
			: readJsonStringField(raw, "message");
	const additional =
		typeof err?.additionalDetails === "string"
			? err.additionalDetails.trim()
			: readJsonStringField(raw, "additionalDetails");
	if (!message && !additional) return undefined;
	if (message && additional && additional !== message) {
		return { headline: message, detail: additional };
	}
	return { headline: message || additional };
}

/** Read a complete string field from an envelope whose trailing JSON was truncated. */
function readJsonStringField(raw: string, field: "message" | "additionalDetails"): string {
	const match = new RegExp(`"${field}"\\s*:\\s*("(?:\\\\.|[^"\\\\])*")`).exec(raw);
	if (!match?.[1]) return "";
	try {
		const value = JSON.parse(match[1]) as unknown;
		return typeof value === "string" ? value.trim() : "";
	} catch {
		return "";
	}
}

/** Tallpine used to persist `provider error: {…}`; parse the JSON object even when prefixed. */
function parseJsonObjectSuffix(raw: string): Record<string, unknown> | undefined {
	const start = raw.indexOf("{");
	if (start < 0) return undefined;
	const slice = raw.slice(start).trim();
	const asObject = (value: unknown): Record<string, unknown> | undefined => {
		if (!value || typeof value !== "object" || Array.isArray(value)) return undefined;
		return value as Record<string, unknown>;
	};
	try {
		return asObject(JSON.parse(slice));
	} catch {
		const end = slice.lastIndexOf("}");
		if (end <= 0) return undefined;
		try {
			return asObject(JSON.parse(slice.slice(0, end + 1)));
		} catch {
			return undefined;
		}
	}
}
