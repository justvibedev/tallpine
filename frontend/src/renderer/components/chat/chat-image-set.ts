/**
 * The images a lightbox pages through.
 *
 * Every thumbnail button carries its source in data attributes, and a message
 * (or a tool row's strip) marks its extent with `data-chat-image-scope`. Opening
 * one thumbnail reads its siblings out of the DOM in document order, so paging
 * follows what the reader sees without each image registering itself.
 */

export interface ChatImageItem {
	/** The address copy and save fetch from (also picks CORS for the viewer). */
	url: string;
	/** What the viewer draws; differs from `url` for a remote image the user loaded. */
	src: string;
	label: string;
}

export const CHAT_IMAGE_SCOPE_ATTRIBUTE = "data-chat-image-scope";
export const CHAT_IMAGE_URL_ATTRIBUTE = "data-chat-image-url";
export const CHAT_IMAGE_LABEL_ATTRIBUTE = "data-chat-image-label";

function itemOf(button: Element): ChatImageItem | undefined {
	const url = button.getAttribute(CHAT_IMAGE_URL_ATTRIBUTE);
	if (!url) return undefined;
	const src = button.querySelector("img")?.getAttribute("src") ?? url;
	return { url, src, label: button.getAttribute(CHAT_IMAGE_LABEL_ATTRIBUTE) ?? "" };
}

/** The images in `button`'s scope, in order, and where `button` sits among them. */
export function collectChatImageSet(button: Element | null, fallback: ChatImageItem): { items: ChatImageItem[]; index: number } {
	const scope = button?.closest(`[${CHAT_IMAGE_SCOPE_ATTRIBUTE}]`);
	if (!button || !scope) return { items: [fallback], index: 0 };
	const buttons = Array.from(scope.querySelectorAll(`[${CHAT_IMAGE_URL_ATTRIBUTE}]`));
	const items: ChatImageItem[] = [];
	let index = 0;
	for (const candidate of buttons) {
		const item = itemOf(candidate);
		if (!item) continue;
		if (candidate === button) index = items.length;
		items.push(item);
	}
	return items.length > 0 ? { items, index } : { items: [fallback], index: 0 };
}
