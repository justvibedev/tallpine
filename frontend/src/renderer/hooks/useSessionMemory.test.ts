import { describe, expect, it } from "vitest";
import { formatCPU, formatMemory } from "./useSessionMemory";
import { formatDurationClock as formatDuration } from "@tallpine/shared/format/duration";
import { processKind } from "../components/session-memory/ProcessRows";

describe("formatMemory", () => {
	it("shows whole megabytes and switches to GB at a thousand, in 1024s", () => {
		expect(formatMemory(641_728_512)).toBe("612 MB");
		expect(formatMemory(2_254_857_830)).toBe("2.1 GB");
	});
});

describe("formatCPU", () => {
	it("is a whole percent of one core", () => {
		expect(formatCPU(82.4)).toBe("82%");
		expect(formatCPU(0.34)).toBe("0%");
	});
});

describe("processKind", () => {
	it("names the kind of process, keeping Tallpine's subcommand", () => {
		expect(processKind("/tmp/.mount_x/resources/daemon/tallpine chat-host s1 /home/u")).toBe("tallpine chat-host");
		expect(processKind("/usr/bin/git status --short")).toBe("git");
		expect(processKind("sh -c go test ./...")).toBe("sh");
		expect(processKind("")).toBe("?");
	});
});

describe("formatDuration", () => {
	it("reads as seconds, then minutes, then hours", () => {
		expect(formatDuration(38_400)).toBe("38s");
		expect(formatDuration(250_000)).toBe("4m 10s");
		expect(formatDuration(3_720_000)).toBe("1h 2m");
	});
});
