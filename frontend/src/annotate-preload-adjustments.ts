/** Style-adjustment fields offered by the annotation overlay (annotate-preload.ts). */
import type { BrowserAdjustmentProperty } from "./shared/browser-annotations";

export type AdjustmentDefinition = {
	property: BrowserAdjustmentProperty;
	/** Full name: the accessible name and the reset's title. */
	label: string;
	/** Compact name shown inside the field. Kept a substring of `label` so the
	 *  visible label stays part of the accessible name (WCAG 2.5.3). */
	short: string;
	group: "Content" | "Appearance" | "Size" | "Spacing" | "Layout";
	options?: string[];
};

export const ADJUSTMENTS: AdjustmentDefinition[] = [
	{ property: "textContent", label: "Text content", short: "Text", group: "Content" },
	{ property: "color", label: "Text color", short: "Color", group: "Appearance" },
	{ property: "backgroundColor", label: "Fill", short: "Fill", group: "Appearance" },
	{ property: "opacity", label: "Opacity", short: "Opacity", group: "Appearance" },
	{ property: "fontFamily", label: "Font family", short: "Font", group: "Appearance", options: ["system-ui", "Arial", "Helvetica", "Georgia", "Times New Roman", "monospace"] },
	{ property: "fontSize", label: "Font size", short: "Size", group: "Appearance" },
	{ property: "fontWeight", label: "Font weight", short: "Weight", group: "Appearance", options: ["100", "200", "300", "400", "500", "600", "700", "800", "900"] },
	{ property: "borderRadius", label: "Border radius", short: "Radius", group: "Appearance" },
	{ property: "borderColor", label: "Border color", short: "Color", group: "Appearance" },
	{ property: "borderWidth", label: "Border width", short: "Width", group: "Appearance" },
	// The size pair reads W/H (the pattern every inspector uses), which also keeps
	// its visible label distinct from the border's "Width" field.
	{ property: "width", label: "Width", short: "W", group: "Size" },
	{ property: "height", label: "Height", short: "H", group: "Size" },
	{ property: "paddingTop", label: "Padding top", short: "Top", group: "Spacing" },
	{ property: "paddingBottom", label: "Padding bottom", short: "Bottom", group: "Spacing" },
	{ property: "paddingLeft", label: "Padding left", short: "Left", group: "Spacing" },
	{ property: "paddingRight", label: "Padding right", short: "Right", group: "Spacing" },
	{ property: "marginTop", label: "Margin top", short: "Top", group: "Spacing" },
	{ property: "marginBottom", label: "Margin bottom", short: "Bottom", group: "Spacing" },
	{ property: "marginLeft", label: "Margin left", short: "Left", group: "Spacing" },
	{ property: "marginRight", label: "Margin right", short: "Right", group: "Spacing" },
	{ property: "flexDirection", label: "Flex flow", short: "Flow", group: "Layout", options: ["row", "row-reverse", "column", "column-reverse"] },
	{ property: "alignItems", label: "Align items", short: "Align", group: "Layout", options: ["start", "center", "end", "stretch", "baseline"] },
	{ property: "justifyContent", label: "Justify content", short: "Justify", group: "Layout", options: ["start", "center", "end", "space-between", "space-around", "space-evenly"] },
	{ property: "columnGap", label: "Column gap", short: "Col", group: "Layout" },
	{ property: "rowGap", label: "Row gap", short: "Row", group: "Layout" },
];

export const PIXEL_PROPERTIES = new Set<BrowserAdjustmentProperty>([
	"fontSize", "borderWidth", "borderRadius", "width", "height",
	"marginTop", "marginRight", "marginBottom", "marginLeft",
	"paddingTop", "paddingRight", "paddingBottom", "paddingLeft",
	"columnGap", "rowGap",
]);
export const COLOR_PROPERTIES = new Set<BrowserAdjustmentProperty>(["color", "backgroundColor", "borderColor"]);
export const TEXT_STYLE_PROPERTIES = new Set<BrowserAdjustmentProperty>(["color", "fontFamily", "fontSize", "fontWeight"]);
export const SUPPORTED_ADJUSTMENT_PROPERTIES = new Set(ADJUSTMENTS.map((item) => item.property));
