import type { BrowserRect } from "./types";

export type BrowserDevToolsState = {
  viewId: string;
  open: boolean;
  activeTabId: string;
  placement?: BrowserDevToolsPlacement;
};

export type BrowserDevToolsPlacement = "right" | "bottom" | "left" | "undocked";

export type BrowserDevToolsInput = {
  viewId: string;
  operation: "open" | "close" | "setPlacement";
  placement?: BrowserDevToolsPlacement;
};

export type InternalBrowserDevToolsOperation =
  | BrowserDevToolsInput["operation"]
  | "toggle";

export type BrowserBoundsInput = {
  viewId: string;
  revision: number;
  rect: BrowserRect;
  visible: boolean;
};

export type BrowserNavigateInput = {
  viewId: string;
  url: string;
};

export type BrowserHistorySuggestInput = {
  viewId: string;
  query: string;
};

export type BrowserHistoryFaviconInput = {
  viewId: string;
  url: string;
};

export type BrowserTabInput = {
  viewId: string;
  tabId: string;
};

export type BrowserOpenTabInput = {
  viewId: string;
  url?: string;
};
