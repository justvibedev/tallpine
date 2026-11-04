import { memo, useLayoutEffect, useRef } from "react";
import { encodeQr } from "../../lib/qr-matrix";
import { paintQr, quietZonePx } from "./qr-paint";

/*
 * Codes are drawn at the smallest QR version that holds the payload, with no
 * logo. A pairing offer fits in about version 11 (61 modules); the panel used
 * to pin version 17 (85 modules) and blank the middle for a logo, which at the
 * 240px panel size left under 3px per module and spent half of error
 * correction M on the logo: hard for a phone camera held at a screen.
 */

/**
 * A QR code drawn on one canvas, synchronously.
 *
 * Encoding and drawing take a few milliseconds, so the code is on screen in
 * the same frame as the component: nothing loads, nothing is cached, and
 * there is no empty state to cover. (The earlier SVG renderer built a clip
 * path of more than a thousand elements per code and drew it after an async
 * image load, which is what made the settings page stutter.)
 *
 * `value` null clears the canvas. `onReady` runs right after a draw, also
 * where no canvas is available (tests), so callers need no special case.
 */
export const QrCanvas = memo(function QrCanvas({
	value,
	size,
	cache = false,
	className,
	onReady,
	"data-qr-value": dataQrValue,
}: {
	value: string | null;
	/** Side of the square, in CSS px. */
	size: number;
	/** The code is public (a store link, the decoy): keep its encoding for next
	 *  time. Off by default, because a pairing code carries the password. */
	cache?: boolean;
	className?: string;
	onReady?: () => void;
	"data-qr-value"?: string;
}) {
	const ref = useRef<HTMLCanvasElement>(null);
	// Read through a ref so a new callback identity does not redraw the code.
	const onReadyRef = useRef(onReady);
	onReadyRef.current = onReady;

	// Layout effect, so the code is in the first painted frame.
	useLayoutEffect(() => {
		const canvas = ref.current;
		if (!canvas) return undefined;
		const draw = () => {
			const ctx = canvas.getContext("2d");
			if (!ctx) return;
			const dpr = Math.max(1, window.devicePixelRatio || 1);
			const pixels = Math.round(size * dpr);
			// Resizing the backing store clears it; either way the draw below covers it.
			if (canvas.width !== pixels) {
				canvas.width = pixels;
				canvas.height = pixels;
			}
			ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
			if (value === null) {
				ctx.clearRect(0, 0, size, size);
				return;
			}
			const matrix = encodeQr(value, { cache });
			const margin = quietZonePx(size, matrix.modules);
			paintQr(ctx, matrix, { size, margin, showLogo: false, logo: null });
		};
		try {
			draw();
		} catch {
			// The code is purely visual: where it cannot be drawn, fail silent
			// rather than crash the settings page.
		}
		if (value !== null) onReadyRef.current?.();
		return undefined;
	}, [value, size, cache]);

	return (
		<canvas
			ref={ref}
			aria-hidden="true"
			className={className}
			data-qr-value={dataQrValue}
			style={{ width: size, height: size }}
		/>
	);
});
