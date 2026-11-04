import { DndContext, DragOverlay, closestCenter } from "@dnd-kit/core";
import { SortableContext, horizontalListSortingStrategy } from "@dnd-kit/sortable";
import { Camera, Check, Copy, Eye, ExternalLink, Plus, Trash2 } from "lucide-react";
import { Button } from "../ui/button";
import { SETTINGS_MENU_ROW, SETTINGS_MENU_SURFACE } from "../settings/SettingsMenuTrigger";
import { Input } from "../ui/input";
import { Popover, PopoverAnchor, PopoverContent } from "../ui/popover";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";
import { cn } from "../../lib/utils";
import { handleTabListKeyDown } from "../../lib/terminal-tabs";
import { useBrowserPanelState } from "./useBrowserPanelState";
import { useBrowserPanelActions } from "./useBrowserPanelActions";
import { BrowserPanelViewProps } from "./browser-types";
import { getDisplayUrl, BrowserSuggestionIcon, faviconForOpenTab } from "./browser-helpers";
import { BrowserControlTooltip, SortableBrowserTopTab, BrowserTopTabDragOverlay } from "./BrowserTabs";
import { browserTopTabDragModifiers } from "./browser-constants";

/** Address bar, menus and derived chrome elements for the browser panel. */
export function useBrowserPanelChrome(props: BrowserPanelViewProps, state: ReturnType<typeof useBrowserPanelState>, actions: ReturnType<typeof useBrowserPanelActions>) {
	const {
		poppedOut,
	} = props;
	const {
		activeHistorySuggestion,
		activeTabId,
		agentPageOnHost,
		annotationAction,
		draggedTopTab,
		draggedTopTabId,
		handleTopTabDragEnd,
		historyMenuId,
		historyMenuRef,
		historyRequestGenerationRef,
		historySuggestions,
		navState,
		setActiveHistorySuggestion,
		setDraggedTopTabId,
		setHistorySuggestions,
		showTabsLeftFade,
		showTabsRightFade,
		t,
		tabScrollRef,
		tabSensors,
		tabs,
		urlCopied,
		urlEditing,
		urlInput,
		urlInputRef,
		viewId,
		annotationState,
	} = state;
	const {
		beginUrlEditing,
		copyCurrentURL,
		copyURLLabel,
		currentURLIsWeb,
		endUrlEditing,
		externalBlocked,
		handleCloseTab,
		handleOpenTab,
		handleSelectTab,
		handleURLChange,
		handleURLKeyDown,
		navigateFromAddressBar,
		openCurrentPageExternally,
		submit,
		suggestionsOpen,
	} = actions;
	const browserAddressBar = (
		<form
			className={cn(
				"browser-panel__address-bar min-w-0 flex-1",
				urlEditing && "browser-panel__address-bar--editing",
			)}
			data-testid="browser-address-bar"
			onFocusCapture={() => {
				// When docked, this form is portaled into the inspector header and is
				// therefore outside the browser-panel focus boundary below. Restore the
				// browser shortcut target when its address input receives focus.
				if (viewId) window.tallpine?.browser.notifyPanelUsed(viewId);
			}}
			onSubmit={submit}
		>
			<Popover
				onOpenChange={(open) => {
					if (!open && suggestionsOpen) {
						historyRequestGenerationRef.current += 1;
						setHistorySuggestions([]);
						setActiveHistorySuggestion(-1);
					}
				}}
				open={suggestionsOpen}
			>
				<PopoverAnchor asChild>
					<div className="browser-panel__url-wrap relative min-w-0 flex-1">
						<Input
							aria-activedescendant={activeHistorySuggestion >= 0 ? `${historyMenuId}-${activeHistorySuggestion}` : undefined}
							aria-controls={suggestionsOpen ? historyMenuId : undefined}
							aria-expanded={suggestionsOpen}
							aria-haspopup="listbox"
							aria-label={t("browser.url")}
							className="browser-panel__url-input h-browser-url text-xs"
							onBlur={endUrlEditing}
							onChange={(event) => handleURLChange(event.target.value)}
							onClick={() => urlInputRef.current?.select()}
							onFocus={beginUrlEditing}
							onKeyDown={handleURLKeyDown}
							placeholder={t("browser.urlPlaceholder")}
							ref={urlInputRef}
							value={urlEditing || poppedOut ? urlInput : getDisplayUrl(navState.url)}
						/>
						{navState.url ? (
							<BrowserControlTooltip label={copyURLLabel}>
								<Button
									aria-label={copyURLLabel}
									className={cn(
										"browser-panel__url-copy",
										!currentURLIsWeb && "browser-panel__url-copy--only",
									)}
									onClick={() => void copyCurrentURL()}
									size="icon-sm"
									type="button"
									variant="ghost"
								>
									<span className="relative size-icon-base">
										<Copy
											aria-hidden="true"
											className={cn(
												"absolute inset-0 size-icon-base transition-[opacity,transform] duration-150 motion-reduce:transition-none",
												urlCopied ? "scale-75 opacity-0" : "scale-100 opacity-100",
											)}
										/>
										<Check
											aria-hidden="true"
											className={cn(
												"absolute inset-0 size-icon-base text-success transition-[opacity,transform] duration-150 motion-reduce:transition-none",
												urlCopied ? "scale-100 opacity-100" : "scale-75 opacity-0",
											)}
										/>
									</span>
								</Button>
							</BrowserControlTooltip>
						) : null}
						{/* On an agent's host page, aria-disabled rather than disabled: a
						    disabled button would lose the tooltip that says why. */}
						{currentURLIsWeb ? (
							<BrowserControlTooltip label={agentPageOnHost ? t("browser.agentPageOnHost") : externalBlocked ? t("browser.externalNeedsLoopbackTcp") : t("inspector.openInSystemBrowser")}>
									<Button
										aria-disabled={agentPageOnHost || externalBlocked || undefined}
										aria-label={t("inspector.openInSystemBrowser")}
										className="browser-panel__url-external"
										onClick={() => void openCurrentPageExternally()}
										size="icon-sm"
										type="button"
										variant="ghost"
									>
										<ExternalLink aria-hidden="true" className={cn("size-icon-base", (agentPageOnHost || externalBlocked) && "opacity-50")} />
									</Button>
							</BrowserControlTooltip>
						) : null}
					</div>
				</PopoverAnchor>
				<PopoverContent
					align="start"
					aria-label={t("browser.urlSuggestions")}
					className={cn(
						SETTINGS_MENU_SURFACE,
						"browser-panel__history-suggestions",
					)}
					data-browser-native-overlay="true"
					id={historyMenuId}
					onOpenAutoFocus={(event) => event.preventDefault()}
					ref={historyMenuRef}
					role="listbox"
					sideOffset={4}
				>
					{historySuggestions.map((suggestion, index) => (
						<button
							aria-selected={index === activeHistorySuggestion}
							className={cn(
								SETTINGS_MENU_ROW,
								"flex w-full items-center gap-2.5 px-2.5 py-2 text-left",
								index === activeHistorySuggestion && "bg-settings-menu-selected text-settings-title",
							)}
							id={`${historyMenuId}-${index}`}
							key={suggestion.url}
							onClick={() => navigateFromAddressBar(suggestion.url)}
							onMouseDown={(event) => event.preventDefault()}
							onPointerMove={() => setActiveHistorySuggestion(index)}
							role="option"
							type="button"
						>
							<BrowserSuggestionIcon
								cachedFavicon={faviconForOpenTab(suggestion.url, tabs)}
								url={suggestion.url}
								viewId={viewId}
							/>
							<span className="min-w-0 flex-1">
								{suggestion.title ? (
									<span className="block truncate text-control text-settings-title">{suggestion.title}</span>
								) : null}
								<span className="block truncate text-caption text-settings-muted">{suggestion.url}</span>
							</span>
						</button>
					))}
				</PopoverContent>
			</Popover>
		</form>
	);
	const browserTabBar = (
		<div className="browser-panel__tab-bar" data-testid="browser-tab-bar">
			<DndContext
				collisionDetection={closestCenter}
				modifiers={browserTopTabDragModifiers}
				onDragCancel={() => setDraggedTopTabId(null)}
				onDragEnd={handleTopTabDragEnd}
				onDragStart={({ active }) => setDraggedTopTabId(String(active.id))}
				sensors={tabSensors}
			>
				<SortableContext items={tabs.map((tab) => tab.id)} strategy={horizontalListSortingStrategy}>
					<div className="browser-panel__tab-region">
						<div
							aria-label={t("browser.tabs")}
							className="browser-panel__tab-strip"
							onKeyDown={draggedTopTabId ? undefined : handleTabListKeyDown}
							ref={tabScrollRef}
							role="tablist"
						>
							{tabs.map((tab) => (
								<SortableBrowserTopTab
									key={tab.id}
									onClose={handleCloseTab}
									onSelect={handleSelectTab}
									selected={tab.id === activeTabId}
									tab={tab}
								/>
							))}
						</div>
						{showTabsLeftFade ? (
							<div aria-hidden="true" className="browser-panel__tab-fade browser-panel__tab-fade--left" />
						) : null}
						{showTabsRightFade ? <div aria-hidden="true" className="browser-panel__tab-fade" /> : null}
					</div>
				</SortableContext>
				<DragOverlay adjustScale={false} dropAnimation={null} modifiers={browserTopTabDragModifiers}>
					{draggedTopTab ? <BrowserTopTabDragOverlay tab={draggedTopTab} /> : null}
				</DragOverlay>
			</DndContext>
		</div>
	);
	const newTabButton = (
		<BrowserControlTooltip label={t("browser.openNewTab")}>
			<button
				aria-label={t("browser.openNewTab")}
				className={cn("browser-panel__tab-new", draggedTopTabId && "browser-panel__tab-new--dragging")}
				onClick={() => void handleOpenTab()}
				type="button"
			>
				<Plus aria-hidden="true" className="size-icon-base" />
			</button>
		</BrowserControlTooltip>
	);
	const annotationIdle = annotationState.count === 0 && !annotationState.hasDraft;
	const annotationToolbar = (
		<div className="browser-panel__toolbar browser-panel__toolbar--annotation">
			<div className="browser-panel__annotation-actions browser-panel__annotation-actions--leading">
				<Tooltip>
					<TooltipTrigger asChild>
						<Button
							aria-label={t("browser.annotationDiscardAllComments")}
							className="browser-panel__annotation-discard"
							disabled={annotationIdle && annotationState.screenshotCount === 0}
							onClick={() => void annotationAction("discard-all")}
							size="icon-sm"
							type="button"
							variant="ghost"
						>
							<Trash2 aria-hidden="true" className="size-icon-base" />
						</Button>
					</TooltipTrigger>
					<TooltipContent data-browser-native-overlay="true" side="bottom">
						{t("browser.annotationDiscardAll")}
					</TooltipContent>
				</Tooltip>
			</div>
			<div className="browser-panel__annotation-context">
				{annotationIdle ? (
					<span className="browser-panel__annotation-hint">{t("browser.annotationEmptyHint")}</span>
				) : (
					<>
						{annotationState.count > 0 ? (
							<span aria-hidden="true" className="browser-panel__annotation-status-dot" />
						) : null}
						<span className="browser-panel__annotation-count">
							{t("browser.annotationCount", { count: annotationState.count })}
						</span>
					</>
				)}
			</div>
			<div className="browser-panel__annotation-actions browser-panel__annotation-actions--trailing">
				<Tooltip>
					<TooltipTrigger asChild>
						<Button
							aria-label={t("browser.takeScreenshot")}
							onClick={() => void annotationAction("capture")}
							size="icon-sm"
							type="button"
							variant="ghost"
						>
							<Camera aria-hidden="true" className="size-icon-base" />
							{annotationState.screenshotCount > 0 ? (
								<span className="browser-panel__annotation-icon-count">{annotationState.screenshotCount}</span>
							) : null}
						</Button>
					</TooltipTrigger>
					<TooltipContent data-browser-native-overlay="true" side="bottom">
						{t("browser.takeScreenshot")}
					</TooltipContent>
				</Tooltip>
				<Tooltip>
					<TooltipTrigger asChild>
						<Button
							aria-label={t("browser.annotationOriginalPage")}
							disabled={annotationIdle}
							onBlur={() => void annotationAction("restore-preview")}
							onKeyDown={(event) => {
								if ((event.key !== "Enter" && event.key !== " ") || event.repeat) return;
								event.preventDefault();
								void annotationAction("preview-original");
							}}
							onKeyUp={(event) => {
								if (event.key !== "Enter" && event.key !== " ") return;
								void annotationAction("restore-preview");
							}}
							onPointerCancel={() => void annotationAction("restore-preview")}
							onPointerDown={() => void annotationAction("preview-original")}
							onPointerLeave={() => void annotationAction("restore-preview")}
							onPointerUp={() => void annotationAction("restore-preview")}
							size="icon-sm"
							type="button"
							variant="ghost"
						>
							<Eye aria-hidden="true" className="size-icon-base" />
						</Button>
					</TooltipTrigger>
					<TooltipContent data-browser-native-overlay="true" side="bottom">
						{t("browser.annotationOriginal")}
					</TooltipContent>
				</Tooltip>
				<span aria-hidden="true" className="browser-panel__annotation-separator" />
				<Button
					aria-label={t("browser.annotationSendAll")}
					className="browser-panel__annotation-send"
					disabled={annotationIdle}
					onClick={() => void annotationAction("submit")}
					size="sm"
					type="button"
					variant={annotationIdle ? "ghost" : "primary"}
				>
					{t("browser.annotationSend")}
					{annotationState.count > 0 ? (
						<span className="browser-panel__annotation-send-count">{annotationState.count}</span>
					) : null}
				</Button>
			</div>
		</div>
	);
	return {
		annotationToolbar,
		browserAddressBar,
		browserTabBar,
		newTabButton,
	};
}
