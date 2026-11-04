import { expect, test, type CDPSession, type Page } from "@playwright/test";
import { mkdir, writeFile } from "node:fs/promises";
import path from "node:path";
import { installFakeAgent } from "./support/fake-bridge";

// Opt-in renderer load benchmark: 15 projects, 30 live chat sessions, a stream
// of CDC session events plus conversation deltas. It reports render counts per
// component (via the React DevTools commit hook), scripting time, and heap, so a
// renderer change can be compared before/after on the same workload.
//
//   TALLPINE_PERF_BENCH=1 TALLPINE_PERF_LABEL=before TALLPINE_PERF_DIR=/tmp/perf npx playwright test renderer-load
//
// Knobs: TALLPINE_PERF_SECONDS (default 20), TALLPINE_PERF_EVENTS_PER_SECOND (60),
// TALLPINE_PERF_COUNT_RENDERS=0 (time without the commit-hook overhead),
// TALLPINE_PERF_PROFILE=1 (CPU profile summary next to the measurements),
// TALLPINE_PERF_CHANNEL=chrome (use an installed Chrome instead of Playwright's).
// Run before/after interleaved: the numbers move with machine load.
// Timings are evidence, not hardware-independent assertions.

const PROJECTS = 15;
const WORKERS_PER_PROJECT = 2;
const CHAT_ID = "p0-w0";

test.use({
	viewport: { width: 1440, height: 1000 },
	channel: process.env.TALLPINE_PERF_CHANNEL || undefined,
});

