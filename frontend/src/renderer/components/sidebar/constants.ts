import { cn } from "../../lib/utils";
import { NAV_ROW_HIGHLIGHT_HOST_CLASS } from "../NavRowHighlight";
import { isMacPlatform } from "../../lib/platform";

// macOS paints framed chrome: the fixed TitlebarNav cluster carries the
// history arrows above this surface. Windows hangs the sidebar under its
// custom titlebar.
export const isMac = isMacPlatform();
export const noDragStyle = isMac ? ({ WebkitAppRegion: "no-drag" } as React.CSSProperties) : undefined;

// One row-action layout shared by project rows, session rows, and the section
// header "+": 24px square buttons, 14px icons, a 6px gap, and a 4px right inset,
// vertically centered in the row, so every action's right edge lines up. Never
// painted: `.sidebar-icon-action` also opts out of the sidebar focus fill in
// styles.css. Hover/reveal stays instant (no transitions here).
export const ROW_ACTIONS_CLASS = "absolute inset-y-0 right-1 flex items-center gap-0.5";
/** Long enough that sweeping the pointer across the list starts no agents. */
export const AGENT_WARM_UP_HOVER_MS = 300;
export const ROW_ACTION_BUTTON_CLASS =
	"sidebar-icon-action grid size-6 shrink-0 place-items-center rounded-md !bg-transparent text-passive hover:!bg-interactive-hover focus:!bg-transparent focus-visible:!bg-interactive-hover active:!bg-interactive-hover data-[state=open]:!bg-interactive-hover hover:text-foreground focus-visible:text-foreground disabled:pointer-events-none disabled:opacity-50 data-[state=open]:text-foreground [&_svg]:size-icon-md";
export const HOVER_ACTION_CLASS = ROW_ACTION_BUTTON_CLASS;
export const SESSION_ACTION_CLASS = ROW_ACTION_BUTTON_CLASS;

// Shared nav-row chrome (Codex-style): inset pill, 14px type, no accent bar.
// Plain fill stays for non-interactive status rows; interactive rows use
// {@link NavRowHighlight} via {@link NAV_ROW_HIGHLIGHT_HOST_CLASS}.
export const NAV_ROW_CLASS =
	"h-9 gap-2.5 rounded-lg px-2.5 text-sm font-medium text-muted-foreground transition-colors hover:bg-interactive-hover hover:text-foreground active:bg-interactive-hover active:text-foreground data-[active=true]:bg-interactive-active data-[active=true]:font-medium data-[active=true]:text-foreground";

/** Expanded footer action row: growing highlight behind icon + label. */
export const FOOTER_NAV_BUTTON_CLASS = cn(
	NAV_ROW_CLASS,
	NAV_ROW_HIGHLIGHT_HOST_CLASS,
	"flex h-9 w-full items-center text-left transition-none",
);

// Top-of-sidebar rows (Search, Automations) and project rows: one 32px row with
// a 10px inset, 8px icon gap, and 14px icons so edges match the section headers
// and session rows.
export const SIDEBAR_ROW_CLASS = "h-8 gap-2 [&_svg]:size-icon-md";

// Search + Pinned/Projects section chrome: same type, icon, and row size.
export const SECTION_ROW_CLASS =
	"flex h-8 w-full min-w-0 items-center gap-2 rounded-md px-2.5 text-sm font-medium text-muted-foreground [&_svg]:size-icon-md [&_svg]:shrink-0";

// Mirrors the daemon's display-name cap (maxDisplayNameLen) and the spawn
// `--name` flag, so inline edits never round-trip a value the API would reject.

/** Initial item count shown in expanded sections; Show more/less toggles the remainder.
 *  Collapsed icon rail always shows the full list so projects stay reachable. */
export const SIDEBAR_INITIAL_SECTION_LIMIT = 10;
/** Initial agent count listed under each expanded project before its own Show more. */
export const SIDEBAR_PROJECT_SESSION_LIMIT = 6;
/** Section scrollers fill the height their flex parent leaves them and scroll inside it. */
export const SECTION_SCROLLER_CLASS =
	"scrollbar-none overflow-y-auto overflow-x-hidden overscroll-contain group-data-[collapsible=icon]:overflow-visible";
