import { describe, expect, it } from "vitest";
import { readManagedFirstRunAccepted } from "./first-run-policy";

const here = new URL(import.meta.url).pathname;
const plist = (doc: unknown) => async () => JSON.stringify(doc);

describe("readManagedFirstRunAccepted", () => {
	it("is true when the managed file sets FirstRunNoticeAccepted", async () => {
		expect(await readManagedFirstRunAccepted("darwin", here, plist({ FirstRunNoticeAccepted: true }))).toBe(true);
	});
	it("is false when unset, wrong-typed, unreadable, missing or off macOS", async () => {
		expect(await readManagedFirstRunAccepted("darwin", here, plist({}))).toBe(false);
		expect(await readManagedFirstRunAccepted("darwin", here, plist({ FirstRunNoticeAccepted: "yes" }))).toBe(false);
		expect(await readManagedFirstRunAccepted("darwin", here, async () => "{")).toBe(false);
		expect(await readManagedFirstRunAccepted("darwin", `${here}.missing`, plist({ FirstRunNoticeAccepted: true }))).toBe(false);
		expect(await readManagedFirstRunAccepted("linux", here, plist({ FirstRunNoticeAccepted: true }))).toBe(false);
	});
});
