/**
 * Local image paths a tool call touched, for the thumbnails under its row.
 *
 * A screenshot tool returns the path it wrote, a read or an edit names the file,
 * and a command prints or lists it. Paths come from the structured file list and
 * from whatever text the call carries; only absolute image paths count, the same
 * rule the reply thumbnails use, and main still decides what is served.
 */

import { findLocalImagePaths, isLocalImagePath, MAX_LOCAL_IMAGE_PATHS } from "../../../shared/local-image-paths";
import { fileChangeFiles, reviewedPaths, type ConversationActivity } from "../../types/conversation";

function textOf(value: unknown): string {
	if (value === undefined || value === null) return "";
	if (typeof value === "string") return value;
	try {
		return JSON.stringify(value) ?? "";
	} catch {
		return "";
	}
}

export function toolImagePaths(activity: ConversationActivity, max = MAX_LOCAL_IMAGE_PATHS): string[] {
	if (activity.status === "running") return [];
	const detail = activity.detail;
	const found: string[] = [];
	const add = (path: string) => {
		if (found.length < max && isLocalImagePath(path) && !found.includes(path)) found.push(path);
	};
	for (const file of fileChangeFiles(activity)) add(file.path);
	for (const path of reviewedPaths(activity)) add(path);
	const text = [detail?.arguments, detail?.result, detail?.content, detail?.command, detail?.output].map(textOf).filter(Boolean).join("\n");
	for (const path of findLocalImagePaths(text, max)) add(path);
	return found;
}
