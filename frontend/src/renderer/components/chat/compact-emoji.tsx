import { Fragment, isValidElement, type ReactNode } from "react";
import { EMOJI_GRAPHEME } from "../../lib/rehype-stream-fade";

const GRAPHEME_SEGMENTER = new Intl.Segmenter(undefined, { granularity: "grapheme" });

/** Wrap each emoji grapheme in prose in a span so chat can size it to the text around it. */
export function compactEmoji(children: ReactNode): ReactNode {
	if (typeof children === "string") {
		if (!EMOJI_GRAPHEME.test(children)) return children;
		let last = 0;
		const parts: ReactNode[] = [];
		for (const { segment, index } of GRAPHEME_SEGMENTER.segment(children)) {
			if (!EMOJI_GRAPHEME.test(segment)) continue;
			if (index > last) parts.push(children.slice(last, index));
			parts.push(
				<span key={`${index}-${segment}`} className="chat-md-emoji">
					{segment}
				</span>,
			);
			last = index + segment.length;
		}
		if (last === 0) return children;
		if (last < children.length) parts.push(children.slice(last));
		return parts;
	}
	if (Array.isArray(children)) {
		// An element react-markdown already keyed needs no wrapper: it holds no text for
		// `compactEmoji` to split, and a wrapper per streamed character span is a
		// wasted element on every update.
		return children.map((child, index) => isValidElement(child) && child.key !== null ? child : <Fragment key={index}>{compactEmoji(child)}</Fragment>);
	}
	return children;
}
