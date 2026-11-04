import type { TFunction } from "i18next";
import { useEffect,useRef,useState } from "react";
import { useTranslation } from "react-i18next";
import {
APP_SHORTCUTS,
effectiveShortcutBindings,
matchesShortcutBinding,
shortcutBindingLabel,
shortcutBindingValidationError,
type AppShortcutId,
type KeybindingOverrides,
type ShortcutBinding,
type ShortcutCategory,
} from "../../../../shared/shortcuts";
import { shortcutCategoryLabelKeys,shortcutLabelKeys } from "../../../i18n/key-maps";
import { tallpineBridge } from "../../../lib/bridge";
import { useKeybindingsStore } from "../../../stores/keybindings-store";

export function shortcutLabel(id: AppShortcutId, t: TFunction): string {
	return t(shortcutLabelKeys[id]);
}

export function shortcutCategoryLabel(category: ShortcutCategory, t: TFunction): string {
	return t(shortcutCategoryLabelKeys[category]);
}

export type RecordingState = { id: AppShortcutId; mode: "replace" | "add" };
export type ConflictState = {
	targetId: AppShortcutId;
	conflictingId: AppShortcutId;
	binding: ShortcutBinding;
	mode: RecordingState["mode"];
};
export type ToastState = {
	title: string;
	body?: string;
	undo?: () => Promise<void>;
};

const ignoredRecordingKeys = new Set([
	"Alt",
	"AltGraph",
	"CapsLock",
	"Control",
	"Dead",
	"Meta",
	"NumLock",
	"Process",
	"ScrollLock",
	"Shift",
	"Unidentified",
]);

const codeMatchedKeys: ReadonlySet<string> = new Set(["Backquote", "BracketLeft", "BracketRight"]);

export function eventBinding(event: KeyboardEvent): ShortcutBinding | null {
	if (ignoredRecordingKeys.has(event.key) || event.getModifierState?.("AltGraph")) return null;
	return {
		key: event.key.length === 1 ? event.key.toLowerCase() : event.key,
		// Keys whose `key` shifts with modifiers or layout are matched by physical code too.
		// Option also rewrites letters on macOS (⌥B types "∫"), so those keep their code.
		...(codeMatchedKeys.has(event.code) || (event.altKey && /^(Key[A-Z]|Digit\d)$/.test(event.code))
			? { code: event.code }
			: {}),
		ctrl: event.ctrlKey,
		meta: event.metaKey,
		shift: event.shiftKey,
		alt: event.altKey,
	};
}

export function definition(id: AppShortcutId) {
	return APP_SHORTCUTS.find((shortcut) => shortcut.id === id);
}


/**
 * Recording, conflict and undo state for the shortcut editor. The editor UI
 * (KeyboardShortcutsContent) only renders it.
 */
