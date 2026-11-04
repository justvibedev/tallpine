import { describe, expect, it } from "vitest";
import { contentSecurityPolicy, rendererCspHeader } from "./renderer-csp";

const directive = (policy: string, name: string) => policy.split("; ").find((d) => d.startsWith(`${name} `) || d === name);

describe("renderer CSP", () => {
	it("header is strict: no unsafe-eval, no inline scripts, no objects, no base, no framing", () => {
		const header = rendererCspHeader();
		expect(directive(header, "script-src")).toBe("script-src 'self'");
		expect(directive(header, "object-src")).toBe("object-src 'none'");
		expect(directive(header, "base-uri")).toBe("base-uri 'none'");
		expect(directive(header, "frame-ancestors")).toBe("frame-ancestors 'none'");
		expect(directive(header, "default-src")).toBe("default-src 'self'");
		expect(header).not.toContain("unsafe-eval");
	});

	it("keeps daemon connectivity for the shipped renderer", () => {
		const connect = directive(rendererCspHeader(), "connect-src") ?? "";
		expect(connect).toContain("http://127.0.0.1:*");
		expect(connect).toContain("ws://127.0.0.1:*");
	});

	it("serve mode only relaxes script-src for the dev preamble", () => {
		expect(directive(contentSecurityPolicy("serve"), "script-src")).toContain("'unsafe-inline'");
		expect(directive(contentSecurityPolicy("build"), "script-src")).not.toContain("unsafe-inline");
	});

	it("allows no remote image origin, so a rendered <img> cannot beacon out", () => {
		for (const mode of ["build", "serve"] as const) {
			const img = directive(contentSecurityPolicy(mode), "img-src") ?? "";
			expect(img).not.toMatch(/(^|\s)https?:(\s|$)/);
			expect(img.split(" ").filter((src) => /^https?:\/\//.test(src) && !/^http:\/\/(127\.0\.0\.1|\*\.localhost):\*$/.test(src))).toEqual([]);
			expect(img).toContain("data:");
		}
	});
});
