import { pinnedTlsOptions } from "../remote-tls";
import { createUpstreamResponders } from "./upstream-responders";
import { createUpstreamSession } from "./upstream-session";
import { request as httpRequest, type IncomingHttpHeaders, type OutgoingHttpHeaders } from "node:http";
import { request as httpsRequest } from "node:https";
import { type Socket } from "node:net";
import { PreviewTarget } from "./common";

export type ProxyCore = ReturnType<typeof createUpstreamSession> & ReturnType<typeof createUpstreamResponders> & {
	token: string;
	corsHeaders: Record<string, string>;
	pinned: ReturnType<typeof pinnedTlsOptions> | Record<string, never>;
	tunnels: Set<() => void>;
	upstream: URL;
	hostname: string;
	upstreamPort: number;
	upstreamRequest: typeof httpRequest | typeof httpsRequest;
	dialUpstream: (onReady: () => void) => Socket;
	stripToken: (rawUrl: string | undefined) => string | null;
	previewPath: (rawHost: string | undefined, rawUrl: string | undefined, method: string | undefined, scheme: "http" | "https") => { path: string; kind: PreviewTarget["kind"] } | null;
	forwardHeaders: (incoming: IncomingHttpHeaders, previewKind?: PreviewTarget["kind"]) => OutgoingHttpHeaders;
	validControlOrigin: (req: { headers: IncomingHttpHeaders; method?: string; url?: string }) => boolean;
	validPreviewOrigin: (scheme: "http" | "https", host: string | undefined, origin: string | undefined, kind?: PreviewTarget["kind"]) => boolean;
};
