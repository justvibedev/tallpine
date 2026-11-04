import { describe, expect, it } from "vitest";
import { unixSocketOwnerError } from "./unix-socket-owner";

const stat = (uid: number, kind: "socket" | "symlink" | "file") => ({
  uid,
  isSocket: () => kind === "socket",
  isSymbolicLink: () => kind === "symlink",
});

describe("unixSocketOwnerError", () => {
  const lstat = (alias: ReturnType<typeof stat>, sock: ReturnType<typeof stat>) => (p: string) =>
    p.endsWith(".sock") ? sock : alias;

  it("accepts our own alias and socket", () => {
    expect(unixSocketOwnerError("/tmp/x-bs-1/a.sock", 501, lstat(stat(501, "symlink"), stat(501, "socket")))).toBeNull();
  });
  it("refuses a symlink alias owned by someone else", () => {
    expect(unixSocketOwnerError("/tmp/x-bs-1/a.sock", 501, lstat(stat(502, "symlink"), stat(501, "socket")))).toMatch(/alias/);
  });
  it("refuses a socket owned by someone else", () => {
    expect(unixSocketOwnerError("/tmp/x-bs-1/a.sock", 501, lstat(stat(501, "symlink"), stat(502, "socket")))).toMatch(/not owned/);
  });
  it("refuses a non-socket", () => {
    expect(unixSocketOwnerError("/tmp/x-bs-1/a.sock", 501, lstat(stat(501, "symlink"), stat(501, "file")))).toMatch(/not a socket/);
  });
});
