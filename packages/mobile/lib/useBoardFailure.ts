import { useMemo } from "react";
import { Platform } from "react-native";
import { classifyConnectionFailure, describeConnectionFailure, type ConnectionErrorCopy } from "./connectionError";
import { PAIRING_REMOVED_MESSAGE, PAIRING_REMOVED_TITLE } from "./revokedPairing";
import { tunnelMayHaveRotated } from "./staleTunnel";
import { useApp } from "./store";

/**
 * The board poll's failure as human copy, shared by every tab so the same
 * failure reads the same on Workers, Projects and PRs.
 */
export function useBoardFailure(): ConnectionErrorCopy {
	const { errorStatus, errorCode, connection, config, activeEndpoints, selectedHostName } = useApp();
	return useMemo(() => {
		// The poll only stops on a 401 because the machine dropped this pairing.
		if (errorStatus === 401 && classifyConnectionFailure(401, errorCode ?? undefined) === "auth") return { title: PAIRING_REMOVED_TITLE, icon: "monitor-off", message: PAIRING_REMOVED_MESSAGE, hint: PAIRING_REMOVED_MESSAGE, showLocalNetworkHint: false };
		const classified = classifyConnectionFailure(errorStatus ?? undefined, errorCode ?? undefined);
		return describeConnectionFailure(
			// A stored tunnel that no longer answers means the hostname rotated,
			// which no amount of retrying fixes — rescanning does. Distinguished
			// here rather than in the classifier because it depends on what the
			// machine advertised, not on a status code.
			classified === "unreachable" && tunnelMayHaveRotated(activeEndpoints, config?.endpointKind, connection === "open")
				? "tunnel-rotated"
				: classified,
			{ host: config?.host ?? "", port: config?.httpPort ?? "", platform: Platform.OS, name: selectedHostName ?? undefined },
		);
	}, [errorStatus, errorCode, selectedHostName, config?.host, config?.httpPort, activeEndpoints, connection]);
}
