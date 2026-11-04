package chat

import (
	"context"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// SetTitle names the provider's thread and returns the normalized title.
//
// Nothing is written to Tallpine's rows here. The provider answers, then emits its own
// rename notification, and the projection applies it — so the title Tallpine stores is
// always one the provider confirmed. Writing it optimistically as well would give
// one fact two authors and no way to tell which lost.
func (s *Service) SetTitle(ctx context.Context, id domain.SessionID, title string) (string, error) {
	if _, err := s.requireChatSession(ctx, id); err != nil {
		return "", err
	}
	normalized := NormalizeTitle(title)
	if normalized == "" {
		return "", ErrTitleRequired
	}
	controller, release, err := s.workingController(ctx, id)
	if err != nil {
		return "", err
	}
	defer release()
	if controller.conversation.Scope == domain.ConversationScopeProject {
		return "", ErrOrchestratorRename
	}
	renamer, ok := controller.conv.(ports.ChatRenamer)
	if !ok {
		return "", ErrRenameUnsupported
	}
	if err := renamer.SetTitle(ctx, normalized); err != nil {
		return "", classify(fmt.Errorf("set title for %s: %w", id, err))
	}
	if err := controller.applyThreadTitle(ctx, normalized, s.now()); err != nil {
		return "", err
	}
	return normalized, nil
}

// NormalizeTitle reduces a title to the one-line label Tallpine is willing to show.
//
// The rules come from the automatic-semantic-task-titles design: one line, no
// wrapper punctuation, no trailing punctuation, and a hard length bound. They are
// applied to whatever the provider says rather than trusted, because a title is
// model output and arrives with heading markers, surrounding quotes, and full stops
// often enough that a client would otherwise render them.
//
// Over-length input is truncated at a word boundary rather than rejected: the
// provider already accepted the name, and refusing to display a title Tallpine's own
// session is now carrying would leave the two disagreeing.
func NormalizeTitle(raw string) string {
	title := raw
	if idx := strings.IndexAny(title, "\r\n"); idx >= 0 {
		title = title[:idx]
	}
	title = strings.TrimSpace(title)

	// Markdown heading and list markers, which a model reaches for when asked for a
	// title and told nothing else.
	title = strings.TrimLeft(title, "#*->+ \t")
	// Wrapper punctuation, stripped from both ends so a quoted title is not shown
	// quoted and a backticked one is not shown as code.
	title = strings.Trim(title, "\"'`“”‘’ \t")
	title = strings.Join(strings.Fields(title), " ")
	title = strings.TrimRight(title, ".,;:!。 \t")

	if utf8.RuneCountInString(title) > maxTitleRunes {
		title = truncateAtWord(title, maxTitleRunes)
	}
	// A title made entirely of punctuation is not a title.
	if strings.IndexFunc(title, func(r rune) bool {
		return unicode.IsLetter(r) || unicode.IsDigit(r)
	}) < 0 {
		return ""
	}
	return title
}

// truncateAtWord cuts at the last space inside the limit, falling back to a hard cut
// for a language that does not put spaces between words.
func truncateAtWord(title string, limit int) string {
	runes := []rune(title)
	if len(runes) <= limit {
		return title
	}
	cut := string(runes[:limit])
	if idx := strings.LastIndex(cut, " "); idx > 0 {
		return strings.TrimRight(cut[:idx], " ")
	}
	return strings.TrimRight(cut, " ")
}
