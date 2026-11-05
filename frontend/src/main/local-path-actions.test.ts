import { describe, expect, it, vi } from "vitest";
import { createLocalPathActions, resolveLocalPath } from "./local-path-actions";

type Entry = { kind: "file" | "directory" | "other"; mode?: number };

function setup(entries: Record<string, Entry>, platform: NodeJS.Platform = "darwin") {
	const deps = {
		platform,
		homeDir: "/Users/me",
		stat: vi.fn(async (target: string) => {
			const entry = entries[target];
			if (!entry) throw new Error("ENOENT");
			return { isFile: () => entry.kind === "file", isDirectory: () => entry.kind === "directory", mode: (entry.kind === "file" ? 0o100000 : 0) | (entry.mode ?? 0o644) };
		}),
		realPath: vi.fn(async (target: string) => target),
		revealPath: vi.fn(),
		openPath: vi.fn(async () => ""),
		copyText: vi.fn(),
		resolveArtifactDir: vi.fn(async (sessionId: string) => {
			if (sessionId === "gone") throw new Error("no artifacts");
			return sessionId === "moved" ? "/elsewhere/moved-artifacts" : `/data/artifacts/${sessionId}`;
		}),
		resolveWorkspace: vi.fn(async (sessionId: string) => `/data/worktrees/proj/${sessionId}`),
	};
	return { deps, actions: createLocalPathActions(deps) };
}

describe("resolveLocalPath", () => {
	it("expands ~ and normalizes", () => {
		expect(resolveLocalPath("~/a/../b", "/Users/me", "darwin")).toBe("/Users/me/b");
		expect(resolveLocalPath("/a//b/./c", "/Users/me", "linux")).toBe("/a/b/c");
	});
	it("rejects relative paths, URLs, NUL and non-strings", () => {
		for (const v of ["a/b", "./a", "file:///etc/passwd", "https://x/y", "tallpine://x", "/a\0b", "", "//host/share", 42, null, "~other/x"]) {
			expect(resolveLocalPath(v, "/Users/me", "darwin"), String(v)).toBeUndefined();
		}
	});
	it("accepts Windows drive paths only on win32", () => {
		expect(resolveLocalPath("C:\\Users\\me\\a.txt", "C:\\Users\\me", "win32")).toBe("C:\\Users\\me\\a.txt");
		expect(resolveLocalPath("C:\\a", "/Users/me", "darwin")).toBeUndefined();
	});
});