export function useShortcutRecorder({ active, isMac }: { active: boolean; isMac: boolean }) {
	const { t } = useTranslation();
	const overrides = useKeybindingsStore((state) => state.overrides);
	const setOverrides = useKeybindingsStore((state) => state.setOverrides);
	const resetBinding = useKeybindingsStore((state) => state.resetBinding);
	const resetAll = useKeybindingsStore((state) => state.resetAll);
	const [recording, setRecordingState] = useState<RecordingState | null>(null);
	const [conflict, setConflict] = useState<ConflictState | null>(null);
	const [toast, setToast] = useState<ToastState | null>(null);
	const toastTimerRef = useRef<number | undefined>(undefined);
	const activeRef = useRef(active);
	const recordingRequestRef = useRef(0);
	activeRef.current = active;

	const showToast = (next: ToastState) => {
		if (toastTimerRef.current !== undefined) window.clearTimeout(toastTimerRef.current);
		setToast(next);
		toastTimerRef.current = window.setTimeout(() => setToast(null), 3500);
	};

	useEffect(
		() => () => {
			activeRef.current = false;
			recordingRequestRef.current += 1;
			if (toastTimerRef.current !== undefined) window.clearTimeout(toastTimerRef.current);
			void tallpineBridge.keybindings.setRecording(false);
		},
		[],
	);

	useEffect(() => {
		if (active) return;
		setRecordingState(null);
		void tallpineBridge.keybindings.setRecording(false);
	}, [active]);

	useEffect(() => {
		if (!recording) return;
		const handleBlur = () => {
			setRecordingState(null);
			void tallpineBridge.keybindings.setRecording(false);
		};
		window.addEventListener("blur", handleBlur);
		return () => window.removeEventListener("blur", handleBlur);
	}, [recording]);

	const beginRecording = async (next: RecordingState) => {
		const request = recordingRequestRef.current + 1;
		recordingRequestRef.current = request;
		try {
			await tallpineBridge.keybindings.setRecording(true);
			if (!activeRef.current || request !== recordingRequestRef.current) {
				await tallpineBridge.keybindings.setRecording(false);
				return;
			}
			setRecordingState(next);
		} catch {
			showToast({
				title: t("shortcut.couldNotRecord"),
				body: t("shortcut.tryReopen"),
			});
		}
	};

	const endRecording = () => {
		recordingRequestRef.current += 1;
		setRecordingState(null);
		void tallpineBridge.keybindings.setRecording(false);
	};

	const applyBinding = async (
		targetId: AppShortcutId,
		candidate: ShortcutBinding,
		mode: RecordingState["mode"],
		conflictingId?: AppShortcutId,
	) => {
		const before = overrides;
		const next: KeybindingOverrides = { ...overrides };
		const currentTarget = effectiveShortcutBindings(targetId, isMac, overrides);
		next[targetId] = mode === "add" ? [...currentTarget, candidate].slice(-2) : [candidate];
		if (conflictingId) {
			next[conflictingId] = effectiveShortcutBindings(conflictingId, isMac, overrides).filter(
				(existing) => !matchesShortcutBinding(candidate, existing),
			);
		}
		await setOverrides(next);
		const targetLabel = shortcutLabel(targetId, t);
		const conflictLabel = conflictingId ? shortcutLabel(conflictingId, t) : undefined;
		showToast({
			title: conflictingId ? t("shortcut.reassigned") : t("shortcut.updated"),
			body: conflictingId
				? t("shortcut.movedBody", {
						binding: shortcutBindingLabel(candidate, isMac),
						from: conflictLabel ?? "",
						to: targetLabel,
					})
				: t("shortcut.updatedBody", { name: targetLabel, binding: shortcutBindingLabel(candidate, isMac) }),
			undo: async () => {
				await setOverrides(before);
				showToast({ title: t("shortcut.undone") });
			},
		});
	};

	useEffect(() => {
		if (!active || !recording) return;
		const handleKeyDown = (event: KeyboardEvent) => {
			event.preventDefault();
			event.stopPropagation();
			if (event.key === "Escape") {
				endRecording();
				return;
			}
			if (event.repeat) return;
			const candidate = eventBinding(event);
			if (!candidate) {
				return;
			}
			const validationError = shortcutBindingValidationError(candidate, isMac);
			if (validationError) {
				showToast({ title: t("shortcut.reserved"), body: validationError });
				return;
			}
			if (
				recording.mode === "add" &&
				effectiveShortcutBindings(recording.id, isMac, overrides).some((existing) =>
					matchesShortcutBinding(candidate, existing),
				)
			) {
				showToast({
					title: t("shortcut.alreadyAssigned"),
					body: t("shortcut.alreadyAvailable", { binding: shortcutBindingLabel(candidate, isMac) }),
				});
				endRecording();
				return;
			}
			const conflicting = APP_SHORTCUTS.find(
				(shortcut) =>
					shortcut.id !== recording.id &&
					effectiveShortcutBindings(shortcut.id, isMac, overrides).some((existing) =>
						matchesShortcutBinding(candidate, existing),
					),
			);
			if (conflicting) {
				if (conflicting.customizable === false) {
					showToast({
						title: t("shortcut.reserved"),
						body: t("shortcut.usedByFixed", { binding: shortcutBindingLabel(candidate, isMac), name: shortcutLabel(conflicting.id, t) }),
					});
					return;
				}
				setConflict({
					targetId: recording.id,
					conflictingId: conflicting.id,
					binding: candidate,
					mode: recording.mode,
				});
				endRecording();
				return;
			}
			void applyBinding(recording.id, candidate, recording.mode).catch(() =>
				showToast({ title: t("shortcut.couldNotUpdate"), body: t("shortcut.previousStillActive") }),
			);
			endRecording();
		};
		window.addEventListener("keydown", handleKeyDown, true);
		return () => window.removeEventListener("keydown", handleKeyDown, true);
	}, [isMac, active, overrides, recording]);

	const handleResetBinding = async (id: AppShortcutId) => {
		const before = overrides;
		await resetBinding(id);
		showToast({
			title: t("shortcut.restored"),
			body: `${definition(id)?.label ?? id} now uses its original binding.`,
			undo: async () => {
				await setOverrides(before);
				showToast({ title: t("shortcut.undone") });
			},
		});
	};

	const handleRemoveBinding = async (id: AppShortcutId, index: number) => {
		const before = overrides;
		const current = effectiveShortcutBindings(id, isMac, overrides);
		const removed = current[index];
		await setOverrides({ ...overrides, [id]: current.filter((_, candidateIndex) => candidateIndex !== index) });
		showToast({
			title: t("shortcut.removed"),
			body: removed ? `${definition(id)?.label ?? id} no longer uses ${shortcutBindingLabel(removed, isMac)}.` : undefined,
			undo: async () => {
				await setOverrides(before);
				showToast({ title: t("shortcut.undone") });
			},
		});
	};

	return {
		overrides,
		recording,
		conflict,
		setConflict,
		toast,
		showToast,
		dismissToast: () => setToast(null),
		beginRecording,
		endRecording,
		applyBinding,
		handleResetBinding,
		handleRemoveBinding,
		resetAll,
	};
}
