import type { ImgHTMLAttributes } from "react";
import { useRemoteImage } from "../hooks/useRemoteImage";

/**
 * Drop-in `<img>` for trusted remote sources (SCM avatars, favicons, link
 * previews). Remote URLs load through the main process because the renderer CSP
 * does not allow remote `img-src`; failures surface as a normal `onError`.
 */
export function RemoteImg({ src, onError, onLoad, ...rest }: ImgHTMLAttributes<HTMLImageElement>) {
	const image = useRemoteImage(src, true);
	if (image.state === "loading" || image.state === "idle") return null;
	if (image.state === "failed" || !image.src) {
		// Report once through a detached element so callers keep their onError handling.
		return <img {...rest} alt={rest.alt ?? ""} src="data:," onError={onError} hidden />;
	}
	return <img {...rest} src={image.src} onError={onError} onLoad={onLoad} />;
}
