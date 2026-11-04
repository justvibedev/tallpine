import { Image, View } from "react-native";
import type { ConnStatus } from "../store";
import { useTheme, useThemedStyles } from "../ThemeProvider";

// Tallpine mark (transparent, black, tinted at draw time) shown beside each screen heading.
import MASCOT from "../../assets/tallpine-mark.png";

import { Dot } from "./indicators";
import { makeStyles } from "./styles";

// `tallpine-mark.png` is the desktop tray mark (frontend/assets/tallpine/brand/tray-mark.svg)
// rasterised black; MascotLamp tints it with the theme text colour.
// Its aspect ratio, as height / width.
export const MASCOT_ASPECT = 1;
// Where the top node sits in the artwork, as fractions of its width and height.
const WAND_TIP_X = 0.5;
const WAND_TIP_Y = 0.125;
const LAMP_DOT = 7;
const LAMP_HALO = 16;

// The mark, with its top node doubling as the connection lamp: it glows
// green when the daemon is reachable and goes dark when it isn't.
export function MascotLamp({ status, size = 40 }: { status?: ConnStatus; size?: number }) {
	const t = useTheme();
	const s = useThemedStyles(makeStyles);
	const color = status === "open" ? t.green : status === "connecting" ? t.amber : t.textFaint;
	const lit = status === "open" || status === "connecting";
	const label = status === "open" ? "Connected" : status === "connecting" ? "Connecting" : "Offline";
	// The lamp and halo scale with the logo and are centred on the wand tip, so
	// they stay on it at whatever size the logo is drawn.
	const k = size / 40;
	const height = size * MASCOT_ASPECT;
	const tipX = size * WAND_TIP_X;
	const tipY = height * WAND_TIP_Y;
	const dot = LAMP_DOT * k;
	const halo = LAMP_HALO * k;
	return (
		<View
			style={[s.mascotWrap, { width: size, height }]}
			accessible
			accessibilityRole="image"
			accessibilityLabel={status ? `Tallpine, ${label}` : "Tallpine"}
		>
			<Image source={MASCOT} style={{ width: size, height, tintColor: t.textPrimary }} resizeMode="contain" />
			{status ? (
				<>
					{/* Halo first, dot on top: RN has no boxShadow, so the glow is a
					    larger translucent circle plus a platform shadow/elevation. */}
					{lit ? <View style={[s.wandHalo, { left: tipX - halo / 2, top: tipY - halo / 2, width: halo, height: halo, borderRadius: halo / 2, backgroundColor: color, shadowColor: color }]} /> : null}
					<View style={[s.wandTip, { left: tipX - dot / 2, top: tipY - dot / 2 }]}>
						<Dot color={color} size={dot} breathing={status === "connecting"} />
					</View>
				</>
			) : null}
		</View>
	);
}
