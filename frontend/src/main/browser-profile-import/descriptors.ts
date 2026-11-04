/* Split out of browser-profile-import.ts (B297, P53). */

import path from "node:path";
import { BrowserDescriptor, DiscoveryContext, configHome, joinEnv } from "./common";

export const DESCRIPTORS: BrowserDescriptor[] = [
	{
		id: "chrome",
		name: "Google Chrome",
		family: "chromium",
		chromiumKeychainNames: ["Chrome", "Google Chrome"],
		roots: (c) => platformPaths(c, {
			win32: [joinEnv(c.env.LOCALAPPDATA, "Google", "Chrome", "User Data")],
			darwin: [path.join(c.homeDir, "Library", "Application Support", "Google", "Chrome")],
			linux: [path.join(configHome(c), "google-chrome")],
		}),
	},
	{
		id: "edge",
		name: "Microsoft Edge",
		family: "chromium",
		chromiumKeychainNames: ["Microsoft Edge"],
		roots: (c) => platformPaths(c, {
			win32: [joinEnv(c.env.LOCALAPPDATA, "Microsoft", "Edge", "User Data")],
			darwin: [path.join(c.homeDir, "Library", "Application Support", "Microsoft Edge")],
			linux: [path.join(configHome(c), "microsoft-edge")],
		}),
	},
	{
		id: "brave",
		name: "Brave",
		family: "chromium",
		chromiumKeychainNames: ["Brave", "Brave Browser"],
		roots: (c) => platformPaths(c, {
			win32: [joinEnv(c.env.LOCALAPPDATA, "BraveSoftware", "Brave-Browser", "User Data")],
			darwin: [path.join(c.homeDir, "Library", "Application Support", "BraveSoftware", "Brave-Browser")],
			linux: [path.join(configHome(c), "BraveSoftware", "Brave-Browser")],
		}),
	},
	{
		id: "chromium",
		name: "Chromium",
		family: "chromium",
		chromiumKeychainNames: ["Chromium"],
		roots: (c) => platformPaths(c, {
			win32: [joinEnv(c.env.LOCALAPPDATA, "Chromium", "User Data")],
			darwin: [path.join(c.homeDir, "Library", "Application Support", "Chromium")],
			linux: [path.join(configHome(c), "chromium")],
		}),
	},
	{
		id: "vivaldi",
		name: "Vivaldi",
		family: "chromium",
		chromiumKeychainNames: ["Vivaldi"],
		roots: (c) => platformPaths(c, {
			win32: [joinEnv(c.env.LOCALAPPDATA, "Vivaldi", "User Data")],
			darwin: [path.join(c.homeDir, "Library", "Application Support", "Vivaldi")],
			linux: [path.join(configHome(c), "vivaldi")],
		}),
	},
	{
		id: "arc",
		name: "Arc",
		family: "chromium",
		chromiumKeychainNames: ["Arc"],
		roots: (c) => platformPaths(c, {
			win32: [],
			darwin: [path.join(c.homeDir, "Library", "Application Support", "Arc", "User Data")],
			linux: [],
		}),
	},
	{
		id: "safari",
		name: "Safari",
		family: "safari",
		roots: (c) => platformPaths(c, {
			win32: [],
			darwin: [path.join(c.homeDir, "Library")],
			linux: [],
		}),
	},
	{
		id: "firefox",
		name: "Firefox",
		family: "firefox",
		roots: (c) => platformPaths(c, {
			win32: [joinEnv(c.env.APPDATA, "Mozilla", "Firefox")],
			darwin: [path.join(c.homeDir, "Library", "Application Support", "Firefox")],
			linux: [path.join(c.homeDir, ".mozilla", "firefox")],
		}),
	},
	{
		id: "zen",
		name: "Zen",
		family: "firefox",
		roots: (c) => platformPaths(c, {
			win32: [joinEnv(c.env.APPDATA, "zen")],
			darwin: [path.join(c.homeDir, "Library", "Application Support", "zen")],
			linux: [path.join(c.homeDir, ".zen")],
		}),
	},
];

export function platformPaths(
	context: DiscoveryContext,
	paths: Record<"win32" | "darwin" | "linux", string[]>,
): string[] {
	if (context.platform === "win32") return paths.win32.filter(Boolean);
	if (context.platform === "darwin") return paths.darwin.filter(Boolean);
	return paths.linux.filter(Boolean);
}
