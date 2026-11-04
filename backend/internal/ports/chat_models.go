package ports

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// ChatModel is one model the provider offers for a conversation.
//
// The list comes from the provider, never from a table in Tallpine. A hardcoded catalog
// is wrong within a week: models are added, renamed, hidden per account, and
// gated by entitlement the provider knows about and Tallpine does not.
type ChatModel struct {
	Inputs      []string
	ID          string
	DisplayName string
	Description string
	// Default marks the model the provider would pick on its own.
	Default bool
	// Efforts are the reasoning levels this model supports, in the provider's
	// order. Empty means the model does not take one.
	Efforts []string
	// DefaultEffort is the level the provider uses when none is chosen.
	DefaultEffort string
	// FastTier is the provider's id for this model's faster tier. Empty means
	// the model has none.
	FastTier string
}

// ChatModelLister is implemented by drivers whose provider can enumerate models.
// Optional: a driver without it simply offers no choice, and the UI shows none.
type ChatModelLister interface {
	ListModels(ctx context.Context) ([]ChatModel, error)
}

// ChatConfigOptionType is the interaction an advertised provider setting needs.
// ACP currently standardizes selects and is incubating booleans; keeping both in
// Tallpine's vocabulary prevents protocol DTOs from leaking out of the adapter.
type ChatConfigOptionType string

const (
	// ChatConfigOptionSelect offers one value from provider-owned choices.
	ChatConfigOptionSelect ChatConfigOptionType = "select"
	// ChatConfigOptionBoolean offers a native on/off control.
	ChatConfigOptionBoolean ChatConfigOptionType = "boolean"
)

// ChatConfigOptionValue is the current or requested value for one provider
// setting. Exactly one field is meaningful according to the option's Type.
type ChatConfigOptionValue struct {
	Select  string
	Boolean *bool
}

// ChatConfigOptionChoice is one value in a select. Group fields preserve an
// agent's organization without making grouped menus a protocol concern above
// the adapter.
type ChatConfigOptionChoice struct {
	PermissionMode PermissionMode
	Value          string
	Name           string
	Description    string
	Group          string
	GroupName      string
}

// ChatConfigOption is one live provider-owned session control.
//
// Category is a presentation hint (model, thought_level, mode, or a provider
// extension), never a correctness discriminator. Unknown categories must still
// render: that is how a newly released agent feature reaches Tallpine without a Tallpine
// release adding a new hardcoded setting.
type ChatConfigOption struct {
	ID          string
	Name        string
	Description string
	Category    string
	Type        ChatConfigOptionType
	Current     ChatConfigOptionValue
	Choices     []ChatConfigOptionChoice
}

// ChatConfigOptionController is implemented by conversations whose provider
// exposes session configuration. Set returns the complete post-change catalog:
// changing a model can add or remove effort and fast-mode choices, so callers
// must replace rather than patch their cached list.
type ChatConfigOptionController interface {
	ListConfigOptions(ctx context.Context) ([]ChatConfigOption, error)
	SetConfigOption(ctx context.Context, id string, value ChatConfigOptionValue) ([]ChatConfigOption, error)
}

// ChatUsage is token accounting for a conversation.
//
// ContextWindow and ContextUsed are what make a "how full is this conversation"
// readout possible; without the window the used figure is a number with no scale.
type ChatUsage struct {
	InputTokens  int64
	OutputTokens int64
	CachedTokens int64
	TotalTokens  int64
	// ContextUsed and ContextWindow are the conversation's position in the model's
	// context, not a running total of the session's spend.
	ContextUsed   int64
	ContextWindow int64
	// Providers may report context occupancy and cumulative accounting in
	// separate notifications. These flags distinguish an omitted group from a
	// meaningful zero (for example, context immediately after compaction).
	ContextKnown bool
	TotalsKnown  bool
	// Cost is cumulative provider-reported spend in Currency. Nil means the
	// provider did not report money (common for subscription-backed sessions).
	Cost     *float64
	Currency string
	// CostUnknown withdraws any cost reported earlier: some of the spend can no
	// longer be priced (a request on a model without a known price), and the
	// earlier figure would understate it.
	CostUnknown bool
	// ModelUsage is the spend of one finished turn by model: an increment the
	// consumer adds to its totals, unlike the cumulative fields above.
	ModelUsage []domain.ModelUsage
}

