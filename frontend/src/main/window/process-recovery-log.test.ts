import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { createProcessRecoveryLog, PROCESS_RECOVERY_LOG_MAX_BYTES } from "./process-recovery-log";

describe("createProcessRecoveryLog", () => {
	it("persists timestamped, versioned lines and rotates at the cap", () => {
		const dir = mkdtempSync(path.join(process.env.TMPDIR ?? os.tmpdir(), "prl-"));
		const log = createProcessRecoveryLog(dir, "9.9.9", () => {});
		log("renderer gone");
		expect(readFileSync(path.join(dir, "logs", "process-recovery.log"), "utf8")).toMatch(/^\d{4}-.*Z v9\.9\.9 renderer gone\n$/);
		writeFileSync(path.join(dir, "logs", "process-recovery.log"), "x".repeat(PROCESS_RECOVERY_LOG_MAX_BYTES));
		log("after rotate");
		expect(readFileSync(path.join(dir, "logs", "process-recovery.log.1"), "utf8")).toHaveLength(PROCESS_RECOVERY_LOG_MAX_BYTES);
		expect(readFileSync(path.join(dir, "logs", "process-recovery.log"), "utf8")).toContain("after rotate");
	});
});
