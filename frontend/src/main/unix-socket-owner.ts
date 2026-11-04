import * as fs from "node:fs";
import * as net from "node:net";
import * as path from "node:path";

type Lstat = (p: string) => Pick<fs.Stats, "uid" | "isSocket" | "isSymbolicLink">;

/**
 * Refuses a Unix-socket address that is not ours. The daemon advertises sockets
 * reached through short `/tmp` symlinks; another local user could squat such a
 * name, so both the symlink and the socket must be owned by the current user
 * and the final entry must be a socket. Non-string (TCP) addresses and Windows
 * pipes are not covered. Returns the refusal reason, or null when acceptable.
 */
export function unixSocketOwnerError(
  address: unknown,
  uid: number | undefined = process.getuid?.(),
  lstat: Lstat = fs.lstatSync,
): string | null {
  if (typeof address !== "string" || uid === undefined || process.platform === "win32") return null;
  try {
    const dir = lstat(path.dirname(address));
    if (dir.isSymbolicLink() && dir.uid !== uid) return `socket alias ${path.dirname(address)} is not owned by the current user`;
    const sock = lstat(address);
    if (!sock.isSocket()) return `${address} is not a socket`;
    if (sock.uid !== uid) return `socket ${address} is not owned by the current user`;
  } catch (error) {
    return error instanceof Error ? error.message : String(error);
  }
  return null;
}

/** Connects like `connect`, but a refused or missing socket yields a socket that errors on the next tick. */
export function connectOwned(address: string, connect: (a: string) => net.Socket): net.Socket {
  const refusal = unixSocketOwnerError(address);
  if (refusal === null) return connect(address);
  const dead = new net.Socket();
  process.nextTick(() => dead.destroy(new Error(refusal)));
  return dead;
}