describe("local path actions", () => {
	it("opens regular files and reveals/copies existing paths", async () => {
		const { actions, deps } = setup({ "/tmp/a.txt": { kind: "file" } });
		expect(await actions.perform("/tmp/a.txt", "open")).toEqual({ ok: true, performed: "open" });
		expect(deps.openPath).toHaveBeenCalledWith("/tmp/a.txt");
		expect(await actions.perform("/tmp/a.txt", "reveal")).toEqual({ ok: true, performed: "reveal" });
		expect(deps.revealPath).toHaveBeenCalledWith("/tmp/a.txt");
		expect(await actions.perform("/tmp/a.txt", "copy")).toEqual({ ok: true, performed: "copy" });
		expect(deps.copyText).toHaveBeenCalledWith("/tmp/a.txt");
	});
	it("copies the expanded path", async () => {
		const { actions, deps } = setup({ "/Users/me/x": { kind: "directory" } });
		await actions.perform("~/x", "copy");
		expect(deps.copyText).toHaveBeenCalledWith("/Users/me/x");
	});
	it("opens exactly the allowlisted document types and reveals everything else", async () => {
		const open = ["a.html", "a.htm", "a.json", "a.md", "a.txt", "a.csv", "a.log", "a.pdf", "a.png", "a.jpg", "a.jpeg", "a.gif", "a.webp", "a.svg", "a.mp4", "a.MOV"];
		const reveal = ["a.zip", "a.dmg", "a.pkg", "a.terminal", "a.docx", "a.js"];
		const { actions } = setup(Object.fromEntries([...open, ...reveal].map((n) => [`/tmp/${n}`, { kind: "file" as const }])));
		for (const n of open) expect(await actions.perform(`/tmp/${n}`, "open"), n).toEqual({ ok: true, performed: "open" });
		for (const n of reveal) expect(await actions.perform(`/tmp/${n}`, "open"), n).toEqual({ ok: true, performed: "reveal" });
	});
	it("never opens executables; falls back to reveal", async () => {
		const { actions, deps } = setup({
			"/Applications/X.app": { kind: "directory" },
			"/tmp/run.command": { kind: "file" },
			"/tmp/run.sh": { kind: "file" },
			"/tmp/tool": { kind: "file", mode: 0o755 },
			"/tmp/setup.py": { kind: "file" },
			"/tmp/a.jnlp": { kind: "file" },
			"/tmp/a.hta": { kind: "file" },
			"/tmp/noext": { kind: "file" },
			"/tmp/script.txt": { kind: "file", mode: 0o755 },
		});
		for (const target of ["/Applications/X.app", "/tmp/run.command", "/tmp/run.sh", "/tmp/tool", "/tmp/setup.py", "/tmp/a.jnlp", "/tmp/a.hta", "/tmp/noext", "/tmp/script.txt"]) {
			expect(await actions.perform(target, "open")).toEqual({ ok: true, performed: "reveal" });
		}
		expect(deps.openPath).not.toHaveBeenCalled();
	});
	it("does not open a symlink whose target is an executable", async () => {
		const { actions, deps } = setup({ "/tmp/link.txt": { kind: "file" } });
		deps.realPath.mockResolvedValue("/tmp/evil.command");
		expect(await actions.perform("/tmp/link.txt", "open")).toEqual({ ok: true, performed: "reveal" });
	});
	it("does not open special files", async () => {
		const { actions } = setup({ "/tmp/sock": { kind: "other" } });
		expect(await actions.perform("/tmp/sock", "open")).toEqual({ ok: true, performed: "reveal" });
	});
	it("rejects missing, invalid paths and unknown actions", async () => {
		const { actions, deps } = setup({ "/tmp/a": { kind: "file" } });
		expect(await actions.perform("/tmp/missing", "reveal")).toEqual({ ok: false, reason: "not_found" });
		expect(await actions.perform("file:///tmp/a", "reveal")).toEqual({ ok: false, reason: "invalid" });
		expect(await actions.perform("/tmp/a", "exec")).toEqual({ ok: false, reason: "invalid" });
		expect(deps.revealPath).not.toHaveBeenCalled();
	});
	it("reports a failed open", async () => {
		const { actions, deps } = setup({ "/tmp/a.txt": { kind: "file" } });
		deps.openPath.mockResolvedValue("no app");
		expect(await actions.perform("/tmp/a.txt", "open")).toEqual({ ok: false, reason: "failed" });
	});
});

