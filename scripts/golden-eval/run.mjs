// Driver: replays golden items in a dedicated eval project, one arm and
// repetition at a time, with an owner-proxy that answers and approves a fixed
// delay after each wait (the real owner's cache-expiry test). `--dry-run`
// prints the schedule and touches no daemon.
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { ARMS, buildSchedule, MAX_CONCURRENCY, planRuns, renderSchedule } from "./plan.mjs";

if (process.argv[1] === fileURLToPath(import.meta.url)) {
	const a = new Map();
	const flags = new Set();
	for (let i = 2; i < process.argv.length; i++) {
		const t = process.argv[i];
		if (["--dry-run", "--setup", "--live"].includes(t)) flags.add(t.slice(2));
		else if (t.startsWith("--")) a.set(t.slice(2), process.argv[++i]);
	}
	const golden = JSON.parse(readFileSync(a.get("golden") ?? new URL("./golden.json", import.meta.url), "utf8"));
	const only = a.get("item");
	const items = golden.items.filter((it) => !only || it.ref === only);
	const arms = a.get("arm") ? [a.get("arm")] : ARMS;
	if (arms.some((x) => !ARMS.includes(x))) {
		console.error("--arm must be in-place or separate");
		process.exit(2);
	}
	const concurrency = Number(a.get("concurrency") ?? MAX_CONCURRENCY);
	const runs = planRuns(items, { arms, reps: a.has("rep") ? 1 : 2 }).map((r) => (a.has("rep") ? { ...r, rep: Number(a.get("rep")) } : r));
	if (flags.has("dry-run")) {
		process.stdout.write(renderSchedule(buildSchedule(runs, { concurrency }), { concurrency }));
	} else if (flags.has("setup") || flags.has("live")) {
		const { cliMain } = await import("./cli.mjs");
		process.exit(await cliMain({ mode: flags.has("setup") ? "setup" : "live", a, items, runs, concurrency }));
	} else {
		console.error("choose --dry-run (print the schedule), --setup (build and register the eval projects) or --live (replay); see README.md");
		process.exit(3);
	}
}
