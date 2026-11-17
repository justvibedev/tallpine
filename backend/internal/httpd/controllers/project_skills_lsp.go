package controllers

import (
	"runtime"

	"tallpine.eu/tallpine/backend/internal/skillbridge"
)

// ProjectLSPRecommendation is the Claude Code LSP plugin matching one of the
// repo's main languages. Nothing is enabled from here; the install
// routes (project_skills_lsp_install.go) install only on an explicit click.
type ProjectLSPRecommendation struct {
	Language string `json:"language"`
	// Files is the number of tracked source files of the language.
	Files int `json:"files"`
	// PluginID is the enabledPlugins key the per-role switches write.
	PluginID        string `json:"pluginId"`
	PluginInstalled bool   `json:"pluginInstalled"`
	// PluginInstall is the command that installs the plugin.
	PluginInstall string `json:"pluginInstall"`
	Binary        string `json:"binary"`
	// BinaryInstalled is whether the language server is found on PATH or in the usual tool dirs.
	BinaryInstalled bool `json:"binaryInstalled"`
	// BinaryProblem is a code for why an installed language server cannot work: "no-go", "no-tsserver", or "" when fine.
	BinaryProblem string `json:"binaryProblem"`
	// BinaryInstall is the command or note that installs the language server.
	BinaryInstall string `json:"binaryInstall"`
	// BinaryInstallable is whether Tallpine can run the install itself.
	BinaryInstallable bool `json:"binaryInstallable"`
	// ToolchainInstall is the command that installs the Go toolchain when BinaryProblem is "no-go", else "".
	ToolchainInstall string `json:"toolchainInstall"`
	// ToolchainInstallable is whether Tallpine can run that install itself.
	ToolchainInstallable bool `json:"toolchainInstallable"`
	// Roles are the plugin's per-role switches (claudePlugins).
	Roles []ProjectSkillRole `json:"roles"`
}

// ProjectLSPMismatch is an LSP plugin that is switched on for some role but
// matches no main language of the repo.
type ProjectLSPMismatch struct {
	PluginID string `json:"pluginId"`
	Language string `json:"language"`
	// Roles are the roles the plugin is currently on for.
	Roles []string `json:"roles"`
}

// ProjectLSP is the project's LSP plugin advice.
type ProjectLSP struct {
	Recommendations []ProjectLSPRecommendation `json:"recommendations"`
	Mismatches      []ProjectLSPMismatch       `json:"mismatches"`
}

// lspAdvice detects the repo's main languages and compares them with the
// account's LSP plugins and the project's per-role switches.
func lspAdvice(path, home string, plugins []skillbridge.Plugin, claudePlugins map[string]map[string]bool, harnesses []string, detect func(string) []skillbridge.LangCount, binary func(home, name string) bool, problem func(home string, l skillbridge.LSPLanguage) string) ProjectLSP {
	out := ProjectLSP{Recommendations: []ProjectLSPRecommendation{}, Mismatches: []ProjectLSPMismatch{}}
	byName := map[string]skillbridge.Plugin{}
	for _, p := range plugins {
		if _, ok := skillbridge.LSPByPluginName(p.Name); ok {
			byName[p.Name] = p
		}
	}
	main := map[string]bool{}
	for _, lc := range detect(path) {
		main[lc.Language] = true
		l, _ := skillbridge.LSPByLanguage(lc.Language)
		pl, found := byName[l.Plugin]
		if !found {
			pl = skillbridge.Plugin{ID: l.PluginID(), Name: l.Plugin, Marketplace: skillbridge.LSPMarketplace}
		}
		rec := ProjectLSPRecommendation{
			Language: l.Language, Files: lc.Files, PluginID: pl.ID, PluginInstalled: pl.Installed, PluginInstall: l.PluginInstall(),
			Binary: l.Binary, BinaryInstalled: binary(home, l.Binary), BinaryProblem: problem(home, l), BinaryInstall: l.BinaryInstall, BinaryInstallable: len(l.BinaryCommands) > 0,
			Roles: pluginRoles(claudePlugins, pl, harnesses),
		}
		if rec.BinaryProblem == skillbridge.LSPProblemNoGo {
			install, cmds := skillbridge.GoToolchainInstall(runtime.GOOS)
			rec.ToolchainInstall, rec.ToolchainInstallable = install, len(cmds) > 0
		}
		out.Recommendations = append(out.Recommendations, rec)
	}
	for _, l := range skillbridge.LSPCatalog {
		pl, found := byName[l.Plugin]
		if !found || main[l.Language] {
			continue
		}
		var on []string
		for _, r := range pluginRoles(claudePlugins, pl, harnesses) {
			if r.On {
				on = append(on, r.Role)
			}
		}
		if len(on) > 0 {
			out.Mismatches = append(out.Mismatches, ProjectLSPMismatch{PluginID: pl.ID, Language: l.Language, Roles: on})
		}
	}
	return out
}
