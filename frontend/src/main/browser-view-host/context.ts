import type { BrowserViewHostOptions } from "./types";
import type { createHostPrelude } from "./prelude";
import type { createTabLifecycle } from "./tab-lifecycle";
import type { createSession } from "./session";
import type { createOperations } from "./operations";
import type { createAgentTabs } from "./agent-tabs";
import type { createUserTabs } from "./user-tabs";
import type { createShortcutWiring } from "./shortcut-wiring";
import type { createDevTools } from "./devtools";
import type { createBoundsNav } from "./bounds-nav";
import type { createDestroyProfile } from "./destroy-profile";
import type { createAnnotation } from "./annotation";
import type { createIpcHandlers } from "./ipc-handlers";
import type { createLiveStream } from "./live-stream";
import type { createAgentExecute } from "./agent-execute";

// Wiring rule: slices are created in order (see browser-view-host.ts) and share
// one `ctx.fns` object that each slice fills in as it is created. A slice may
// destructure functions of EARLIER slices while it is being created. Its own
// functions and those of later slices must be read lazily (`ctx.fns.x` inside a
// function body, at call time): they are still undefined at creation, and the
// type checker cannot tell. In development `createWiringFns` makes such an
// early read throw until `seal()` is called.
export type HostFns =
  ReturnType<typeof createTabLifecycle> &
  ReturnType<typeof createSession> &
  ReturnType<typeof createOperations> &
  ReturnType<typeof createAgentTabs> &
  ReturnType<typeof createUserTabs> &
  ReturnType<typeof createShortcutWiring> &
  ReturnType<typeof createDevTools> &
  ReturnType<typeof createBoundsNav> &
  ReturnType<typeof createDestroyProfile> &
  ReturnType<typeof createAnnotation> &
  ReturnType<typeof createIpcHandlers> &
  ReturnType<typeof createLiveStream> &
  ReturnType<typeof createAgentExecute>;

export type HostContext = { options: BrowserViewHostOptions } & ReturnType<typeof createHostPrelude> & {
  fns: HostFns;
};

/**
 * Dev-only guard for the rule above. While wiring, reading a function no slice
 * has registered yet throws; after `seal()` reads behave normally. In
 * production this returns a plain object and a no-op seal.
 */
export function createWiringFns(dev: boolean): { fns: HostFns; seal: () => void } {
  const target = {} as HostFns;
  if (!dev) return { fns: target, seal: () => {} };
  let wiring = true;
  const fns = new Proxy(target, {
    get(obj, key, receiver) {
      if (wiring && typeof key === "string" && !(key in obj)) {
        throw new Error(`browser-view-host: ctx.fns.${key} read before its slice was wired; read it lazily inside a function body`);
      }
      return Reflect.get(obj, key, receiver);
    },
  });
  return { fns, seal: () => { wiring = false; } };
}
