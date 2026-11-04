import { createElement, type ReactNode } from "react";
import { readdirSync } from "node:fs";
import { createRequire } from "node:module";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
vi.mock("expo-router", () => ({
	Tabs: Object.assign(
		({ children, ...props }: { children?: ReactNode }) => createElement("Tabs", props, children),
		{ Screen: (props: { name: string }) => createElement("TabsScreen", props) },
	),
}));
vi.mock("expo-router/stack", () => ({ Stack: (props: object) => createElement("Stack", props) }));
vi.mock("./sidebar-navigation-shell", () => ({ SidebarNavigationShell: ({ children }: { children: ReactNode }) => createElement("Sidebar", {}, children) }));
import SidebarLayout from "../app/(tabs)/_layout";
// Exercise the installed Expo router, not a fake navigation state machine.
const { TabRouter } = createRequire(import.meta.url)("expo-router/build/react-navigation/routers/TabRouter.js");

describe("sidebar list lifecycle", () => {
	it("keeps route and navigator keys when moving among the three existing pages", () => {
		const router = TabRouter({ initialRouteName: "index", backBehavior: "none" });
		const routeNames = ["index", "projects", "prs"];
		const options = { routeNames, routeGetIdList: {} };
		// expo-router 58 seeds navigator state globally; mirror that seed here.
		const seed = { key: "tab-root", stale: false, type: "tab", index: 0, routeNames, routes: routeNames.map((name) => ({ key: `${name}-key`, name })) };
		let state = router.getStateForDeclaredRoutes(seed, routeNames);
		const key = state.key;
		const routes = state.routes.map((route: { key: string }) => route.key);
		for (const name of ["projects", "index", "prs", "index", "prs"]) {
			state = router.getStateForAction(state, { type: "NAVIGATE", payload: { name } }, options).state;
			expect(state.key).toBe(key);
			expect(state.routes.map((route: { key: string }) => route.key)).toEqual(routes);
			expect(state.routes[state.index].name).toBe(name);
		}
		const back = router.getStateForAction(state, { type: "GO_BACK" }, options);
		expect(back === null || typeof back === "string").toBe(true);
	});
	it("retains visited pages without mounting all pages upfront or adding a visible tab bar", () => {
		let renderer!: ReactTestRenderer;
		act(() => { renderer = create(<SidebarLayout />); });
		const tabs = renderer.root.findByType("Tabs" as never);
		expect(tabs.props.screenOptions).toMatchObject({ headerShown: false, animation: "none", lazy: true, freezeOnBlur: true });
		expect(tabs.props.detachInactiveScreens).toBe(true);
		expect(tabs.props.backBehavior).toBe("none");
		expect(tabs.props.tabBar()).toBeNull();
		act(() => renderer.unmount());
	});
	it("declares a screen for every route file, since expo-router 58 renders only declared screens", () => {
		// Undeclared routes stay in navigator state but draw nothing: the Workers
		// and Projects pages were blank (black in dark mode) with data loaded.
		const files = readdirSync(new URL("../app/(tabs)/", import.meta.url))
			.filter((file) => /\.tsx$/.test(file) && !file.startsWith("_"))
			.map((file) => file.replace(/\.tsx$/, ""))
			.sort();
		let renderer!: ReactTestRenderer;
		act(() => { renderer = create(<SidebarLayout />); });
		const declared = renderer.root.findAllByType("TabsScreen" as never).map((screen) => screen.props.name).sort();
		expect(files).toEqual(["index", "projects", "prs"]);
		expect(declared).toEqual(files);
		act(() => renderer.unmount());
	});
});
