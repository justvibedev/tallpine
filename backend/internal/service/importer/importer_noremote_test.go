package importer

import (
	"context"
	"testing"
)

func TestValidateProjectImportCommittedRepoWithoutOriginIsReady(t *testing.T) {
	repo := t.TempDir()
	gitRepoWithCommitNoOrigin(t, repo)
	result, err := New().Validate(context.Background(), ImportValidationInput{ImportKind: ImportKindProject, Path: repo})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !result.IsValid || result.NextStep != ImportNextStepContinue || result.Root.HasOrigin {
		t.Fatalf("result = %#v, want valid local project with continue", result)
	}
	wantActions(t, result.Root.RequiredActions, nil)
}