describe("tallpine:// artifact links", () => {
	const link = (path: string, session = "s1") => `tallpine://sessions/p/${session}/artifacts/${path}`;
	it("resolves into the session artifact dir, percent-decoding segments, and reveals/opens/copies like a local path", async () => {
		const { actions, deps } = setup({ "/data/artifacts/s1/plans/my plan.md": { kind: "file" }, "/data/artifacts/s1/run.sh": { kind: "file" } });
		const plan = link("plans/my%20plan.md");
		expect(await actions.inspect(plan)).toEqual({ ok: true, path: "/data/artifacts/s1/plans/my plan.md", kind: "file", canOpen: true });
		expect(await actions.perform(plan, "reveal")).toEqual({ ok: true, performed: "reveal" });
		expect(deps.revealPath).toHaveBeenCalledWith("/data/artifacts/s1/plans/my plan.md");
		expect(await actions.perform(plan, "copy")).toEqual({ ok: true, performed: "copy" });
		expect(deps.copyText).toHaveBeenCalledWith("/data/artifacts/s1/plans/my plan.md");
		expect(await actions.perform(link("run.sh"), "open")).toEqual({ ok: true, performed: "reveal" });
	});
	it("resolves tallpine:// artifact links the same as tallpine://", async () => {
		const { actions } = setup({ "/data/artifacts/s1/plan.md": { kind: "file" } });
		expect(await actions.inspect("tallpine://sessions/p/s1/artifacts/plan.md")).toEqual({ ok: true, path: "/data/artifacts/s1/plan.md", kind: "file", canOpen: true });
		expect(await actions.inspect("tallpine://sessions/p/s1/artifacts/../x")).toEqual({ ok: false, reason: "invalid" });
	});
	it("rejects traversal, encoded traversal and symlinks that leave the artifact dir", async () => {
		const { actions, deps } = setup({ "/data/artifacts/s1/a.txt": { kind: "file" }, "/data/artifacts/s1/out.txt": { kind: "file" } });
		for (const bad of [link("../s2/a.txt"), link("%2e%2e/s2/a.txt"), link("..%2Fs2%2Fa.txt"), link("a/../../x"), link("a%5C..%5Cx")]) {
			expect(await actions.inspect(bad), bad).toEqual({ ok: false, reason: "invalid" });
		}
		deps.realPath.mockImplementation(async (t: string) => (t.endsWith("out.txt") ? "/etc/passwd" : t));
		expect(await actions.inspect(link("out.txt"))).toEqual({ ok: false, reason: "invalid" });
		expect(deps.revealPath).not.toHaveBeenCalled();
	});
	it("reports missing files, unknown sessions and non-artifact tallpine:// forms", async () => {
		const { actions } = setup({});
		expect(await actions.inspect(link("nope.txt"))).toEqual({ ok: false, reason: "not_found" });
		expect(await actions.inspect(link("a.txt", "gone"))).toEqual({ ok: false, reason: "not_found" });
		for (const v of ["tallpine://sessions/p/s1", "tallpine://other/x", "tallpine://sessions/p/s1/kill", "tallpine://sessions/p/s1/artifacts/"]) {
			expect(await actions.inspect(v), v).toEqual({ ok: false, reason: "invalid" });
		}
	});
});

describe("inspect image source for a chat session", () => {
	const file: Entry = { kind: "file" };
	const image = async (target: string, sessionId: unknown = "s-1") => {
		const { actions } = setup({ [target]: file, "/data/artifacts/s-1/dir.png": { kind: "directory" } });
		const inspection = await actions.inspect(target, sessionId);
		return inspection.ok ? inspection.image : inspection.reason;
	};

	it("maps a file inside the session's real worktree or artifact directory", async () => {
		expect(await image("/data/worktrees/proj/s-1/docs/shot.png")).toEqual({ kind: "workspace", path: "docs/shot.png" });
		// A worktree folder that happens to be named artifacts/<x> is still the worktree.
		expect(await image("/data/worktrees/proj/s-1/artifacts/x/a.png")).toEqual({ kind: "workspace", path: "artifacts/x/a.png" });
		expect(await image("/data/artifacts/s-1/a light.png")).toEqual({ kind: "artifact", sessionId: "s-1", path: "a light.png" });
	});

	it("maps another session's artifact only when it sits inside that session's own directory", async () => {
		expect(await image("/data/artifacts/s-2/shots/one.webp")).toEqual({ kind: "artifact", sessionId: "s-2", path: "shots/one.webp" });
		expect(await image("/data/artifacts/moved/one.png")).toBeUndefined();
		expect(await image("/data/artifacts/gone/one.png")).toBeUndefined();
	});

	it("maps nothing for a same-named segment outside the real roots", async () => {
		expect(await image("/Users/me/proj/artifacts/foo/logo.png")).toBeUndefined();
		expect(await image("/tmp/s-1/shot.png")).toBeUndefined();
		expect(await image("/data/worktrees/proj/s-2/docs/shot.png")).toBeUndefined();
		expect(await image("/data/worktrees/proj/s-1-old/shot.png")).toBeUndefined();
	});

	it("maps only image files, and only when asked for a session", async () => {
		expect(await image("/data/artifacts/s-1/notes.md")).toBeUndefined();
		expect(await image("/data/artifacts/s-1/a.png", null)).toBeUndefined();
		expect(await image("/data/artifacts/s-1/a.png", "../s-1")).toBeUndefined();
		expect(await image("/data/artifacts/s-1/dir.png")).toBeUndefined();
		const { actions } = setup({});
		expect(await actions.inspect("/data/artifacts/s-1/missing.png", "s-1")).toEqual({ ok: false, reason: "not_found" });
	});
});