test.describe("renderer load (15 projects, 30 live sessions)", () => {
	test.skip(process.env.TALLPINE_PERF_BENCH !== "1", "Opt-in benchmark");

	// "soak" opens every chat session in turn (as a user switching between agents)
	// while events stream, to show what the renderer retains over time.
	for (const workload of ["board", "chat", "typing", "soak"] as const) {
		test(workload, async ({ page }, testInfo) => {
			test.setTimeout(10 * 60_000);
			const durationMs = Number(process.env.TALLPINE_PERF_SECONDS ?? 20) * 1000;
			const eventsPerSecond = Number(process.env.TALLPINE_PERF_EVENTS_PER_SECOND ?? 60);
			await installLoad(page, process.env.TALLPINE_PERF_COUNT_RENDERS !== "0");
			await page.goto(workload === "board" ? "/#/projects/p0" : `/#/projects/p0/sessions/${CHAT_ID}`);
			if (workload === "board") await expect(page.getByTestId("board")).toBeVisible();
			else await expect(page.getByRole("log", { name: "Conversation" })).toBeVisible();
			await page.waitForTimeout(1500);

			const cdp = await page.context().newCDPSession(page);
			await cdp.send("Performance.enable");
			await settle(cdp);
			const before = await metrics(cdp);
			await page.evaluate(() => window.__tallpineRenders?.reset());
			const profiling = process.env.TALLPINE_PERF_PROFILE === "1";
			if (profiling) {
				await cdp.send("Profiler.enable");
				await cdp.send("Profiler.setSamplingInterval", { interval: 200 });
				await cdp.send("Profiler.start");
			}
			await page.evaluate(
				([rate, ms]) => window.__tallpineLoad.start({ eventsPerSecond: rate, durationMs: ms, streamId: "p0-w0" }),
				[eventsPerSecond, durationMs] as const,
			);
			const started = Date.now();
			let typed = 0;
			if (workload === "typing") {
				const composer = page.locator('[contenteditable="true"]').last();
				await composer.click();
				const text = "the quick brown fox jumps over the lazy dog ".repeat(4);
				const startedTyping = Date.now();
				while (Date.now() - started < durationMs) {
					await page.keyboard.type(text, { delay: 15 });
					typed += text.length;
				}
				void startedTyping;
			} else if (workload === "soak") {
				const heapSamples: number[] = [];
				let visit = 0;
				while (Date.now() - started < durationMs) {
					const project = visit % PROJECTS;
					const worker = Math.floor(visit / PROJECTS) % WORKERS_PER_PROJECT;
					await page.evaluate((hash) => { location.hash = hash; }, `#/projects/p${project}/sessions/p${project}-w${worker}`);
					visit += 1;
					await page.waitForTimeout(3000);
					if (visit % 20 === 0) {
						await settle(cdp);
						heapSamples.push(Number(((await metrics(cdp)).JSHeapUsedSize / 1e6).toFixed(1)));
					}
				}
				console.log("SOAK heap MB every 20 visits:", heapSamples.join(" "));
			} else {
				await page.waitForTimeout(durationMs);
			}
			const elapsedS = (Date.now() - started) / 1000;
			const after = await metrics(cdp);
			if (profiling) await writeProfileSummary(cdp, `${process.env.TALLPINE_PERF_LABEL ?? "sample"}-${workload}-${testInfo.repeatEachIndex}`);
			const renders = await page.evaluate(() => window.__tallpineRenders?.snapshot() ?? null);
			await settle(cdp);
			const heap = (await metrics(cdp)).JSHeapUsedSize;
			const delta = (key: string) => (after[key] ?? 0) - (before[key] ?? 0);
			const top = renders
				? Object.entries(renders.byComponent)
						.sort((a, b) => b[1] - a[1])
						.slice(0, 400)
				: [];
			const record = {
				label: process.env.TALLPINE_PERF_LABEL ?? "sample",
				workload,
				eventsPerSecond,
				elapsedS,
				typedChars: typed,
				scriptingMsPerS: (delta("ScriptDuration") * 1000) / elapsedS,
				taskMsPerS: (delta("TaskDuration") * 1000) / elapsedS,
				layoutMsPerS: (delta("LayoutDuration") * 1000) / elapsedS,
				recalcStyleMsPerS: (delta("RecalcStyleDuration") * 1000) / elapsedS,
				commitsPerS: renders ? renders.commits / elapsedS : null,
				rendersPerS: renders ? renders.total / elapsedS : null,
				heapMbBefore: before.JSHeapUsedSize / 1e6,
				heapMbAfterGc: heap / 1e6,
				domNodes: after.Nodes,
				eventsSent: await page.evaluate(() => window.__tallpineLoad.sent()),
				// Cached queries per family (the dev build exposes the client as __qc).
				cachedQueries: await page.evaluate(() => {
					const client = (window as unknown as { __qc?: { getQueryCache: () => { getAll: () => Array<{ queryKey: unknown[] }> } } }).__qc;
					const counts: Record<string, number> = {};
					for (const query of client?.getQueryCache().getAll() ?? []) counts[String(query.queryKey[0])] = (counts[String(query.queryKey[0])] ?? 0) + 1;
					return Object.fromEntries(Object.entries(counts).sort((a, b) => b[1] - a[1]).slice(0, 8));
				}),
				topComponentsPerSecond: top.map(([name, n]) => [name, Number((n / elapsedS).toFixed(2))]),
			};
			console.log(JSON.stringify({ ...record, topComponentsPerSecond: undefined }));
			console.log(record.topComponentsPerSecond.map(([name, n]) => `${n}/s ${name}`).join("\n"));
			await testInfo.attach("measurements", { body: JSON.stringify(record, null, 2), contentType: "application/json" });
			const output = process.env.TALLPINE_PERF_DIR;
			if (output) {
				await mkdir(output, { recursive: true });
				await writeFile(path.join(output, `${record.label}-${workload}-${testInfo.repeatEachIndex}.json`), JSON.stringify(record, null, 2) + "\n");
			}
		});
	}
});

