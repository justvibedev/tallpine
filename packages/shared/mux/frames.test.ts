import { describe, expect, it } from "vitest";
import { base64ToBytes, bytesToBase64, closeFrame, dataFrame, openFrame, pingFrame, resizeFrame, subscribeFrame } from "./frames";

const parse = (s: string) => JSON.parse(s);

describe("mux frames", () => {
	it("open carries only the fields given", () => {
		expect(parse(openFrame("s1", { cols: 80, rows: 24 }))).toEqual({ ch: "terminal", type: "open", id: "s1", cols: 80, rows: 24 });
		expect(parse(openFrame("s1", { projectId: "p", role: "secondary" }))).toEqual({ ch: "terminal", type: "open", id: "s1", projectId: "p", role: "secondary" });
		expect(parse(openFrame("s1"))).toEqual({ ch: "terminal", type: "open", id: "s1" });
	});

	it("resize adds force only when set, and projectId when given", () => {
		expect(parse(resizeFrame("s", 100, 30))).toEqual({ ch: "terminal", type: "resize", id: "s", cols: 100, rows: 30 });
		expect(parse(resizeFrame("s", 1, 2, { force: true, projectId: "p" }))).toEqual({ ch: "terminal", type: "resize", id: "s", cols: 1, rows: 2, force: true, projectId: "p" });
	});

	it("close and ping and subscribe", () => {
		expect(parse(closeFrame("s", { projectId: "p" }))).toEqual({ ch: "terminal", type: "close", id: "s", projectId: "p" });
		expect(parse(closeFrame("s"))).toEqual({ ch: "terminal", type: "close", id: "s" });
		expect(parse(pingFrame())).toEqual({ ch: "system", type: "ping" });
		expect(parse(subscribeFrame(["sessions"]))).toEqual({ ch: "subscribe", topics: ["sessions"] });
	});

	it("data base64-encodes the bytes and round-trips", () => {
		const bytes = new Uint8Array([0, 1, 2, 250, 255, 27, 91]);
		const frame = parse(dataFrame("s", bytes, { projectId: "p" }));
		expect(frame).toMatchObject({ ch: "terminal", type: "data", id: "s", projectId: "p" });
		expect(Array.from(base64ToBytes(frame.data))).toEqual(Array.from(bytes));
	});

	it("round-trips large and empty payloads and pads like Go StdEncoding", () => {
		const big = new Uint8Array(100_000).map((_, i) => i % 256);
		expect(Array.from(base64ToBytes(bytesToBase64(big)))).toEqual(Array.from(big));
		expect(bytesToBase64(new Uint8Array(0))).toBe("");
		expect(bytesToBase64(new Uint8Array([1]))).toBe("AQ==");
		expect(bytesToBase64(new Uint8Array([1, 2]))).toBe("AQI=");
	});

	it("rejects malformed base64", () => {
		expect(() => base64ToBytes("!!")).toThrow();
	});
});
