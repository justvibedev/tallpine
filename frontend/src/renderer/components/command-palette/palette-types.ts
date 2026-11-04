/** Merged hook state the palette sections read. */
import type { usePaletteData } from "./usePaletteData";
import type { usePaletteNavigation } from "./usePaletteNavigation";
import type { usePaletteActions } from "./usePaletteActions";

export type CommandPaletteState = ReturnType<typeof usePaletteData> &
	ReturnType<typeof usePaletteNavigation> &
	ReturnType<typeof usePaletteActions>;
