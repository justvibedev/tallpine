package skillbridge

import (
	"testing"
)

// Every project sees the app-wide custom skills (<dataDir>/skills-custom);
// a project custom skill of the same name wins.
func TestDiscoverFindsAppCustomSkillsBelowProjectOnes(t *testing.T) {
	dataDir := t.TempDir()
	app := CustomStore{Root: AppCustomDir(dataDir)}
	project := CustomStore{Root: CustomDir(dataDir, "mer")}
	for _, n := range []string{"shared", "app-only"} {
		if _, err := app.Create(n, "app "+n); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := project.Create("shared", "project shared"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"mer", "other"} {
		inv := Discover(Options{DataDir: dataDir}.WithProject(id))
		winners := map[string]Convention{}
		for _, s := range inv.Winners {
			winners[s.Name] = s.Convention
		}
		if winners["app-only"] != ConvCustomApp {
			t.Fatalf("project %s: app custom skill missing: %+v", id, inv.Winners)
		}
		want := ConvCustomApp
		if id == "mer" {
			want = ConvCustom
		}
		if winners["shared"] != want {
			t.Fatalf("project %s: shared won from %q, want %q", id, winners["shared"], want)
		}
	}
	if AppCustomDir("") != "" {
		t.Fatal("no data dir must mean no app custom skills")
	}
}
