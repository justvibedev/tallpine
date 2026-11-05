/**
 * What counts as a live preview: any file the daemon surfaces (an .html build, or
 * a generated doc like plan.md / a report) EXCEPT a repo's README, which the
 * detector's markdown fallback always matches on a fresh checkout. Filtering the
 * README out keeps the globe's green dot meaningful: it means "there's something
 * the agent produced to view", not just "this repo has a README".
 */
export function hasLivePreview(preview: { entry: string } | null): boolean {
	const base = (preview?.entry ?? "").split("/").pop() ?? "";
	return !!preview && !/^readme\.(md|markdown)$/i.test(base);
}
