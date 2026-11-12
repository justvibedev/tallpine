import { useLexicalComposerContext } from "@lexical/react/LexicalComposerContext";
import { $createTextNode, $getNodeByKey, $isRangeSelection, $getSelection } from "lexical";
import { FileText } from "lucide-react";
import type { KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";
import { pastePreview } from "./composerPaste";

/** A large paste collapsed to a chip; the full text stays in the node and is what gets sent. */
export function ComposerPasteChip({ nodeKey, seq, lines, content }: {
	nodeKey: string;
	seq: string;
	lines: number;
	content: string;
}) {
	const { t } = useTranslation();
	const [editor] = useLexicalComposerContext();
	const preview = pastePreview(content);

	const onKeyDown = (event: KeyboardEvent<HTMLSpanElement>) => {
		if (event.key !== "Enter" && event.key !== " ") return;
		// Capture phase: Lexical's own Enter handler on the editor root must not see it (it would send).
		event.preventDefault();
		event.stopPropagation();
		expand();
	};

	// Replace the chip with its text; insertRawText keeps the line breaks.
	const expand = () => {
		editor.update(() => {
			const node = $getNodeByKey(nodeKey);
			if (!node) return;
			const anchor = $createTextNode("");
			node.replace(anchor);
			anchor.select();
			const selection = $getSelection();
			if ($isRangeSelection(selection)) selection.insertRawText(content);
		}, { discrete: true });
		editor.focus();
	};

	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<span
					data-composer-token="paste"
					data-value={seq}
					contentEditable={false}
					onDoubleClick={expand}
					onKeyDownCapture={onKeyDown}
					tabIndex={0}
					role="button"
					className="mx-0.5 inline-flex items-center gap-1 rounded-md border border-border-strong bg-interactive-hover px-1.5 py-0.5 align-middle text-[0.9em] leading-none text-foreground select-none"
				>
					<FileText aria-hidden="true" className="size-3 shrink-0" />
					{t("chat.paste.chip", { seq, count: lines })}
				</span>
			</TooltipTrigger>
			<TooltipContent side="top" className="max-w-md">
				<pre className="m-0 max-h-64 overflow-hidden font-mono text-xs whitespace-pre-wrap break-words">{preview.text}</pre>
				{preview.hiddenLines > 0 ? (
					<p className="m-0 mt-1 text-muted-foreground">{t("chat.paste.more", { count: preview.hiddenLines })}</p>
				) : null}
				<p className="m-0 mt-1 text-muted-foreground">{t("chat.paste.expandHint")}</p>
			</TooltipContent>
		</Tooltip>
	);
}
