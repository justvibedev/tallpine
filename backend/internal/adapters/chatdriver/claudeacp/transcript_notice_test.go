package claudeacp

import "testing"

func TestClaudeTranscriptNotice(t *testing.T) {
	for text, want := range map[string]bool{
		"[Request interrupted by user]":                                                  true,
		"[Request interrupted by user for tool use]":                                     true,
		"[Request interrupted by user] keep going":                                       false,
		"what does <task-notification> mean?":                                            false,
		claudeCompactionSummaryPrefix + " The summary below covers the earlier portion.": true,
		"why did it say: " + claudeCompactionSummaryPrefix:                               false,
	} {
		if claudeTranscriptNotice(text) != want {
			t.Errorf("claudeTranscriptNotice(%q) = %v", text, !want)
		}
	}
}
