import type { ReactNode } from "react";
import { isOpenLinkClick } from "../lib/copy-link";
import { openLocalPathOnClick } from "../lib/local-path-click";
import { LocalPathMenuItems, useLocalPathMenu } from "./LocalPathMenuItems";
import { ContextMenu, ContextMenuContent, ContextMenuTrigger } from "./ui/context-menu";

/**
 * A local path from chat text. Click opens an existing file in its default app (allowlisted document types; anything
 * else is revealed, never executed) and copies a directory or missing path; Cmd/Ctrl+click reveals it in the file manager;
 * right-click offers Reveal / Open (allowlisted document types only) / Copy path. Existence is checked in main when the menu opens,
 * not at render, so prose that merely looks like a path costs nothing.
 */
export function LocalPathMenu({ path, children }: { path: string; children: ReactNode }) {
	const menu = useLocalPathMenu(path);
	const perform = menu.perform;
	return (
		<ContextMenu onOpenChange={menu.onOpenChange}>
			<ContextMenuTrigger asChild>
				<span
					role="button"
					tabIndex={0}
					className="cursor-pointer rounded hover:underline focus-visible:underline"
					onClick={(event) => (isOpenLinkClick(event) ? perform("reveal") : void openLocalPathOnClick(path))}
					onKeyDown={(event) => {
						if (event.key !== "Enter") return;
						event.preventDefault();
						if (isOpenLinkClick(event)) perform("reveal");
						else void openLocalPathOnClick(path);
					}}
				>
					{children}
				</span>
			</ContextMenuTrigger>
			<ContextMenuContent className="min-w-52">
				<LocalPathMenuItems menu={menu} />
			</ContextMenuContent>
		</ContextMenu>
	);
}
