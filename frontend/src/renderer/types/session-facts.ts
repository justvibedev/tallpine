/** The daemon's observed branch facts. `unpushed` equals `commits` until the branch reaches its remote. */
export type SessionBranchState = { commits: number; remoteBranch?: string; unpushed: number; head?: string; landed?: boolean };

/** A running background task of a session's agent. */
export type SessionBackgroundTask = { id: string; name: string; kind?: string; model?: string; startedAt?: string };
/** The session's current launch still runs a harness release an update has replaced. */
export type SessionStaleBinary = { version: string; since: string; switchFailedAt?: string };
