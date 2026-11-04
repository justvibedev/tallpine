import type { DesktopUpdateStatus } from "./api";

export type DesktopUpdateRow = { label: string; canStart: boolean; note?: string };

const short = (sha: string) => sha.slice(0, 7);

/** What the Settings row on the phone shows for the Mac's "Update & restart". */
export function describeDesktopUpdate(status: DesktopUpdateStatus | null, busy: boolean): DesktopUpdateRow {
	if (!status) return { label: "Checking…", canStart: false };
	if (busy || status.running) return { label: "Updating…", canStart: false };
	const last = status.last;
	const note =
		last && (last.state === "failed" || last.state === "rolled_back")
			? `Last update to ${short(last.commit)} ${last.state === "rolled_back" ? "was rolled back" : "failed"}: ${last.message}`
			: undefined;
	if (status.updateAvailable) return { label: `Update ${short(status.candidate)} (CI passed)`, canStart: true, note };
	return { label: status.candidate ? "Up to date" : "No CI-passed build", canStart: false, note };
}
