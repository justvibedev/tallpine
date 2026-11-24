// Console, signal and network-capture records kept per browser tab.

export type BrowserLogEntry = {
  level: string;
  message: string;
  source?: string;
  line?: number;
  timestamp: string;
};

// Text only — never a request/response body, matching the existing on-demand
// network capture's privacy stance (see MAX_NETWORK_REQUESTS in constants.ts).
export type BrowserSignalEntry = {
  kind: "console-error" | "network-failure";
  message: string;
  timestamp: string;
};

export type BrowserNetworkRequest = {
  id: string;
  method: string;
  url: string;
  resourceType?: string;
  startedAt: string;
  status?: number;
  statusText?: string;
  mimeType?: string;
  durationMs?: number;
  failed?: boolean;
  canceled?: boolean;
  errorText?: string;
  fromCache?: boolean;
  fromServiceWorker?: boolean;
  redirectedTo?: string;
  requestHeaders?: Record<string, string>;
  responseHeaders?: Record<string, string>;
};

export type InternalBrowserNetworkRequest = BrowserNetworkRequest & {
  protocolRequestId: string;
  startedMonotonic?: number;
};

export type BrowserNetworkCapture = {
  active: boolean;
  tabId: string;
  startedAt: string;
  expiresAt: string;
  stoppedAt?: string;
  stopReason?: string;
  maxEntries: number;
  nextSequence: number;
  requests: InternalBrowserNetworkRequest[];
  byRequestId: Map<string, InternalBrowserNetworkRequest>;
  timer?: ReturnType<typeof setTimeout>;
};
