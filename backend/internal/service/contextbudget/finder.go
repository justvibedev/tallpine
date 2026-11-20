package contextbudget

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// Measurement is everything one daily check read for a project.
type Measurement struct {
	Budget       int
	MemoryBudget int
	// Groups are the instruction files per set of harnesses that load the same
	// ones; Total is the largest group, the worst case a session sees.
	Groups []group
	Total  int
	Skills []File
	// Memory is nil when the project has no Claude orchestrator memory.
	Memory      *Memory
	Prompts     []PromptSize
	Calibration []Calibration
}

// PromptSize is the size of the Tallpine system prompt for one role and harness.
type PromptSize struct {
	Kind    domain.SessionKind
	Harness domain.AgentHarness
	Tokens  int
}

// Calibration is the real first-turn input of a harness's recent sessions,
// beside what the files estimate.
type Calibration struct {
	Harness domain.AgentHarness
	Samples int
	Median  int
}

// Finder turns a measurement into findings. Only Kind, Fingerprint, Evidence,
// MeasuredTokens and BudgetTokens of the result are read; the sweep owns the rest.
type Finder interface {
	Find(ctx context.Context, proj domain.ProjectRecord, m Measurement) ([]domain.ContextHealthFinding, error)
}

// budgetFinder reports the instruction files and the memory index when they
// pass their budgets. The Tallpine prompt is evidence only: the owner cannot
// trim it from the repository, so it never gets a card of its own.
type budgetFinder struct{}

// BudgetFinder is the finder for the instruction-file and memory budgets.
func BudgetFinder() Finder { return budgetFinder{} }

func (budgetFinder) Find(_ context.Context, _ domain.ProjectRecord, m Measurement) ([]domain.ContextHealthFinding, error) {
	var out []domain.ContextHealthFinding
	if m.Total > m.Budget {
		out = append(out, domain.ContextHealthFinding{
			Kind: domain.ContextInstructionBudget, Fingerprint: string(domain.ContextInstructionBudget),
			MeasuredTokens: m.Total, BudgetTokens: m.Budget, Evidence: instructionEvidence(m),
		})
	}
	if m.Memory != nil && m.Memory.IndexTokens > m.MemoryBudget {
		out = append(out, domain.ContextHealthFinding{
			Kind: domain.ContextMemoryBudget, Fingerprint: string(domain.ContextMemoryBudget),
			MeasuredTokens: m.Memory.IndexTokens, BudgetTokens: m.MemoryBudget, Evidence: memoryEvidence(m),
		})
	}
	return out, nil
}

func k(tokens int) string { return fmt.Sprintf("~%.1fK", float64(tokens)/1000) }

func instructionEvidence(m Measurement) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Always-loaded instruction files are %s tokens, over the %s budget; every session of this project pays that on start.\n", k(m.Total), k(m.Budget))
	b.WriteString("\n## Instruction files\n")
	for _, g := range m.Groups {
		names := make([]string, len(g.harnesses))
		for i, h := range g.harnesses {
			names[i] = string(h)
		}
		fmt.Fprintf(&b, "%s: %s tokens\n", strings.Join(names, ", "), k(g.Tokens))
		for _, f := range g.Files {
			fmt.Fprintf(&b, "- %s: %s (%d bytes)\n", f.Path, k(f.Tokens), f.Bytes)
		}
		for _, sec := range g.Sections {
			fmt.Fprintf(&b, "- biggest section: %s \"%s\" %s\n", sec.File, sec.Heading, k(sec.Tokens))
		}
	}
	sharedEvidence(&b, m)
	return b.String()
}

func memoryEvidence(m Measurement) string {
	mem := m.Memory
	var b strings.Builder
	fmt.Fprintf(&b, "The Claude orchestrator memory index (MEMORY.md) is %s tokens, over the %s budget; the orchestrator loads it on every start.\n", k(mem.IndexTokens), k(m.MemoryBudget))
	fmt.Fprintf(&b, "\n## Memory\n- directory: %s\n- MEMORY.md: %s (%d bytes)\n- %d entry files: %s tokens in total (read on demand)\n", mem.Dir, k(mem.IndexTokens), mem.IndexBytes, mem.Files, k(mem.DirTokens))
	sharedEvidence(&b, m)
	return b.String()
}

// sharedEvidence adds what every finding shows beside its own numbers: skills,
// the Tallpine prompt's share, and the real first-turn input.
func sharedEvidence(b *strings.Builder, m Measurement) {
	if len(m.Skills) > 0 {
		total := 0
		for _, f := range m.Skills {
			total += f.Tokens
		}
		fmt.Fprintf(b, "\n## Skills\n%d skills, %s tokens of names and descriptions\n", len(m.Skills), k(total))
		for _, f := range m.Skills[:min(3, len(m.Skills))] {
			fmt.Fprintf(b, "- %s: %s\n", f.Path, k(f.Tokens))
		}
	}
	if len(m.Prompts) > 0 {
		b.WriteString("\n## Tallpine system prompt\n")
		for _, p := range m.Prompts {
			fmt.Fprintf(b, "- %s on %s: %s tokens%s\n", p.Kind, p.Harness, k(p.Tokens), promptShare(m, p))
		}
		b.WriteString("Tallpine's own prompt is measured here as context only; it is not part of this finding.\n")
	}
	if len(m.Calibration) > 0 {
		b.WriteString("\n## Calibration (real first-turn input)\n")
		for _, c := range m.Calibration {
			fmt.Fprintf(b, "- %s: median %s tokens over %d recent sessions (the files above are estimated at ~3 bytes per token)\n", c.Harness, k(c.Median), c.Samples)
		}
	}
}

// promptShare is the Tallpine prompt's part of prompt + instruction files for
// the harness, as " (NN% of prompt plus instruction files)".
func promptShare(m Measurement, p PromptSize) string {
	for _, g := range m.Groups {
		for _, h := range g.harnesses {
			if h == p.Harness && p.Tokens+g.Tokens > 0 {
				return fmt.Sprintf(" (%d%% of prompt plus instruction files)", p.Tokens*100/(p.Tokens+g.Tokens))
			}
		}
	}
	return ""
}

// median of v, 0 for none.
func median(v []int64) int {
	if len(v) == 0 {
		return 0
	}
	s := append([]int64(nil), v...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return int(s[len(s)/2])
}
