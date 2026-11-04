import { useQuery } from "@tanstack/react-query";
import { tallpineBridge } from "../lib/bridge";
import { isCommandPaletteEnabled } from "../lib/build-channel";

export function useAppVersion(): string | undefined {
	const { data } = useQuery({
		queryKey: ["app-version"],
		queryFn: () => tallpineBridge.app.getVersion(),
		staleTime: Infinity,
	});
	return typeof data === "string" ? data : undefined;
}

export function useCommandPaletteEnabled(): boolean {
	return isCommandPaletteEnabled(useAppVersion());
}
