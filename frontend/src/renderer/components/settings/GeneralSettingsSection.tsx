import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { previewDocumentThemeStyle, type ThemePreference, type ThemeStyle } from "../../lib/theme";
import { THEMES, getThemeDefinition } from "../../lib/theme-palettes";
import type { AppLocale } from "../../i18n";
import { useLocaleStore } from "../../stores/locale-store";
import { useSoundNotificationsStore } from "../../stores/sound-notifications-store";
import { useUiStore } from "../../stores/ui-store";
import { CliLinkSettings } from "./CliLinkSettingsRow";
import { useTerminalShellStore } from "../../stores/terminal-shell-store";
import { SettingsOptionMenu, type SettingsOption } from "./SettingsOptionMenu";
import { SettingsInputRow, SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";
import { UpdateRow } from "./UpdateRow";
import { AppUpdateRows } from "./AppUpdateRows";
import { DynamicOrchestratorContextRow } from "./DynamicOrchestratorContextRow";
import { ThemePaletteSwatch } from "./ThemePaletteSwatch";
import { Switch } from "../ui/switch";
import { cn } from "../../lib/utils";
import {
	useSettings,
	useUpdateChatHibernation,
	useUpdateSessionInterface,
	useUpdateVersionRefresh,
} from "../../hooks/useSettings";
import type { SessionMode } from "../../types/workspace";
import type { TerminalShellKind } from "../../../shared/ui-locale";
import { isMacPlatform, isWindowsPlatform } from "../../lib/platform";

/**
 * Default interface for new sessions. Daemon-owned so `tallpine spawn` and mobile
 * resolve the same value. Only affects sessions created afterwards — a
 * session's interface is fixed when it is born.
 */
function SessionInterfaceRow() {
	const { t } = useTranslation();
	const { settings, isLoading, error } = useSettings();
	const { update, saving, error: saveError } = useUpdateSessionInterface();
	const interfaceOptions = [
		{ value: "tui", label: t("settings.sessionInterface.terminal") },
		{ value: "chat", label: t("settings.sessionInterface.chat") },
	] satisfies SettingsOption<SessionMode>[];

	const chatAvailable = (settings?.chatHarnesses.length ?? 0) > 0;
	// Silent when everything works; speak up only when the control is limited
	// (no chat-capable agent installed) or a save failed.
	const note = saveError ?? error ?? (!chatAvailable ? t("settings.sessionInterface.unavailable") : null);

	const failed = Boolean(saveError || error);

	return (
		<SettingsRow
			label={t("settings.sessionInterface.label")}
			description={note ? <span className={cn(failed && "text-error")} role={failed ? "alert" : undefined}>{note}</span> : undefined}
		>
			<SettingsOptionMenu
				aria-label={t("settings.sessionInterface.label")}
				value={settings?.defaultSessionMode ?? "tui"}
				options={interfaceOptions}
				onChange={(mode) => update(mode)}
				disabled={isLoading || saving || !chatAvailable}
			/>
		</SettingsRow>
	);
}

/**
 * After a harness update, idle open sessions restart on the new release
 * instead of running the old one until they happen to restart. Daemon-owned:
 * the daemon does the moving.
 */
function VersionRefreshRow() {
	const { t } = useTranslation();
	const { settings, isLoading } = useSettings();
	const { update, saving, error } = useUpdateVersionRefresh();
	return (
		<div className="flex w-full flex-col">
			<SettingsRow className="rounded-none" label={t("settings.versionRefresh.label")} description={t("settings.versionRefresh.description")}>
				<Switch
					aria-label={t("settings.versionRefresh.label")}
					checked={settings?.versionRefresh ?? false}
					disabled={isLoading || saving || !settings}
					onCheckedChange={update}
				/>
			</SettingsRow>
			{error ? (
				<p role="alert" className="px-3 pb-2 text-caption leading-4 text-error">
					{error}
				</p>
			) : null}
		</div>
	);
}

const WORKER_IDLE_PRESETS = [10, 20, 30, 60, 120].map((minutes) => minutes * 60);
const ORCHESTRATOR_IDLE_PRESETS = [30, 60, 120, 240].map((minutes) => minutes * 60);

function idleOptions(presets: number[], current: number | undefined, label: (seconds: number) => string): SettingsOption<string>[] {
	const values = current && !presets.includes(current) ? [...presets, current].sort((a, b) => a - b) : presets;
	return values.map((seconds) => ({ value: String(seconds), label: label(seconds) }));
}

/**
 * Idle Chat agents release their processes (about 400 MB each) and resume
 * their conversation on the next message, report or view. Daemon-owned: the
 * daemon decides when a session is quiet enough and never stops one with work
 * still running. A project can opt out in its own settings.
 */
function ChatHibernationRows() {
	const { t } = useTranslation();
	const { settings, isLoading } = useSettings();
	const { update, saving, error } = useUpdateChatHibernation();
	const policy = settings?.chatHibernation;
	const duration = (seconds: number) =>
		seconds % 3600 === 0
			? t("settings.chatHibernation.hours", { count: seconds / 3600 })
			: t("settings.chatHibernation.minutes", { count: Math.round(seconds / 60) });
	const disabled = isLoading || saving || !policy;
	return (
		<div className="flex w-full flex-col">
			<SettingsRow className="rounded-none" label={t("settings.chatHibernation.label")} description={t("settings.chatHibernation.description")}>
				<Switch
					aria-label={t("settings.chatHibernation.label")}
					checked={policy?.enabled ?? false}
					disabled={disabled}
					onCheckedChange={(enabled) => update({ enabled })}
				/>
			</SettingsRow>
			{policy?.enabled ? (
				<>
					<SettingsRow className="rounded-none" label={t("settings.chatHibernation.workerIdle")}>
						<SettingsOptionMenu
							aria-label={t("settings.chatHibernation.workerIdle")}
							value={String(policy.workerIdleSeconds)}
							options={idleOptions(WORKER_IDLE_PRESETS, policy.workerIdleSeconds, duration)}
							onChange={(value) => update({ workerIdleSeconds: Number(value) })}
							disabled={disabled}
						/>
					</SettingsRow>
					<SettingsRow className="rounded-none" label={t("settings.chatHibernation.orchestratorIdle")}>
						<SettingsOptionMenu
							aria-label={t("settings.chatHibernation.orchestratorIdle")}
							value={String(policy.orchestratorIdleSeconds)}
							options={idleOptions(ORCHESTRATOR_IDLE_PRESETS, policy.orchestratorIdleSeconds, duration)}
							onChange={(value) => update({ orchestratorIdleSeconds: Number(value) })}
							disabled={disabled}
						/>
					</SettingsRow>
				</>
			) : null}
			{error ? (
				<p role="alert" className="px-3 pb-2 text-caption leading-4 text-error">
					{error}
				</p>
			) : null}
		</div>
	);
}

function TerminalShellRows() {
	const { t } = useTranslation();
	const preference = useTerminalShellStore((state) => state.preference);
	const load = useTerminalShellStore((state) => state.load);
	const setPreference = useTerminalShellStore((state) => state.setPreference);
	const saving = useTerminalShellStore((state) => state.saving);
	const saveError = useTerminalShellStore((state) => state.saveError);
	const [customPath, setCustomPath] = useState(preference.path ?? "");

	useEffect(() => {
		void load();
	}, [load]);

	useEffect(() => {
		setCustomPath(preference.path ?? "");
	}, [preference.path]);

	const shellOptions = [
		{ value: "auto", label: t("settings.terminalShell.auto") },
		{ value: "git-bash", label: t("settings.terminalShell.gitBash") },
		{ value: "pwsh", label: t("settings.terminalShell.pwsh") },
		{ value: "powershell", label: t("settings.terminalShell.windowsPowerShell") },
		{ value: "cmd", label: t("settings.terminalShell.cmd") },
		{ value: "custom", label: t("settings.terminalShell.custom") },
	] satisfies SettingsOption<TerminalShellKind>[];

	return (
		<>
			<SettingsRow
				label={t("settings.terminalShell.label")}
				description={saveError ? <span role="alert" className="text-error">{t("settings.terminalShell.saveFailed")}</span> : undefined}
			>
				<SettingsOptionMenu
					aria-label={t("settings.terminalShell.label")}
					value={preference.kind}
					options={shellOptions}
					disabled={saving}
					onChange={(kind) => {
						void setPreference(kind === "custom" ? { kind, path: customPath } : { kind });
					}}
				/>
			</SettingsRow>
			{preference.kind === "custom" ? (
				<SettingsInputRow
					id="terminal-shell-custom-path"
					label={t("settings.terminalShell.customPath")}
					value={customPath}
					onChange={setCustomPath}
					onCommit={(path) => void setPreference({ kind: "custom", path })}
					onCancel={() => setCustomPath(preference.path ?? "")}
					placeholder={t("settings.terminalShell.customPathPlaceholder")}
				/>
			) : null}
		</>
	);
}

const COLOR_THEME_OPTIONS = THEMES.map(({ id, label }) => ({ value: id, label })) satisfies SettingsOption<ThemeStyle>[];

export function GeneralSettingsSection({
	titleHidden,
}: {
	titleHidden?: boolean;
}) {
	const { t } = useTranslation();
	const themePreference = useUiStore((state) => state.themePreference);
	const setThemePreference = useUiStore((state) => state.setThemePreference);
	const themeStyle = useUiStore((state) => state.themeStyle);
	const setThemeStyle = useUiStore((state) => state.setThemeStyle);
	const resolvedTheme = useUiStore((state) => state.resolvedTheme);
	const locale = useLocaleStore((state) => state.locale);
	const setLocale = useLocaleStore((state) => state.setLocale);
	const localeSaving = useLocaleStore((state) => state.saving);
	const localeSaveError = useLocaleStore((state) => state.saveError);
	const soundNotificationsEnabled = useSoundNotificationsStore((state) => state.enabled);
	const setSoundNotificationsEnabled = useSoundNotificationsStore((state) => state.setEnabled);
	const soundNotificationsSaving = useSoundNotificationsStore((state) => state.saving);
	const soundNotificationsSaveError = useSoundNotificationsStore((state) => state.saveError);
	const terminalCopyOnSelect = useUiStore((state) => state.terminalCopyOnSelect);
	const setTerminalCopyOnSelect = useUiStore((state) => state.setTerminalCopyOnSelect);

	// Trigger and menu rows share one layout: name, then the palette on the right.
	const renderColorTheme = (option: SettingsOption<ThemeStyle> | undefined, compact = false) =>
		option ? (
			<>
				<span className="min-w-0 flex-1 truncate">{option.label}</span>
				<ThemePaletteSwatch palette={getThemeDefinition(option.value)[resolvedTheme]} compact={compact} />
			</>
		) : null;

	const themeOptions = [
		{ value: "light", label: t("settings.theme.light") },
		{ value: "dark", label: t("settings.theme.dark") },
		{ value: "system", label: t("settings.theme.system") },
	] satisfies SettingsOption<ThemePreference>[];

	const languageOptions = [
		{ value: "en", label: t("settings.language.en") },
		{ value: "zh-CN", label: t("settings.language.zhCN") },
		{ value: "ja", label: t("settings.language.ja") },
		{ value: "ko", label: t("settings.language.ko") },
		{ value: "es", label: t("settings.language.es") },
		{ value: "fr", label: t("settings.language.fr") },
		{ value: "de", label: t("settings.language.de") },
		{ value: "pt-BR", label: t("settings.language.ptBR") },
	] satisfies SettingsOption<AppLocale>[];

	return (
		<>
			{/* Appearance */}
			<SettingsSection title={t("settings.appearance")} titleHidden={titleHidden} grouped>
				<SettingsRow label={t("settings.theme")}>
					<div className="flex items-center gap-1.5">
						<SettingsOptionMenu
							aria-label={t("settings.colorTheme")}
							value={themeStyle}
							options={COLOR_THEME_OPTIONS}
							onChange={setThemeStyle}
							onHighlight={(previewed) => previewDocumentThemeStyle(previewed, useUiStore.getState().themeStyle)}
							menuClassName="min-w-60"
							renderTrigger={(selected) => renderColorTheme(selected, true)}
							renderMenuItem={(option) => renderColorTheme(option)}
						/>
						<SettingsOptionMenu
							aria-label={t("settings.theme")}
							value={themePreference}
							options={themeOptions}
							onChange={setThemePreference}
						/>
					</div>
				</SettingsRow>
				{isMacPlatform() && <WindowTranslucencyRow />}
				<SettingsRow
					label={t("settings.language")}
					description={localeSaveError ? <span role="alert" className="text-error">{t("settings.language.saveFailed")}</span> : undefined}
				>
					<SettingsOptionMenu
						aria-label={t("settings.language")}
						disabled={localeSaving}
						value={locale}
						options={languageOptions}
						onChange={(next) => {
							void setLocale(next);
						}}
					/>
				</SettingsRow>
			</SettingsSection>

			{/* Sessions */}
			<SettingsSection title={t("settings.sessions")} grouped>
				<SessionInterfaceRow />
				<VersionRefreshRow />
				<DynamicOrchestratorContextRow />
				<ChatHibernationRows />
				{isWindowsPlatform() ? <TerminalShellRows /> : null}
				<SettingsRow label={t("settings.terminalCopyOnSelect")}>
					<Switch
						aria-label={t("settings.terminalCopyOnSelect")}
						checked={terminalCopyOnSelect}
						onCheckedChange={setTerminalCopyOnSelect}
					/>
				</SettingsRow>
				<SettingsRow
					label={t("settings.soundNotifications")}
					description={soundNotificationsSaveError ? <span role="alert" className="text-error">{t("settings.soundNotifications.saveFailed")}</span> : undefined}
				>
					<Switch
						aria-label={t("settings.soundNotifications")}
						checked={soundNotificationsEnabled}
						disabled={soundNotificationsSaving}
						onCheckedChange={(next) => {
							void setSoundNotificationsEnabled(next);
						}}
					/>
				</SettingsRow>
			</SettingsSection>


			{/* Updates */}
			<SettingsSection title={t("settings.updates", "Updates")} grouped>
				<AppUpdateRows />
				<UpdateRow />
			</SettingsSection>

			{/* Advanced */}
			<SettingsSection title={t("settings.advanced")} grouped>
				<DiagnosticsRow />
				<CliLinkSettings />
				<SideChatsRow />
			</SettingsSection>
		</>
	);
}

/**
 * Toggle for memory and CPU monitoring: the card chips, the board's memory
 * light and window, and the Diagnostics settings page. Off by default so it
 * does not poll process trees.
 */
function DiagnosticsRow() {
	const { t } = useTranslation();
	const diagnostics = useUiStore((state) => state.diagnostics);
	const setDiagnostics = useUiStore((state) => state.setDiagnostics);
	return (
		<SettingsRow label={t("settings.diagnostics")} description={t("settings.diagnosticsToggleHint")}>
			<Switch aria-label={t("settings.diagnostics")} checked={diagnostics} onCheckedChange={setDiagnostics} />
		</SettingsRow>
	);
}

/**
 * Developer Mode-only toggle for side chats. Renderer state like Diagnostics:
 * off hides the inspector tab, /btw and /side, and "Add to side chat".
 */
function SideChatsRow() {
	const { t } = useTranslation();
	const sideChats = useUiStore((state) => state.sideChats);
	const setSideChats = useUiStore((state) => state.setSideChats);
	return (
		<SettingsRow label={t("settings.sideChats")} description={t("settings.sideChatsHint")}>
			<Switch aria-label={t("settings.sideChats")} checked={sideChats} onCheckedChange={setSideChats} />
		</SettingsRow>
	);
}

/**
 * macOS Appearance toggle for window translucency: native
 * vibrancy blurs the desktop behind a translucent shell. Renderer state.
 */
function WindowTranslucencyRow() {
	const { t } = useTranslation();
	const windowTranslucency = useUiStore((state) => state.windowTranslucency);
	const setWindowTranslucency = useUiStore((state) => state.setWindowTranslucency);
	return (
		<SettingsRow label={t("settings.windowTranslucency")} description={t("settings.windowTranslucencyHint")}>
			<Switch
				aria-label={t("settings.windowTranslucency")}
				checked={windowTranslucency}
				onCheckedChange={setWindowTranslucency}
			/>
		</SettingsRow>
	);
}
