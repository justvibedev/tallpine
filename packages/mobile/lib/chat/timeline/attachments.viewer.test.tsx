import { act, create } from "react-test-renderer";
import { expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });
vi.mock("react-native", () => ({
	Image: "Image", Platform: { OS: "ios" }, Pressable: "Pressable", Text: "Text", View: "View",
}));
vi.mock("../../icons", () => ({ Feather: "Feather" }));
vi.mock("../../haptics", () => ({ haptics: { tap: () => {} } }));
vi.mock("../../config", () => ({ authHeaders: () => ({}), httpBase: () => "" }));
vi.mock("../../store", () => ({ useApp: () => ({}) }));
vi.mock("../../ThemeProvider", () => ({ useTheme: () => ({}), useThemedStyles: () => ({}) }));
vi.mock("../../ui", () => ({ MascotLamp: () => null }));
vi.mock("../ChatMarkdown", () => ({ ChatMarkdown: () => null }));
vi.mock("../../api", () => ({ previewFilePath: () => "" }));
vi.mock("./styles", () => ({ makeStyles: () => ({}) }));
vi.mock("../../ImageViewer", () => ({ ImageViewer: (props: { name: string; onClose(): void }) => <ViewerProbe {...props} /> }));
function ViewerProbe(_props: { name: string; onClose(): void }) { return null; }
function NativeMenu({ children }: { children: React.ReactNode }) { return <>{children}</>; }
import type React from "react";
import { ImageViewerHost, StagedAttachment } from "./attachments";

const source = { uri: "http://host/a.png", headers: { Authorization: "Bearer x" } };

it("opens a tapped bubble image in the timeline-level viewer, outside the native message menu, and closes it", () => {
	let renderer!: ReturnType<typeof create>;
	act(() => { renderer = create(<ImageViewerHost><NativeMenu><StagedAttachment name="a.png" source={source} tileSize={100} /></NativeMenu></ImageViewerHost>); });
	// No Modal is mounted per tile while closed.
	expect(renderer.root.findAllByType(ViewerProbe)).toHaveLength(0);
	act(() => renderer.root.findByProps({ accessibilityLabel: "Open a.png" }).props.onPress());
	const viewers = renderer.root.findAllByType(ViewerProbe);
	expect(viewers).toHaveLength(1);
	expect(renderer.root.findByType(NativeMenu).findAllByType(ViewerProbe)).toHaveLength(0);
	act(() => viewers[0].props.onClose());
	expect(renderer.root.findAllByType(ViewerProbe)).toHaveLength(0);
});

it("falls back to its own viewer when rendered without a host", () => {
	let renderer!: ReturnType<typeof create>;
	act(() => { renderer = create(<StagedAttachment name="a.png" source={source} tileSize={100} />); });
	expect(renderer.root.findAllByType(ViewerProbe)).toHaveLength(0);
	act(() => renderer.root.findByProps({ accessibilityLabel: "Open a.png" }).props.onPress());
	expect(renderer.root.findAllByType(ViewerProbe)).toHaveLength(1);
});
