import { Lightbulb } from "lucide-react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { useTranslation } from "react-i18next";
import { NAV_ROW_HIGHLIGHT_HOST_CLASS, NavRowHighlight } from "../NavRowHighlight";
import { cn } from "../../lib/utils";


export const EMPTY_CHAT_PLACEHOLDERS = [
	"Fix a failing test in this project",
	"Explain how this project is structured",
	"Plan the next step for this feature",
	"Find and fix a bug in this project",
] as const;

const ARTIFACT_PROMPT_KEYS = [
	"chat.welcome.artifactPrompt.plan",
	"chat.welcome.artifactPrompt.compare",
	"chat.welcome.artifactPrompt.notes",
] as const;

const CHAT_PROMPT_KEYS = [
	"chat.welcome.prompt.review",
	"chat.welcome.prompt.tests",
	"chat.welcome.prompt.trace",
] as const;

/**
 * Sample requests above an empty chat's composer; picking one fills the composer.
 * The list collapses its height, and the gap below it, on the way in and out so the
 * centered composer glides instead of jumping.
 */
export function WelcomePromptSuggestions({ open, artifact, onPick }: { open: boolean; artifact: boolean; onPick: (text: string) => void }) {
	const { t } = useTranslation();
	const reducedMotion = useReducedMotion();
	const keys = artifact ? ARTIFACT_PROMPT_KEYS : CHAT_PROMPT_KEYS;
	return (
		<AnimatePresence initial={false}>
			{open ? (
				<motion.div
					key="welcome-prompts"
					initial={{ height: 0, opacity: 0 }}
					animate={{ height: "auto", opacity: 1, transition: { duration: reducedMotion ? 0 : 0.2, ease: [0.22, 1, 0.36, 1] } }}
					exit={{ height: 0, opacity: 0, transition: { duration: reducedMotion ? 0 : 0.16, ease: [0.4, 0, 1, 1] } }}
					// The dock's gap-2 sits outside this box. Cancel it and put the space back as
					// padding inside, so it collapses with the height instead of snapping.
					className="-mb-2 overflow-hidden"
				>
					<ul aria-label={t(artifact ? "chat.welcome.artifactPrompts" : "chat.welcome.prompts")} className="flex flex-col divide-y divide-border pb-3">
						{keys.map((key) => (
							<li key={key} className="py-0.5">
								<button
									type="button"
									// Same growing pill as the sidebar rows; the host stays transparent.
									className={cn(
										"welcome-prompt-suggestion flex h-8 w-full items-center gap-2.5 rounded-lg px-2.5 text-left text-control text-muted-foreground focus-visible:outline-none",
										NAV_ROW_HIGHLIGHT_HOST_CLASS,
									)}
									onClick={() => onPick(t(key))}
								>
									<NavRowHighlight />
									<Lightbulb aria-hidden="true" className="relative z-[1] size-icon-sm shrink-0" />
									<span className="relative z-[1] truncate">{t(key)}</span>
								</button>
							</li>
						))}
					</ul>
				</motion.div>
			) : null}
		</AnimatePresence>
	);
}
