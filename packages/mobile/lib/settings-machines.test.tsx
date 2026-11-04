import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	navigate: vi.fn(),
	hosts: [{ id: "dev", name: "Dev", endpoints: [], token: "token", platform: "darwin", lastConnected: 1 }],
	theme: new Proxy({}, { get: () => "#000000" }),
}));
vi.mock("expo-router", async () => {
	const React = await import("react");
	return {
		useRouter: () => ({ navigate: fixtures.navigate }),
		useFocusEffect: (effect: () => void) => React.useEffect(effect, [effect]),
	};
});
vi.mock("react-native", () => ({
	ActivityIndicator: "ActivityIndicator", Alert: { alert: vi.fn() }, Linking: {},
	Platform: { OS: "android" }, Pressable: "Pressable", ScrollView: "ScrollView",
	StyleSheet: { create: (styles: object) => styles, hairlineWidth: 1 }, Text: "Text", View: "View",
}));
vi.mock("@expo/ui", () => ({ Host: "Host", Picker: "Picker", Switch: "Switch" }));
vi.mock("expo-application", () => ({ nativeApplicationVersion: "1.0", nativeBuildVersion: "1" }));
vi.mock("expo-clipboard", () => ({ setStringAsync: vi.fn() }));
vi.mock("expo-device", () => ({ modelName: "Phone" }));
vi.mock("expo-constants", () => ({ default: { expoConfig: {} } }));
vi.mock("./icons", () => ({ Feather: "Feather" }));
vi.mock("./api", () => ({ ApiError: class ApiError extends Error {}, pingServer: vi.fn() }));
vi.mock("./config", () => ({ isConfigured: () => false }));
vi.mock("./hosts", () => ({ activeHost: async () => fixtures.hosts[0], loadHosts: async () => fixtures.hosts }));
vi.mock("./disconnect", () => ({ forgetPairedHost: vi.fn(), forgetServer: vi.fn() }));
vi.mock("./haptics", () => ({ haptics: { tap: vi.fn(), select: vi.fn(), warning: vi.fn() } }));
vi.mock("./layoutGrid", () => ({ toggleLayoutGrid: vi.fn(), useLayoutGrid: () => false }));
vi.mock("./native-header-button", () => ({ NativeHeaderButton: "NativeHeaderButton" }));
vi.mock("./openGitHub", () => ({ openGitHub: vi.fn() }));
vi.mock("./push", () => ({
	getPushStatus: async () => ({ osPermission: "undetermined", registered: false, localToken: null }),
	openNotificationSettings: vi.fn(), registerForPush: vi.fn(), unregisterFromPush: vi.fn(),
}));
vi.mock("./store", () => ({ useApp: () => ({ config: null, configured: false, selectedHostName: "Dev", connection: "closed", activeEndpoints: [] }) }));
vi.mock("./ThemeProvider", () => ({
	useTheme: () => fixtures.theme,
	useThemedStyles: (factory: (theme: object) => object) => factory(fixtures.theme),
	useThemeState: () => ({ preference: "system", scheme: "dark", setPreference: vi.fn() }),
}));
vi.mock("./backNavigation", () => ({ backOr: vi.fn() }));
vi.mock("./RouteErrorBoundary", () => ({ RouteErrorBoundary: () => null }));

import SettingsScreen from "../app/settings/index";

let renderer: ReactTestRenderer | undefined;
beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	vi.stubGlobal("__DEV__", false);
	fixtures.navigate.mockClear();
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
});
afterEach(() => {
	act(() => renderer?.unmount());
	vi.unstubAllGlobals();
	vi.restoreAllMocks();
});

it("shows Add a computer below Test connection with the dropdown closed and opens pairing", async () => {
	await act(async () => { renderer = create(<SettingsScreen />); });
	expect(renderer!.root.findAllByProps({ testID: "machine-selector-options" })).toHaveLength(0);
	const rows = renderer!.root.findAll((node) => (node.type as unknown) === "Text" && ["Test connection", "Add a computer"].includes(node.props.children));
	expect(rows.map((row) => row.props.children)).toEqual(["Test connection", "Add a computer"]);
	const add = rows[1].parent!;
	act(() => add.props.onPress());
	expect(fixtures.navigate).toHaveBeenCalledWith("/pair");
	act(() => renderer!.root.findByProps({ testID: "machine-selector-trigger" }).props.onPress());
	expect(renderer!.root.findAll((node) => (node.type as unknown) === "Text" && node.props.children === "Add a computer")).toHaveLength(1);
});

it("keeps Manage computers row without their helper paragraphs", async () => {
	await act(async () => { renderer = create(<SettingsScreen />); });
	const texts = renderer!.root.findAllByType("Text" as never).map((node) => node.props.children);
	expect(texts).toContain("Manage computers");
	expect(texts).not.toContain("See every saved connection route across all of your computers.");
});

it("keeps removal and editing out of Settings, including for paired computers", async () => {
	await act(async () => { renderer = create(<SettingsScreen />); });
	act(() => renderer!.root.findByProps({ testID: "machine-selector-trigger" }).props.onPress());
	const labels = renderer!.root.findAllByType("Text" as never).map((node) => node.props.children);
	const icons = renderer!.root.findAllByType("Feather" as never).map((node) => node.props.name);
	for (const label of labels) if (typeof label === "string") expect(label).not.toMatch(/Forget|Disconnect/);
	for (const icon of ["trash-2", "log-out", "edit-2"]) expect(icons).not.toContain(icon);
});

it.each(["Add a computer", "Report a problem"])("shows %s as a tappable action without a trailing chevron", async (label) => {
	await act(async () => { renderer = create(<SettingsScreen />); });
	const row = renderer!.root.findAll((node) => (node.type as unknown) === "Text" && node.props.children === label)[0].parent!;
	expect(row.props.onPress).toBeTypeOf("function");
	expect(row.findAllByType("Feather" as never).map((icon) => icon.props.name)).not.toContain("chevron-right");
	const environments = renderer!.root.findAll((node) => (node.type as unknown) === "Text" && node.props.children === "Manage computers" && node.parent?.props.onPress)[0].parent!;
	expect(environments.findAllByType("Feather" as never).map((icon) => icon.props.name)).toContain("chevron-right");
});

it.each(["Software update", "App updates", "Request a feature", "Discord", "App Store"])("renders no upstream %s row", async (label) => {
	await act(async () => { renderer = create(<SettingsScreen />); });
	const texts = renderer!.root.findAllByType("Text" as never).map((node) => node.props.children);
	expect(texts).not.toContain(label);
});
