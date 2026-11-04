import { describe, expect, it } from "vitest";
import { createWiringFns } from "./context";

describe("createWiringFns", () => {
  it("throws on an early read of a slice function while wiring, then allows reads after seal", () => {
    const { fns, seal } = createWiringFns(true);
    Object.assign(fns, { early: () => 1 });
    expect((fns as unknown as Record<string, () => number>).early()).toBe(1);
    expect(() => (fns as unknown as Record<string, unknown>).late).toThrow(/read before its slice was wired/);
    Object.assign(fns, { late: () => 2 });
    seal();
    expect((fns as unknown as Record<string, () => number>).late()).toBe(2);
    expect((fns as unknown as Record<string, unknown>).missing).toBeUndefined();
  });

  it("is a plain object outside development", () => {
    const { fns } = createWiringFns(false);
    expect((fns as unknown as Record<string, unknown>).anything).toBeUndefined();
  });
});
