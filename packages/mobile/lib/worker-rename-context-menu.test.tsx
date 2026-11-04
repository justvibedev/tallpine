import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, describe, expect, it, vi } from "vitest";

// The bundled drawables are Metro assets; Node cannot parse them, so stand in an id.
vi.hoisted(() => {
	// eslint-disable-next-line @typescript-eslint/no-require-imports
	const Module = require("node:module") as { _extensions: Record<string, (m: { exports: unknown }) => void> };
	Module._extensions[".xml"] = (m) => { m.exports = 1; };
});

vi.mock("./ThemeProvider", () => ({
	useTheme: () => new Proxy({}, { get: (_t, key) => `c-${String(key)}` }),
	useThemeState: () => ({ scheme: "dark" }),
}));
vi.mock("@expo/ui/community/menu", async () => {
	const React = await import("react");
	return { MenuView: ({ children, ...props }: { children?: React.ReactNode }) => React.createElement("MenuView", props, children) };
});
vi.mock("react-native", async () => {
	const React = await import("react");
	return { Pressable: ({ children, ...props }: { children?: React.ReactNode }) => React.createElement("Pressable", props, children) };
});

import { WorkerRowContextMenu } from "./worker-rename-context-menu";
import { WorkerRowContextMenu as AndroidMenu } from "./worker-rename-context-menu.android";

let renderer: ReactTestRenderer | undefined;
afterEach(async () => {
	if (renderer) await act(async () => renderer?.unmount());
	renderer = undefined;
});

describe.each([["default", WorkerRowContextMenu], ["Android", AndroidMenu]] as const)("%s worker row context menu", (_name, Menu) => {
	const render = async (onAction = vi.fn(), onPress = vi.fn()) => {
		await act(async () => {
			renderer = create(
				<Menu
					onPress={onPress}
					onAction={onAction}
					gestureRef={{ current: undefined }}
					accessibilityLabel="Row"
					accessibilityHint="Opens"
					style={{ a: 1 }}
					pressedStyle={{ b: 2 }}
					actions={[
						{ id: "pin", title: "Pin" },
						{ id: "delete", title: "Delete", destructive: true },
					] as never}
				>
					row
				</Menu>,
			);
		});
		return renderer!.root;
	};

	it("hosts the row in a long-press menu with themed, destructive-aware actions", async () => {
		const root = await render();
		const menu = root.findByProps({ testID: "worker-row-menu" });
		expect(menu.props.shouldOpenOnLongPress).toBe(true);
		expect(menu.props.colorScheme).toBe("dark");
		const [pin, del] = menu.props.actions;
		expect(pin).toMatchObject({ id: "pin", title: "Pin", titleColor: "c-textPrimary", imageColor: "c-accent" });
		expect(del).toMatchObject({ id: "delete", titleColor: "c-red", imageColor: "c-red", attributes: { destructive: true } });
	});

	it("forwards the chosen action id and the row press", async () => {
		const onAction = vi.fn(), onPress = vi.fn();
		const root = await render(onAction, onPress);
		act(() => root.findByProps({ testID: "worker-row-menu" }).props.onPressAction({ nativeEvent: { event: "delete" } }));
		expect(onAction).toHaveBeenCalledWith("delete");
		const row = root.findAllByType("Pressable" as never)[0];
		act(() => row.props.onPress());
		expect(onPress).toHaveBeenCalled();
		expect(row.props.style({ pressed: true })).toEqual([{ a: 1 }, { b: 2 }]);
		expect(row.props.style({ pressed: false })).toEqual([{ a: 1 }, false]);
	});
});
