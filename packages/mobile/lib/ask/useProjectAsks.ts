import { useCallback, useEffect, useRef, useState } from "react";
import { AppState } from "react-native";
import type { ServerConfig } from "../config";
import { startPlanPolling } from "../plan/planPolling";
import { listProjectAsks } from "./askApi";
import type { OwnerAsk } from "./askModel";

const REFRESH_MS = 8000;
const EMPTY: OwnerAsk[] = [];

/** Loads a project's asks and keeps them fresh while mounted; a project switch never shows the old project's asks. */
export function useProjectAsks(cfg: ServerConfig | null | undefined, projectId: string | undefined) {
	const [loaded, setLoaded] = useState<{ cfg: unknown; projectId: string; asks: OwnerAsk[] }>();
	const current = useRef({ cfg, projectId });
	current.current = { cfg, projectId };
	const refresh = useCallback(async () => {
		if (!cfg || !projectId) return;
		try {
			const asks = await listProjectAsks(cfg, projectId);
			if (current.current.cfg !== cfg || current.current.projectId !== projectId) return;
			setLoaded({ cfg, projectId, asks });
		} catch { /* keep the last list; the next tick retries */ }
	}, [cfg, projectId]);
	useEffect(() => startPlanPolling(() => void refresh(), AppState, REFRESH_MS), [refresh]);
	const asks = cfg && projectId && loaded?.cfg === cfg && loaded.projectId === projectId ? loaded.asks : EMPTY;
	return { asks, refresh };
}
