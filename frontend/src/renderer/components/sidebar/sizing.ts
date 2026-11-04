/** How the sidebar collapses. Only the "icon" mode shows a rail where every list
 *  must be fully open; "offcanvas" slides the whole sidebar away, and its lists
 *  must keep their open/closed and Show more state while it is hidden. */
export const SIDEBAR_COLLAPSIBLE: "offcanvas" | "icon" = "offcanvas";
export const SIDEBAR_DEFAULT_WIDTH = 240;
/** Floor/ceiling for sidebar resize — pass the same values to useResizable. */
export const SIDEBAR_MIN_WIDTH = 200;
export const SIDEBAR_MAX_WIDTH = 420;