// ChatRateLimits is the account's quota position.
//
// Modelled because it explains a class of failure the user cannot otherwise
// diagnose: a turn that fails for a reason unrelated to what they asked.
type ChatRateLimits struct {
	// PrimaryUsedPercent and SecondaryUsedPercent are the provider's own windows
	// (typically a short and a long one). Negative means not reported.
	PrimaryUsedPercent   float64
	SecondaryUsedPercent float64
	// PrimaryResetsInSeconds is how long until the tighter window refills.
	PrimaryResetsInSeconds   int64
	SecondaryResetsInSeconds int64
	// PlanLabel is the provider's name for the account tier, when it says.
	PlanLabel string
	// CodexCapacity carries the normalized full/sparse provider observation to
	// the daemon-owned account coordinator. It is never persisted with the
	// conversation quota projection.
	CodexCapacity *CodexCapacityObservation
}

// ChatTurnDiff is the running diff of what a turn changed on disk.
type ChatTurnDiff struct {
	Files []ChatDiffFile
}

// ChatDiffFile is one changed path in a turn diff.
type ChatDiffFile struct {
	Path      string
	Additions int
	Deletions int
	// Status is the provider's change kind: added, modified, deleted, renamed.
	Status string
	// OldPath is set for a rename.
	OldPath string
	// Patch is the unified diff for this file when the provider sends one.
	Patch string
}

// ChatModelReroute is the provider answering with a model other than the one that
// was asked for.
type ChatModelReroute struct {
	FromModel string
	ToModel   string
	// Reason is the provider's own word for why, carried verbatim.
	Reason string
}

// ChatAccount is what the provider says about the account a conversation runs
// under.
type ChatAccount struct {
	AuthMode  string
	PlanLabel string
	// ReauthRequired means the provider asked for credentials the client is
	// expected to supply. Tallpine does not hold provider credentials, so this is
	// reported to the user rather than answered.
	ReauthRequired bool
	// ReauthRecovered reports provider recovery intent. The daemon requires a
	// correlated authoritative turn completion before clearing a demand; an
	// uncorrelated account report alone is not authentication evidence.
	ReauthRecovered bool
	// ReauthReason is the provider's stated reason, e.g. "unauthorized".
	ReauthReason string
}

// ChatThreadState is the provider's lifecycle view of the thread.
type ChatThreadState struct {
	// Status is Tallpine's neutral spelling of the provider's state.
	Status domain.ThreadStatus
	// WaitingOn are the provider's active flags in Tallpine's spelling.
	WaitingOn []string
	// Archived is tri-state: nil means this report says nothing about archiving,
	// which is different from saying it is not archived.
	Archived *bool
	// Closed is set only by a report that the thread was closed.
	Closed bool
	// Compaction, when set, reports a context compaction starting or ending. It
	// says nothing about status or archiving.
	Compaction *ChatCompaction
}

// ChatCompaction is one lifecycle report of a context compaction.
type ChatCompaction struct {
	// ID is the provider's id for this compaction; reports sharing it describe
	// the same one.
	ID     string
	Status domain.CompactionStatus
	// Trigger is "auto" or "manual"; empty when the provider did not say.
	Trigger    string
	PreTokens  int64
	PostTokens int64
}

// ChatMCPServer is one tool server's startup state.
type ChatMCPServer struct {
	Name          string
	Status        string
	Error         string
	FailureReason string
}

// ChatMCPReloadResult distinguishes a complete post-reload inventory from a
// reload whose inventory could not be read. An authoritative empty inventory is
// meaningful: it says every previously known server was disabled or removed.
type ChatMCPReloadResult struct {
	Servers       []ChatMCPServer
	Authoritative bool
}

// ChatSkill is one capability the provider exposes to the agent, which a user can
// invoke by name.
type ChatSkill struct {
	Name        string
	DisplayName string
	Description string
	// InputHint is the provider's short argument placeholder, for example
	// "<issue-number>". It is presentation only; invocation remains ordinary
	// message text beginning with /Name.
	InputHint string
	// Source says where it came from (built-in, a plugin, the project), so a user
	// can tell a Tallpine-provided skill from one the provider ships.
	Source string
}

// ChatCompactionResult reports what a compaction did.
type ChatCompactionResult struct {
	// TokensBefore and TokensAfter bracket the reclaim, so the UI can say what was
	// gained rather than only that something happened.
	TokensBefore int64
	TokensAfter  int64
}
