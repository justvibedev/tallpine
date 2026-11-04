package kiro

import "testing"

func TestCreditsFromSummaries(t *testing.T) {
	got, ok := CreditsFromSummaries([]PromptTurnSummary{{"credit", 0.5}, {"credit", 0.25}, {"token", 9}, {"credit", -1}})
	if !ok || got != 0.75 {
		t.Fatalf("got %v ok=%v", got, ok)
	}
	if _, ok := CreditsFromSummaries([]PromptTurnSummary{{"credit", 0}, {"token", 3}}); ok {
		t.Fatal("zero credits must report !ok")
	}
	if _, ok := CreditsFromSummaries(nil); ok {
		t.Fatal("nil must report !ok")
	}
}