// Self time per function from a CPU profile, written next to the measurements.
async function writeProfileSummary(cdp: CDPSession, name: string) {
	const { profile } = await cdp.send("Profiler.stop");
	const self = new Map<number, number>();
	profile.samples?.forEach((id, index) => self.set(id, (self.get(id) ?? 0) + (profile.timeDeltas?.[index] ?? 0)));
	const byFunction = new Map<string, number>();
	for (const node of profile.nodes) {
		const { functionName, url, lineNumber } = node.callFrame;
		const key = `${functionName || "(anonymous)"} ${url.replace(/^.*\/(src|node_modules)\//, "$1/")}:${lineNumber + 1}`;
		byFunction.set(key, (byFunction.get(key) ?? 0) + (self.get(node.id) ?? 0) / 1000);
	}
	const top = [...byFunction.entries()].sort((a, b) => b[1] - a[1]).slice(0, 40);
	// Inclusive time per app function: self plus everything it called.
	const nodes = new Map(profile.nodes.map((node) => [node.id, node]));
	const total = new Map<number, number>();
	const totalOf = (id: number): number => {
		const cached = total.get(id);
		if (cached !== undefined) return cached;
		const node = nodes.get(id)!;
		const sum = (self.get(id) ?? 0) / 1000 + (node.children ?? []).reduce((acc, child) => acc + totalOf(child), 0);
		total.set(id, sum);
		return sum;
	};
	const inclusive = new Map<string, number>();
	for (const node of profile.nodes) {
		const { functionName, url, lineNumber } = node.callFrame;
		if (!url.includes("/src/renderer/")) continue;
		const key = `${functionName || "(anonymous)"} ${url.replace(/^.*\/src\//, "src/")}:${lineNumber + 1}`;
		inclusive.set(key, Math.max(inclusive.get(key) ?? 0, totalOf(node.id)));
	}
	const topInclusive = [...inclusive.entries()].sort((a, b) => b[1] - a[1]).slice(0, 40);
	const output = process.env.TALLPINE_PERF_DIR;
	if (output) {
		await mkdir(output, { recursive: true });
		await writeFile(path.join(output, `${name}-profile.txt`), top.map(([key, ms]) => `${ms.toFixed(0)}ms ${key}`).join("\n") + "\n\n== inclusive (app code) ==\n" + topInclusive.map(([key, ms]) => `${ms.toFixed(0)}ms ${key}`).join("\n") + "\n");
	}
}

async function settle(cdp: CDPSession) {
	await cdp.send("HeapProfiler.collectGarbage");
}

async function metrics(cdp: CDPSession): Promise<Record<string, number>> {
	const { metrics } = await cdp.send("Performance.getMetrics");
	return Object.fromEntries(metrics.map((metric) => [metric.name, metric.value]));
}

declare global {
	interface Window {
		__tallpineRenders?: { reset: () => void; snapshot: () => { total: number; commits: number; byComponent: Record<string, number> } };
		__tallpineLoad: {
			start: (opts: { eventsPerSecond: number; durationMs: number; streamId: string }) => void;
			sent: () => number;
		};
	}
}

