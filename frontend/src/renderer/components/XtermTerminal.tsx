// Self-contained xterm.js surface, ported from yyork's terminal architecture.
//
// Design rules (the reason this component exists):
//  - The mount effect is dependency-free: the terminal instance is created once
//    per mount and NEVER torn down because a callback identity changed.
//    TerminalPane's shell-owned cache chooses the mount lifetime: retained
//    handle generations survive route switches, replacement handles get a clean
//    surface, and same-handle reconnects reuse the mounted renderer.
//  - Nothing writes into the buffer at mount. Status/empty-state belongs to DOM
//    chrome around the terminal, not inside it. Writing before layout settles
//    is what crashed xterm's Viewport (`dimensions` of a zero-sized renderer).
//  - Fitting runs on several triggers, not one: FitAddon derives the grid from
//    the measured cell box, and if it measures before the monospace font's real
//    metrics (and the post-open renderer) are resolved it mis-counts cols/rows
//    and the grid clips inside the panel. So: next frame, two settle timeouts,
//    fonts.ready, a ResizeObserver, AND an onRender convergence loop that
//    re-fits until the proposed grid stops changing (the last is the only
//    trigger that recovers a clipped grid without the host box resizing). xterm
//    itself only fires onResize when the grid actually changed, so repeated
//    fits don't spam the PTY. Measured resizes commit before paint and publish
//    the new PTY size immediately, including during synchronized redraws.

import { tallpineBridge } from "../lib/bridge";
import { openLinkInSystemBrowser } from "../lib/external-link-policy";
import { TerminalSearch } from "./TerminalSearch";
import { LinkPreviewCard, LinkPreviewCardLoading } from "./LinkPreviewCard";
import { HoverCard, HoverCardContent, HoverCardTrigger } from "./ui/hover-card";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "./ui/dropdown-menu";

import { useXtermState } from "./terminal/useXtermState";
import { useXtermThemeEffects } from "./terminal/useXtermThemeEffects";
import { useXtermMount } from "./terminal/useXtermMount";
import { useXtermFocusEffects } from "./terminal/useXtermFocusEffects";
import type { XtermTerminalProps } from "./terminal/xterm-types";

