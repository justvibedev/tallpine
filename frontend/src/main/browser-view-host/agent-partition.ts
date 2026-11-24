import type { BrowserEntry, BrowserSessionEntry } from "./types";
import { temporaryBrowserPartition } from "./entry-state";

// Agent-created browser tabs must not inherit the logins a person imported or
// signed in to in a named profile. They use a throwaway partition unless the
// person grants the agent their logins for that worker session. A temporary
// session gets one too, so the agent's network is always the proxied one.

export type AgentLoginsConfirm = (sessionId: string, profileName: string) => Promise<boolean>;

export type AgentPartitionInput = {
  /** The session's partition, shared with the person's own tabs. */
  sessionPartition: string;
  /** The session's lazily created throwaway partition for agent tabs. */
  agentPartition: string | undefined;
  granted: boolean;
};

export function chooseAgentPartition(input: AgentPartitionInput): {
  partition: string;
  created: boolean;
} {
  if (input.granted) {
    return { partition: input.sessionPartition, created: false };
  }
  if (input.agentPartition) return { partition: input.agentPartition, created: false };
  return { partition: temporaryBrowserPartition(), created: true };
}

// The confirm is shown once per worker session and profile per app run (at most
// one pending dialog), the agent meanwhile works in the throwaway partition, and
// a later "yes" moves the agent onto a profile tab (needsProfileTab). Denial (or no
// confirm hook) is the default, so a restart never silently widens agent access.
// request returns the pending answer (settled after onGrant ran) so a command
// aimed at the person's own page can wait for it (see personPageInFront).
export class AgentLoginGrants {
  private readonly asked = new Set<string>();
  private readonly pending = new Map<string, Promise<void>>();

  constructor(private readonly confirm?: AgentLoginsConfirm) {}

  request(sessionId: string, profileId: string, profileName: string, onGrant: () => void): Promise<void> | undefined {
    const key = `${sessionId}\u0000${profileId}`;
    const pending = this.pending.get(key);
    if (pending) return pending;
    if (this.asked.has(key) || !this.confirm) return undefined;
    this.asked.add(key);
    const answer = this.confirm(sessionId, profileName)
      .then((granted) => {
        if (granted) onGrant();
      })
      .catch(() => undefined)
      .finally(() => this.pending.delete(key));
    this.pending.set(key, answer);
    return answer;
  }
}

// A person's own tab in a named profile may hold their logins; the agent works
// in a tab of its own instead. Viewer tabs (agentTab) show the agent's browser.
export function needsAgentTab(
  entry: BrowserEntry | undefined,
  session: BrowserSessionEntry,
): boolean {
  return (
    entry !== undefined &&
    entry.agentTab === undefined &&
    !entry.agentDriven &&
    // A temporary session has no logins to grant: its agent always moves to the
    // proxied agent partition, off the person's direct tabs.
    (session.profileId === null || session.agentLoginsGrantedProfileId !== session.profileId)
  );
}


// After a grant, an agent tab still in the throwaway partition must move into the
// profile, or the agent stays logged out on the tab it keeps driving.
export function needsProfileTab(
  entry: BrowserEntry | undefined,
  session: BrowserSessionEntry,
): boolean {
  return (
    entry !== undefined &&
    entry.agentDriven === true &&
    session.profileId !== null &&
    session.agentLoginsGrantedProfileId === session.profileId &&
    entry.partition !== session.profilePartition
  );
}

// The person opened a page of their own in a named profile and the agent may not
// drive it yet: the agent was most likely asked to read that very page, so its
// command waits for the logins answer instead of starting on a blank agent tab.
export function personPageInFront(
  entry: BrowserEntry | undefined,
  session: BrowserSessionEntry,
): boolean {
  return (
    session.profileId !== null &&
    needsAgentTab(entry, session) &&
    /^https?:/i.test(entry!.view.webContents.getURL())
  );
}