async function installLoad(page: Page, countRenders: boolean) {
	await installFakeAgent(page, { workers: [] });
	await page.addInitScript(
		({ projects, workersPer, countRenders }) => {
			if (countRenders) {
				// Minimal React DevTools hook: after every commit, count the function
				// components that actually ran (PerformedWork) by display name.
				const byComponent: Record<string, number> = {};
				let total = 0;
				let commits = 0;
				const PerformedWork = 1;
				const nameOf = (type: unknown): string => {
					if (typeof type === "function") return (type as { displayName?: string; name?: string }).displayName || (type as { name?: string }).name || "Anonymous";
					if (type && typeof type === "object") {
						const t = type as { type?: unknown; render?: unknown; displayName?: string };
						return t.displayName || nameOf(t.type ?? t.render);
					}
					return "Unknown";
				};
				type Fiber = { tag: number; flags: number; type: unknown; child: Fiber | null; sibling: Fiber | null; alternate: Fiber | null };
				const walkChildren = (fiber: Fiber) => {
					for (let child = fiber.child; child; child = child.sibling) visit(child);
				};
				const visit = (fiber: Fiber) => {
					const prev = fiber.alternate;
					const rendered = fiber.tag === 0 || fiber.tag === 1 || fiber.tag === 11 || fiber.tag === 15;
					if (rendered && (!prev || fiber.flags & PerformedWork)) {
						const stack = (fiber as unknown as { _debugStack?: { stack?: string } })._debugStack?.stack;
						const site = stack?.match(/src\/renderer\/([^:)?]+)[^:)]*:(\d+)/);
						const name = `${nameOf(fiber.type)}${site ? ` @${site[1]}:${site[2]}` : ""}`;
						byComponent[name] = (byComponent[name] ?? 0) + 1;
						total += 1;
					}
					if (!prev || fiber.child !== prev.child) walkChildren(fiber);
				};
				(window as unknown as Record<string, unknown>).__REACT_DEVTOOLS_GLOBAL_HOOK__ = {
					supportsFiber: true,
					renderers: new Map(),
					inject: () => 1,
					checkDCE: () => undefined,
					onCommitFiberUnmount: () => undefined,
					onPostCommitFiberRoot: () => undefined,
					onCommitFiberRoot: (_id: number, root: { current: Fiber }) => {
						commits += 1;
						visit(root.current);
					},
				};
				window.__tallpineRenders = {
					reset: () => {
						for (const key of Object.keys(byComponent)) delete byComponent[key];
						total = 0;
						commits = 0;
					},
					snapshot: () => ({ total, commits, byComponent: { ...byComponent } }),
				};
			}

			// A busy desktop keeps its projects open: render every session row.
			try {
				localStorage.setItem("ao.sidebar.expanded-projects", JSON.stringify(Array.from({ length: projects }, (_, p) => `p${p}`)));
			} catch { /* storage unavailable */ }
			// ---- synthetic data: `projects` projects, `workersPer` chat workers each ----
			const now = new Date().toISOString();
			type Sess = Record<string, any>;
			const sessions: Sess[] = [];
			const text: Record<string, string> = {};
			const turnCount: Record<string, number> = {};
			const workspaces = Array.from({ length: projects }, (_, p) => {
				const id = `p${p}`;
				// Distinct creation times, as real sessions have (the sidebar sorts on them).
				const created = (n: number) => new Date(Date.parse(now) - (p * 10 + n) * 60_000).toISOString();
				let made = 0;
				const make = (sid: string, kind: string, title: string): Sess => ({
					id: sid,
					workspaceId: id,
					workspaceName: `project-${p}`,
					title,
					provider: "claude-code",
					kind,
					mode: "chat",
					branch: `session/${sid}`,
					status: "working",
					kanbanColumn: "building",
					createdAt: created(made++),
					updatedAt: now,
					activity: { state: "active", lastActivityAt: now },
					prs: [],
				});
				const list = [make(`${id}-orch`, "orchestrator", "Orchestrator")];
				for (let w = 0; w < workersPer; w++) list.push(make(`${id}-w${w}`, "worker", `Task ${p}.${w}: implement feature`));
				sessions.push(...list);
				return {
					id,
					name: `project-${p}`,
					kind: "single_repo",
					path: `/repos/project-${p}`,
					type: "main",
					orchestratorAgent: "claude-code",
					sessions: list,
				};
			});
			for (const s of sessions) {
				text[s.id as string] = "Working on it. ";
				turnCount[s.id as string] = 3;
			}
			const agent = (window as unknown as { __tallpineFakeAgent: { snapshot: () => unknown } }).__tallpineFakeAgent;
			agent.snapshot = () => JSON.parse(JSON.stringify(workspaces));

			const view = (s: Sess) => ({
				id: s.id,
				projectId: s.workspaceId,
				displayName: s.title,
				harness: s.provider,
				kind: s.kind,
				mode: s.mode,
				branch: s.branch,
				status: s.status,
				statusReadiness: "ready",
				displayStatus: "Working",
				kanbanColumn: s.kanbanColumn,
				createdAt: s.createdAt,
				updatedAt: s.updatedAt,
				lastEventAt: s.updatedAt,
				activity: s.activity,
				isTerminated: false,
				isPinned: false,
				outputType: "none",
				prs: [],
				autoInjectCI: true,
				autoInjectReview: true,
				autoReviewEnabled: false,
				chatProviderPreserved: false,
				terminateOnPrMerge: false,
			});
			const conversation = (s: Sess) => {
				const n = turnCount[s.id as string];
				const turns: unknown[] = [];
				const messages: unknown[] = [];
				for (let i = 0; i < n; i++) {
					const turnId = `turn-${i}`;
					const last = i === n - 1;
					turns.push({ id: turnId, state: last ? "running" : "completed", requestedAt: now, startedAt: now, ...(last ? {} : { completedAt: now }) });
					messages.push({
						kind: "message", id: `u-${i}`, turnId, sequence: i * 2 + 1, revision: 0, role: "user", origin: "human",
						text: `Please continue with step ${i + 1}.`, streaming: false, createdAt: now,
					});
					messages.push({
						kind: "message", id: `a-${i}`, turnId, sequence: i * 2 + 2, revision: last ? text[s.id as string].length : 0, role: "assistant", origin: "provider",
						text: last ? text[s.id as string] : "Here is a **summary** of the change with `code` and a list:\n\n- first item\n- second item\n\n" + "More detail follows. ".repeat(8),
						streaming: last, createdAt: now,
					});
				}
				return {
					conversationId: `conv-${s.id}`, sessionId: s.id, harness: "claude-code", mode: "chat", controller: "ready",
					latestSequence: n * 2 + text[s.id as string].length, oldestSequence: 1, hasMoreBefore: false,
					turns, messages, activities: [], settings: {},
				};
			};
			const json = (body: unknown, status = 200) =>
				new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
			const realFetch = window.fetch.bind(window);
			window.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
				const raw = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
				const url = new URL(raw, location.href);
				if (!url.pathname.startsWith("/api/v1/")) return realFetch(input, init);
				const match = url.pathname.match(/^\/api\/v1\/sessions\/([^/]+)(\/.*)?$/);
				const s = match ? sessions.find((x) => x.id === match[1]) : undefined;
				if (s) {
					const rest = match![2] ?? "";
					if (rest === "") return json({ session: view(s) });
					if (rest === "/conversation") return json(conversation(s));
					if (rest === "/conversation/models") return json({ models: [], selected: {} });
					if (rest === "/conversation/skills") return json({ skills: [] });
					if (rest === "/workspace/files") return json({ files: [], truncated: false });
					if (rest === "/interface-transition") return json({ supported: true, targetMode: "tui" });
				}
				return json({ error: { code: "NOT_FOUND", message: "not found" } }, 404);
			};

			// ---- event driver ----
			const sources: Array<{ url: string; readyState: number; _dispatch: (type: string, data?: string) => void }> = [];
			const FakeES = window.EventSource as unknown as new (url: string) => (typeof sources)[number];
			(window as unknown as { EventSource: unknown }).EventSource = class extends (FakeES as unknown as new (url: string) => object) {
				constructor(url: string) {
					super(url);
					sources.push(this as unknown as (typeof sources)[number]);
				}
			};
			let sent = 0;
			let seq = 0;
			const emit = (type: string, s: Sess, payload: Record<string, unknown>) => {
				const data = JSON.stringify({ seq: (seq += 1), projectId: s.workspaceId, sessionId: s.id, type, payload, createdAt: now });
				for (const source of sources) if (source.url.includes("/api/v1/events") && source.readyState === 1) source._dispatch(type, data);
				sent += 1;
			};
			let timer: number | undefined;
			(window as unknown as { __tallpineLoad: unknown }).__tallpineLoad = {
				sent: () => sent,
				start: ({ eventsPerSecond, durationMs, streamId }: { eventsPerSecond: number; durationMs: number; streamId: string }) => {
					const workers = sessions.filter((s) => s.kind === "worker");
					const startedAt = performance.now();
					let emitted = 0;
					let i = 0;
					if (timer !== undefined) clearInterval(timer);
					timer = window.setInterval(() => {
						const elapsed = performance.now() - startedAt;
						if (elapsed > durationMs) {
							clearInterval(timer);
							timer = undefined;
							return;
						}
						const due = Math.floor((elapsed / 1000) * eventsPerSecond);
						for (; emitted < due; emitted++) {
							const s = emitted % 3 === 0 ? sessions.find((x) => x.id === streamId)! : workers[i++ % workers.length];
							s.updatedAt = new Date().toISOString();
							s.activity = { state: "active", lastActivityAt: s.updatedAt };
							// Half of the traffic is streamed conversation deltas, half is session activity.
							if (emitted % 2 === 0 || s.id === streamId) {
								text[s.id as string] += "streamed token ";
								emit("session_updated", s, { id: s.id, conversationId: `conv-${s.id}` });
							} else {
								emit("session_updated", s, { id: s.id, activity: "active", isTerminated: false });
							}
						}
					}, 10);
				},
			};
		},
		{ projects: PROJECTS, workersPer: WORKERS_PER_PROJECT, countRenders },
	);
}
