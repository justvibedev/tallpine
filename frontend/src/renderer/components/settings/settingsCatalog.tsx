import { Activity, BadgeCheck, Bot, ClipboardCheck, ShieldCheck, CircleHelp, Globe2, HardDrive, Keyboard, Settings2, Smartphone, type LucideIcon } from "lucide-react";
import { lazy, type ReactNode } from "react";
import type { TFunction } from "i18next";
import { agentLabel } from "../../lib/agent-options";
import type { GlobalSettingsSection, HarnessDiagnosticsRequest } from "../../stores/settings-modal";
import { AgentAccountsPanel } from "./AgentAccountsSection";
import { AccountPoolSection } from "./AccountPoolSection";
import { AgentPolicySection } from "./AgentPolicySection";
import { BrowserDownloadsSection } from "./BrowserDownloadsSection";
import { BrowserProfilesSection } from "./BrowserProfilesSection";
import { CodexAccountsSection } from "./CodexAccountsSection";
import { ConnectMobileContent } from "./ConnectMobileContent";
import { SectionErrorBoundary } from "./SectionErrorBoundary";
import { GeneralSettingsSection } from "./GeneralSettingsSection";
import { HarnessSettingsSection } from "./HarnessSettingsSection";
import { ImportFromOfficialSection } from "./ImportFromOfficialSection";
import { KeyboardShortcutsContent } from "./KeyboardShortcutsContent";
import { MobileDevicesSection } from "./MobileDevicesSection";
import { ReviewsSettingsSection } from "./ReviewsSettingsSection";
import { ReportProblemContent } from "./ReportProblemContent";
import { RemoteHostsSettings } from "./RemoteHostsSettings";
import { SettingsSection } from "./SettingsSection";
import { RetentionSection } from "./RetentionSection";
import { StorageSection } from "./StorageSection";
import { ArtifactStorageRow } from "./ArtifactStorageRow";
import { StorageCompactionRows } from "./StorageCompactionRows";

/** The memory window's own blocks, loaded only when the page is opened: it
 * samples the machine every two seconds while it is on screen. */
const MemoryDiagnostics = lazy(async () => {
	const module = await import("../session-memory/SessionMemoryDiagnostics");
	return { default: module.MemoryDiagnostics };
});


type CatalogContext = {
	/** The Diagnostics toggle is on — gates the memory and CPU page. */
	diagnostics: boolean;
	focusAgentId?: string;
	hostId?: string;
	startLogin?: boolean;
	showDiagnostics?: HarnessDiagnosticsRequest;
	/** One page is shown, under its own title: a first heading that only
	 * rewords that title ("Keyboard shortcuts" on Shortcuts) is hidden. */
	singlePage?: boolean;
};

export type SettingsCatalogItem = {
	id: GlobalSettingsSection;
	icon: LucideIcon;
	label: (t: TFunction) => string;
	visible?: (context: CatalogContext) => boolean;
	/** Left out of the single-page "all" view; it has its own page in the nav.
	 * Diagnostics is a live monitor, not a preference: rendering it inside the
	 * whole-settings page would sample the machine whenever settings opens. */
	pageOnly?: boolean;
	render: (t: TFunction, titleHidden: boolean, context: CatalogContext) => ReactNode;
};

