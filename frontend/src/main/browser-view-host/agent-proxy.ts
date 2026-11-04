import type { Session } from "electron";
import { startRenderCheckProxy } from "../render-check-proxy";

// Agent tabs in their throwaway partition send every connection through the
// render-check resolver: it pins the addresses it resolves and refuses private,
// CGNAT, NAT64, 6to4, Teredo and this machine's own interfaces. That covers what
// the agent-URL check cannot see: page-initiated navigation, redirects,
// subresources and DNS. Loopback stays direct so the agent can open a dev server.
// "<-loopback>" first removes Chromium's implicit bypass, which also sends
// link-local (169.254/16, fe80::/10: cloud metadata, neighbours) direct; only the
// loopback entries after it are bypassed. Checked with session.resolveProxy.

type ProxySession = Pick<Session, "setProxy">;

const AGENT_BYPASS_RULES = "<-loopback>;localhost;*.localhost;127.0.0.1/8;[::1]";

const proxied = new WeakMap<ProxySession, Promise<void>>();

/**
 * Points `session` at the resolver once. The returned promise rejects when the
 * proxy cannot start, and the tab must then not load anything: failing open
 * would leave the agent's pages with direct network access.
 */
export function proxyAgentSession(session: ProxySession): Promise<void> {
  // The "agent" listener is never stopped: the session keeps using it for as long as its tabs live.
  let ready = proxied.get(session);
  if (!ready) {
    ready = startRenderCheckProxy("agent")
      .then((port) => session.setProxy({ proxyRules: `socks5://127.0.0.1:${port}`, proxyBypassRules: AGENT_BYPASS_RULES }))
      .catch((error: unknown) => {
        proxied.delete(session);
        throw error;
      });
    proxied.set(session, ready);
  }
  return ready;
}
