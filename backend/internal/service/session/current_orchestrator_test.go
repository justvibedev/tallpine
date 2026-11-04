package session

import (
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func orchSession(id, project, lane string, ended bool, age int) domain.Session {
	var s domain.Session
	s.ID = domain.SessionID(id)
	s.ProjectID = domain.ProjectID(project)
	s.Kind = domain.KindOrchestrator
	s.Lane = lane
	s.IsTerminated = ended
	s.CreatedAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(age) * time.Hour)
	return s
}

func TestMarkCurrentOrchestrators(t *testing.T) {
	cases := []struct {
		name string
		in   []domain.Session
		want []string
	}{
		{"live default beats newer ended", []domain.Session{orchSession("live", "p", "", false, 1), orchSession("ended", "p", "", true, 2)}, []string{"live"}},
		{"live default beats older ended listed first", []domain.Session{orchSession("ended", "p", "", true, 1), orchSession("live", "p", "", false, 2)}, []string{"live"}},
		{"newest ended when none live", []domain.Session{orchSession("old", "p", "", true, 1), orchSession("new", "p", "", true, 3), orchSession("mid", "p", "", true, 2)}, []string{"new"}},
		{"oldest live is main of several", []domain.Session{orchSession("b", "p", "", false, 2), orchSession("a", "p", "", false, 1)}, []string{"a"}},
		{"ended lane is never current", []domain.Session{orchSession("l1", "p", "B1", true, 1)}, nil},
		{"live lane per lane and project", []domain.Session{orchSession("l1", "p", "B1", false, 1), orchSession("l2", "p", "B2", false, 1), orchSession("q", "q", "", false, 1), orchSession("d", "p", "", true, 1)}, []string{"l1", "l2", "q", "d"}},
		{"no project is skipped", []domain.Session{orchSession("x", "", "", false, 1)}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			markCurrentOrchestrators(tc.in)
			var got []string
			for _, s := range tc.in {
				if s.CurrentOrchestrator {
					got = append(got, string(s.ID))
				}
			}
			if len(got) != len(tc.want) {
				t.Fatalf("marked %v, want %v", got, tc.want)
			}
			set := map[string]bool{}
			for _, g := range got {
				set[g] = true
			}
			for _, w := range tc.want {
				if !set[w] {
					t.Fatalf("marked %v, want %v", got, tc.want)
				}
			}
		})
	}
}
