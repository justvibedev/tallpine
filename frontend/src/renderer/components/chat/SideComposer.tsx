import { ArrowUp, Loader2, Plus, Square, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Spinner } from "../ui/spinner";
import {
	type Side,
} from "../../lib/side-chats-model";
import type { ChatModel, ChatSkill } from "../../types/conversation";
import { Button } from "../ui/button";
import { ChatAnnotationSummary, type ChatAnnotationSummaryItem } from "./ChatAnnotationSummary";
import { ComposerEditor, type ComposerEditorHandle, type ComposerTrigger } from "./ComposerEditor";
import { moveHighlight, rankSkills } from "./composerSuggest";
import { ComposerSuggestMenu } from "./ComposerSuggestMenu";
import { type SideChatDraft } from "./sideChatDraft";
import { TurnSettingsBar } from "./TurnSettingsBar";

export function SideComposer({
	draft,
	onChange,
	onSend,
	onInterrupt,
	onAttach,
	files,
	onRemoveFile,
	ready,
	running,
	sending,
	deliveryPending = false,
	models,
	skills,
	side,
	onSettings,
	onRemoveReference,
	onRemoveAllReferences,
	onFocusSide,
	onCompact,
	focusKey,
	queuedDock,
	onNavigate,
}: {
	draft: SideChatDraft;
	onChange: (text: string) => void;
	onSend: (text: string) => void;
	onInterrupt: () => void;
	onAttach: (files: File[]) => void;
	files: File[];
	onRemoveFile: (index: number) => void;
	ready: boolean;
	running: boolean;
	sending: boolean;
	deliveryPending?: boolean;
	models: ChatModel[];
	skills: ChatSkill[];
	side: Side;
	onSettings: (model: string, effort: string) => void;
	onRemoveReference: (id: string) => void;
	onRemoveAllReferences: () => void;
	onFocusSide?: () => void;
	onCompact: () => void;
	focusKey: number;
	queuedDock?: ReactNode;
	onNavigate?: (annotation: ChatAnnotationSummaryItem) => void;
}) {
	const { t } = useTranslation();
	const editor = useRef<ComposerEditorHandle>(null);
	const [concealExpired, setConcealExpired] = useState(false);
	useEffect(() => {
		if (!sending) {
			setConcealExpired(false);
			return;
		}
		const timer = window.setTimeout(() => setConcealExpired(true), 3000);
		return () => window.clearTimeout(timer);
	}, [sending]);
	const filePicker = useRef<HTMLInputElement>(null);
	const btwHandled = useRef(false);
	const compactHandled = useRef(false);
	const [highlighted, setHighlighted] = useState(0);
	const [dismissed, setDismissed] = useState<string>();
	const [trigger, setTrigger] = useState<ComposerTrigger>();
	const commands = useMemo(
		() => [
			...(onFocusSide ? ["btw", "side"].map((name) => ({ name, displayName: name, description: "Opens a side chat", source: "Tallpine" })) : []),
			{
				name: "compact",
				displayName: "compact",
				description: "Compacts this side chat",
				source: "Tallpine",
			},
			...skills.filter((skill) => skill.name !== "compact" && (!onFocusSide || (skill.name !== "btw" && skill.name !== "side"))),
		],
		[skills, onFocusSide],
	);
	const suggestions = trigger?.kind === "skill" && trigger.key !== dismissed ? rankSkills(commands, trigger.query) : [];
	const choose = (value: string) => {
		if ((value === "btw" || value === "side") && onFocusSide) {
			onChange((editor.current?.getSnapshot().text ?? draft.text).replace(/\/[^\s]*$/, "").trim());
			onFocusSide();
			setDismissed(trigger?.key);
			return;
		}
		if (trigger) editor.current?.insertToken(trigger, value);
	};
	useEffect(() => {
		if (editor.current?.getSnapshot().text !== draft.text) editor.current?.setText(draft.text);
	}, [draft.text]);
	useEffect(() => {
		if (focusKey && ready) editor.current?.focus();
	}, [focusKey, ready]);
	const canSend =
		ready &&
		!sending &&
		!deliveryPending &&
		Boolean(draft.text.trim() || files.length || draft.attachments.length || draft.references.length);
	return (
		<div className="cursor-chat-composer-dock shrink-0 px-4 pb-3">
			<div aria-hidden="true" className="chat-composer-fade" />
			<div className="mx-auto w-full max-w-3xl">
				{queuedDock}
				<form
					className="cursor-chat-composer relative mx-auto flex w-full max-w-3xl cursor-text flex-col gap-1.5 px-3 pt-3 pb-3"
					onSubmit={(event) => {
						event.preventDefault();
						const text = editor.current?.getSnapshot().text ?? draft.text;
						if (text.trim() === "/compact") {
							onCompact();
							return;
						}
						if (canSend) onSend(text);
					}}
					onClick={(event) => {
						if (event.target === event.currentTarget) editor.current?.focus();
					}}
				>
					{suggestions.length && trigger ? (
						<ComposerSuggestMenu
							id="side-chat-completions"
							kind="skill"
							items={suggestions}
							highlighted={Math.min(highlighted, suggestions.length - 1)}
							onPick={choose}
						/>
					) : null}
					{draft.references.length || draft.attachments.length || files.length ? (
						<div className="flex flex-wrap items-center gap-1.5" data-testid="side-chat-attachments-and-annotations">
							<ChatAnnotationSummary
								annotations={draft.references}
								onSelect={onNavigate}
								onRemove={(ref) => ref.id && onRemoveReference(ref.id)}
								onRemoveAll={onRemoveAllReferences}
								disabled={sending || deliveryPending}
							/>
							{draft.attachments.length ? (
								<ul className="contents" aria-label={t("sideChat.stagedFiles")}>
									{draft.attachments.map((file, index) => (
										<li
											key={file.id}
											className="flex items-center gap-1.5 rounded border border-border px-2 py-1 text-[11px]"
										>
											{file.name}
											<button
												type="button"
												aria-label={t("sideChat.remove", { value1: file.name })}
												onClick={() => onRemoveFile(index)}
											>
												<X className="size-3" />
											</button>
										</li>
									))}
								</ul>
							) : null}
							{files.length ? (
								<ul className="contents" aria-label={t("sideChat.attachedFiles")}>
									{files.map((file, index) => (
										<li
											key={`${file.name}-${index}`}
											className="flex items-center gap-1.5 rounded border border-border bg-background px-2 py-1 text-[11px]"
										>
											<span className="max-w-[120px] truncate" title={file.name}>
												{file.name}
											</span>
											<button
												type="button"
												aria-label={t("sideChat.remove", { value1: file.name })}
												onClick={() => onRemoveFile(index + draft.attachments.length)}
											>
												<X className="size-3" />
											</button>
										</li>
									))}
								</ul>
							) : null}
						</div>
					) : null}
					<ComposerEditor
						ref={editor}
						concealed={sending && !concealExpired}
						label="Side chat question"
						placeholder={
							ready
								? running
									? t("sideChat.nextMessageQueued")
									: t("sideChat.messageTheAgent")
								: t("sideChat.theControllerIsNotConnected")
						}
						menuOpen={suggestions.length > 0}
						menuId="side-chat-completions"
						activeIndex={Math.min(highlighted, suggestions.length - 1)}
						onChange={(next) => {
							onChange(next.text);
							setTrigger(next.trigger);
							setDismissed(undefined);
							setHighlighted(0);
						}}
						onComplete={(next, key) => {
							const matches = next.trigger?.kind === "skill" ? rankSkills(commands, next.trigger.query) : [];
							const chosen = matches[Math.min(highlighted, matches.length - 1)];
							if ((chosen?.value === "btw" || chosen?.value === "side") && key === "Enter" && onFocusSide) {
								btwHandled.current = true;
								onChange(next.text.replace(/\/[^\s]*$/, "").trim());
								onFocusSide();
								setDismissed(next.trigger?.key);
								return undefined;
							}
							if (chosen?.value === "compact" && key === "Enter" && next.text.trim() === "/compact") {
								compactHandled.current = true;
								onCompact();
								return undefined;
							}
							return chosen?.value;
						}}
						onEnter={(next, event) => {
							if (event.shiftKey) return false;
							if (btwHandled.current) {
								btwHandled.current = false;
								return true;
							}
							if (compactHandled.current) {
								compactHandled.current = false;
								return true;
							}
							if (/^\/(?:btw|side)$/i.test(next.text.trim()) && onFocusSide) {
								onChange("");
								onFocusSide();
								return true;
							}
							if (next.text.trim() === "/compact") {
								onCompact();
								return true;
							}
							if (
								ready &&
								!sending &&
								!deliveryPending &&
								(next.text.trim() || files.length || draft.attachments.length || draft.references.length)
							)
								onSend(next.text);
							return true;
						}}
						onCompositionChange={() => {}}
						onKeyDown={(event) => {
							if (event.key === "ArrowDown" || event.key === "ArrowUp") {
								event.preventDefault();
								setHighlighted((value) => moveHighlight(value, event.key === "ArrowDown" ? 1 : -1, suggestions.length));
							}
						}}
						onPaste={() => {}}
					/>
					<div className="flex h-7 items-center gap-1.5">
						<div
							role="group"
							aria-label={t("sideChat.messageTools")}
							className="flex min-w-0 flex-1 items-center gap-0.5"
						>
							<input
								ref={filePicker}
								type="file"
								multiple
								hidden
								onChange={(event) => {
									onAttach(Array.from(event.target.files ?? []));
									event.target.value = "";
								}}
							/>
							<Button
								type="button"
								variant="ghost"
								size="icon-sm"
								disabled={!ready || sending}
								aria-label={t("sideChat.attachAFile")}
								onClick={() => filePicker.current?.click()}
								className="size-7 shrink-0 rounded-full p-0 text-muted-foreground hover:bg-white/5! hover:text-foreground"
							>
								<Plus aria-hidden="true" className="size-3.5" />
							</Button>
							<TurnSettingsBar
								models={models}
								settings={{ model: side.model, reasoningEffort: side.effort }}
								onChange={(next) => onSettings(next.model ?? "", next.reasoningEffort ?? "")}
								showApprovalMode={false}
								disabled={!ready || sending}
							/>
						</div>
						<Button
							type={running && !canSend ? "button" : "submit"}
							variant="ghost"
							size="icon-sm"
							aria-label={running && !canSend ? t("sideChat.stopTurn") : t("sideChat.sendMessage")}
							onClick={running && !canSend ? onInterrupt : undefined}
							disabled={running && !canSend ? false : !canSend}
							className="size-7 rounded-full bg-foreground text-on-foreground hover:bg-foreground/90 hover:text-on-foreground dark:hover:bg-foreground/90 dark:hover:text-on-foreground"
						>
							{running && !canSend ? (
								<Square aria-hidden="true" className="size-2.5 fill-current" />
							) : sending ? (
								<Spinner icon={Loader2}   className="size-3.5" />
							) : (
								<ArrowUp aria-hidden="true" className="size-3.5" />
							)}
						</Button>
					</div>
				</form>
			</div>
		</div>
	);
}