export function XtermTerminal(props: XtermTerminalProps) {
	const state = useXtermState(props);
	useXtermThemeEffects(props, state);
	useXtermMount(props, state);
	useXtermFocusEffects(props, state);
	const {
		contextMenu,
		copiedToast,
		focusTerminal,
		hostRef,
		linkPreview,
		linkPreviewControlsRef,
		linkPreviewQuery,
		macPlatform,
		runContextMenuAction,
		scrollbarThumbRef,
		scrollbarTrackRef,
		searchAddonRef,
		searchOpen,
		setContextMenuOpen,
		setSearchOpen,
		shellRef,
		t,
		toggleFullscreenAndRestoreFocus,
	} = state;
	const fullscreenElement = document.fullscreenElement;
	const contextMenuPortalContainer =
		props.isFullscreen &&
		fullscreenElement instanceof HTMLElement &&
		hostRef.current &&
		fullscreenElement.contains(hostRef.current)
			? fullscreenElement
			: undefined;

	return (
		<>
			<div
				ref={shellRef}
				aria-label={props.ariaLabel}
				className={props.className}
				style={{
					height: "100%",
					overflow: "hidden",
					position: "relative",
					width: "100%",
				}}
			>
				<div
					ref={hostRef}
					className={macPlatform ? "terminal-xterm-host terminal-xterm-host--mac" : "terminal-xterm-host"}
					style={{
						backgroundColor: "var(--color-bg-terminal-opaque)",
						position: "relative",
						contain: "paint",
						minWidth: 0,
						height: "100%",
						overflow: "hidden",
						width: "100%",
					}}
				/>
				{macPlatform ? (
					<div
						aria-hidden="true"
						className="terminal-scrollbar"
						data-active="false"
						data-scrollable="false"
						ref={scrollbarTrackRef}
					>
						<div className="terminal-scrollbar__thumb" ref={scrollbarThumbRef} />
					</div>
				) : null}
				<TerminalSearch
					onClose={() => setSearchOpen(false)}
					onReturnFocus={focusTerminal}
					open={searchOpen && props.isVisible !== false}
					searchAddon={searchAddonRef.current}
				/>
				{copiedToast && props.isVisible !== false ? (
					<div
						aria-live="polite"
						className="pointer-events-none absolute bottom-3 left-1/2 z-10 -translate-x-1/2 rounded-md border border-[var(--color-border-import-modal)] bg-[var(--color-bg-import-modal)] px-3 py-1.5 text-xs text-[var(--color-text-import-title)] shadow-[var(--shadow-import-modal)]"
						role="status"
					>
						{t("terminal.copiedToClipboard")}
					</div>
				) : null}
			</div>
			<DropdownMenu modal={false} open={contextMenu.open} onOpenChange={setContextMenuOpen}>
				<DropdownMenuTrigger asChild>
					<button
						type="button"
						aria-hidden="true"
						tabIndex={-1}
						style={{
							border: 0,
							height: 0,
							left: contextMenu.x,
							opacity: 0,
							padding: 0,
							pointerEvents: "none",
							position: "fixed",
							top: contextMenu.y,
							width: 0,
						}}
					/>
				</DropdownMenuTrigger>
				<DropdownMenuContent
					align="start"
					className="min-w-36"
					onCloseAutoFocus={(event) => event.preventDefault()}
					portalContainer={contextMenuPortalContainer}
					side="right"
					sideOffset={2}
				>
					{contextMenu.link ? (
						<>
							<DropdownMenuItem disabled={!props.onLinkOpen} onSelect={() => {
								const { link } = contextMenu;
								setContextMenuOpen(false);
								if (link) props.onLinkOpen?.(link);
							}}>
								{t("link.openInTallpineBrowser")}
							</DropdownMenuItem>
							<DropdownMenuItem
								onSelect={() => {
									const { link } = contextMenu;
									setContextMenuOpen(false);
									if (link) void openLinkInSystemBrowser(link);
								}}
							>
								{t("link.openInExternalBrowser")}
							</DropdownMenuItem>
							<DropdownMenuSeparator />
							<DropdownMenuItem onSelect={() => {
								const { link } = contextMenu;
								setContextMenuOpen(false);
								if (link) void tallpineBridge.clipboard.writeText(link);
							}}>
								{t("link.copy")}
							</DropdownMenuItem>
							<DropdownMenuSeparator />
						</>
					) : null}
					<DropdownMenuItem disabled={!contextMenu.canCopy} onSelect={() => runContextMenuAction("copy")}>
						{t("titlebar.copy")}
					</DropdownMenuItem>
					<DropdownMenuItem onSelect={() => runContextMenuAction("paste")}>{t("titlebar.paste")}</DropdownMenuItem>
					<DropdownMenuItem onSelect={() => runContextMenuAction("selectAll")}>{t("titlebar.selectAll")}</DropdownMenuItem>
					<DropdownMenuSeparator />
					<DropdownMenuItem
						onSelect={() => {
							setContextMenuOpen(false);
							setSearchOpen(true);
						}}
					>
						{t("terminal.search")}
					</DropdownMenuItem>
					{props.onToggleFullscreen ? (
						<DropdownMenuItem
							onSelect={() => {
								setContextMenuOpen(false);
								void toggleFullscreenAndRestoreFocus();
							}}
						>
							{props.isFullscreen ? t("terminal.exitFullscreen") : t("terminal.fullscreen")}
						</DropdownMenuItem>
					) : null}
				</DropdownMenuContent>
			</DropdownMenu>
			<HoverCard
				open={linkPreview !== null}
				onOpenChange={(open) => {
					if (!open) linkPreviewControlsRef.current.hide(true);
				}}
			>
				<HoverCardTrigger asChild>
					<button
						type="button"
						aria-hidden="true"
						tabIndex={-1}
						style={{
							border: 0,
							height: 0,
							left: linkPreview?.x ?? 0,
							opacity: 0,
							padding: 0,
							pointerEvents: "none",
							position: "fixed",
							top: linkPreview?.y ?? 0,
							width: 0,
						}}
					/>
				</HoverCardTrigger>
				{linkPreview && !linkPreviewQuery.isError ? (
					<HoverCardContent
						collisionPadding={8}
						portalContainer={contextMenuPortalContainer}
						onPointerEnter={() => linkPreviewControlsRef.current.cancelHide()}
						onPointerLeave={() => linkPreviewControlsRef.current.hide()}
					>
						{linkPreviewQuery.data ? (
							<LinkPreviewCard url={linkPreview.url} preview={linkPreviewQuery.data} onBrowserOpen={props.onLinkOpen} />
						) : (
							<LinkPreviewCardLoading />
						)}
					</HoverCardContent>
				) : null}
			</HoverCard>
		</>
	);
}