const globalSettingsCatalog: SettingsCatalogItem[] = [
	{
		id: "general",
		icon: Settings2,
		label: (t) => t("settings.general"),
		render: (_t, titleHidden) => (
			<>
				<GeneralSettingsSection titleHidden={titleHidden} />
				<ImportFromOfficialSection />
			</>
		),
	},
	{
		id: "harness",
		icon: Bot,
		label: (t) => t("settings.harness"),
		render: (_t, titleHidden, { focusAgentId, hostId, startLogin, showDiagnostics }) => (
			<>
				<HarnessSettingsSection focusAgentId={focusAgentId} {...(hostId ? { hostId } : {})} {...(startLogin ? { startLogin } : {})} {...(showDiagnostics ? { showDiagnostics } : {})} titleHidden={titleHidden} />
				<AgentPolicySection />
			</>
		),
	},
	{
		id: "reviews",
		icon: ClipboardCheck,
		label: (t) => t("settings.reviews"),
		render: () => <ReviewsSettingsSection />,
	},
	{
		id: "security",
		icon: ShieldCheck,
		label: (t) => t("settings.security"),
		render: () => <AgentPolicySection scope="security" />,
	},
	{
		id: "agents",
		icon: BadgeCheck,
		label: (t) => t("settings.agents"),
		render: (_t, titleHidden) => (
			<>
				<CodexAccountsSection titleHidden={titleHidden} />
				<SettingsSection title={agentLabel("claude-code")} sectionId="claudeAccounts">
					<AgentAccountsPanel provider="claude" />
				</SettingsSection>
				<AccountPoolSection />
			</>
		),
	},
	{
		id: "browserProfiles",
		icon: Globe2,
		label: (t) => t("settings.browserProfiles"),
		render: (_t, titleHidden) => (
			<>
				<BrowserProfilesSection titleHidden={titleHidden} />
				<BrowserDownloadsSection />
			</>
		),
	},
	{
		id: "connections",
		icon: Smartphone,
		label: (t) => t("settings.connections"),
		// Phones that control this computer, then the other computers this one controls.
		render: (t) => (
			<>
				<SettingsSection title={t("settings.connections.phones")} grouped>
					{/* Its own card rows: the pairing content row, then Keep awake. */}
					<SectionErrorBoundary>
						<ConnectMobileContent active />
					</SectionErrorBoundary>
				</SettingsSection>
				{/* Its own section (and card) when phones are paired; renders nothing otherwise. */}
				<MobileDevicesSection />
				<RemoteHostsSettings />
			</>
		),
	},
	{
		id: "storage",
		icon: HardDrive,
		label: (t) => t("settings.storageUsage.title"),
		// Measuring walks the disk, so it has its own page rather than running
		// whenever settings opens.
		pageOnly: true,
		render: (t, titleHidden) => (
			<>
				<StorageSection titleHidden={titleHidden} />
				<SettingsSection title={t("settings.storage")} grouped>
					<StorageCompactionRows />
					<ArtifactStorageRow />
				</SettingsSection>
				<RetentionSection />
			</>
		),
	},
	{
		id: "shortcuts",
		icon: Keyboard,
		label: (t) => t("settings.shortcuts"),
		render: (_t, titleHidden, { singlePage }) => <KeyboardShortcutsContent active titleHidden={titleHidden || Boolean(singlePage)} />,
	},
	{
		id: "diagnostics",
		icon: Activity,
		label: (t) => t("settings.diagnostics"),
		pageOnly: true,
		visible: ({ diagnostics }) => diagnostics,
		render: (t, titleHidden) => (
			<SettingsSection titleHidden={titleHidden} title={t("settings.diagnostics")}>
				<MemoryDiagnostics />
			</SettingsSection>
		),
	},
	{
		id: "help",
		icon: CircleHelp,
		label: (t) => t("settings.help"),
		render: (t, titleHidden) => (
			<SettingsSection titleHidden={titleHidden} title={t("settings.reportProblem")} grouped>
				<div className="settings-content-row">
					<ReportProblemContent active />
				</div>
			</SettingsSection>
		),
	},
];

export function visibleGlobalSettings(context: CatalogContext): SettingsCatalogItem[] {
	return globalSettingsCatalog.filter((item) => item.visible?.(context) ?? true);
}

export function globalSettingsItem(section: GlobalSettingsSection, context: CatalogContext): SettingsCatalogItem {
	return visibleGlobalSettings(context).find((item) => item.id === section) ?? globalSettingsCatalog[0];
}

export function globalSettingsItemsFor(section: GlobalSettingsSection | "all", context: CatalogContext): SettingsCatalogItem[] {
	return section === "all"
		? visibleGlobalSettings(context).filter((item) => !item.pageOnly)
		: [globalSettingsItem(section, context)];
}
