package settingswrite

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
	settingssvc "tallpine.eu/tallpine/backend/internal/service/settings"
	"tallpine.eu/tallpine/backend/internal/service/settingsview"
	"tallpine.eu/tallpine/backend/internal/settingsreg"
)

type peerCheckRow struct {
	settingssvc.Store
	value string
}

func (s *peerCheckRow) GetAppSettings(context.Context) (settingssvc.Snapshot, error) {
	return settingssvc.Snapshot{PeerCheck: s.value}, nil
}

// fakeSwitch stands in for the daemon's switch: desktop says whether the
// caller is the signed desktop app; Set writes the row and records the live
// apply.
type fakeSwitch struct {
	row     *peerCheckRow
	desktop bool
	applied []string
}

func (f *fakeSwitch) DesktopCaller(context.Context) bool { return f.desktop }
func (f *fakeSwitch) Set(_ context.Context, value string) error {
	f.row.value = value
	f.applied = append(f.applied, value)
	return nil
}

func peerCheckWriter(desktop bool) (*Writer, *fakeSwitch) {
	row := &peerCheckRow{value: "default"}
	app := settingssvc.New(row, nil, settingssvc.Offering{}, nil)
	sw := &fakeSwitch{row: row, desktop: desktop}
	return &Writer{View: &settingsview.Service{AppSettings: app}, AppSettings: app, PeerCheck: sw}, sw
}

func setPeerCheck(ctx context.Context, w *Writer, value string) error {
	raw, _ := json.Marshal(value)
	return w.Apply(ctx, Change{Set: map[settingsreg.Key]json.RawMessage{PeerCheckKey: raw}})
}

func TestDesktopAppTurnsPeerCheckOffAndBackLive(t *testing.T) {
	w, sw := peerCheckWriter(true)
	if err := setPeerCheck(context.Background(), w, "off"); err != nil {
		t.Fatal(err)
	}
	if err := w.Apply(context.Background(), Change{Unset: []settingsreg.Key{PeerCheckKey}}); err != nil {
		t.Fatal(err)
	}
	if len(sw.applied) != 2 || sw.applied[0] != "off" || sw.applied[1] != "default" {
		t.Fatalf("applied = %v, want [off default]", sw.applied)
	}
	if err := setPeerCheck(context.Background(), w, "maybe"); err == nil {
		t.Fatal("an unknown value was accepted")
	}
}

func TestOnlyTheDesktopAppChangesPeerCheck(t *testing.T) {
	agent := requestscope.WithAgent(context.Background(), requestscope.Agent{Role: requestscope.RoleOrchestrator, ProjectID: "app"})
	lan := requestscope.WithLAN(context.Background())
	cases := []struct {
		name    string
		ctx     context.Context
		desktop bool
		code    string
	}{
		{"install token outside the desktop app", context.Background(), false, CodeDesktopOnly},
		{"phone over the LAN", lan, true, CodeDesktopOnly},
		{"agent", agent, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, sw := peerCheckWriter(tc.desktop)
			err := setPeerCheck(tc.ctx, w, "off")
			var refused *Error
			if !errors.As(err, &refused) || (tc.code != "" && refused.Code != tc.code) {
				t.Fatalf("err = %v, want a refusal %s", err, tc.code)
			}
			if len(sw.applied) != 0 || sw.row.value != "default" {
				t.Fatalf("refused change was applied: %v", sw.applied)
			}
		})
	}
}

func TestPeerCheckRefusedWithoutASwitch(t *testing.T) {
	w, _ := peerCheckWriter(true)
	w.PeerCheck = nil
	if err := setPeerCheck(context.Background(), w, "off"); err == nil {
		t.Fatal("a daemon without a peer check stored the switch")
	}
}
