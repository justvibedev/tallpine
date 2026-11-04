import { readFileSync } from "node:fs";

/**
 * ChatSessionScreen and the hooks/components split out of it, as one string.
 * Source-text tests assert wiring that now spans these files.
 */
const FILES = [
	"ChatSessionScreen.tsx",
	"ChatSessionBanners.tsx",
	"useReviewPromptState.ts",
	"useChatOpeners.ts",
	"useChatStartup.ts",
	"useInterfaceSwitchRequest.ts",
	"useConversationActionsMenu.ts",
	"useInterfaceTransitionBanner.ts",
	"dismissKeyboardBeforeSheet.ts",
];

export function chatSessionSource(): string {
	return FILES.map((f) => readFileSync(new URL(`./${f}`, import.meta.url), "utf8")).join("\n");
}

const COMPOSER_FILES = ["ChatComposer.tsx", "chatComposerStyles.ts", "composerAttachments.ts", "useReviewCardMotion.ts", "useComposerActions.ts"];

/** ChatComposer and the hooks/styles split out of it, as one string. */
export function chatComposerSource(): string {
	return COMPOSER_FILES.map((f) => readFileSync(new URL(`./${f}`, import.meta.url), "utf8")).join("\n");
}
