package settingswrite

import (
	"context"
	"encoding/json"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
	settingssvc "tallpine.eu/tallpine/backend/internal/service/settings"
	"tallpine.eu/tallpine/backend/internal/settingsreg"
)

// PeerCheckKey is the owner's off switch for the daemon peer check (B631).
const PeerCheckKey settingsreg.Key = "app.daemon.peerCheck"

// CodeDesktopOnly refuses a change only the desktop app itself may make.
const CodeDesktopOnly = "SETTING_DESKTOP_ONLY"

// PeerCheckSwitch persists the peer check switch and applies it live.
type PeerCheckSwitch interface {
	// DesktopCaller reports whether the request on ctx comes from the signed
	// desktop app (always true on a build that cannot check peers).
	DesktopCaller(ctx context.Context) bool
	// Set stores value ("default" or "off") and applies it to the running
	// daemon before returning.
	Set(ctx context.Context, value string) error
}

// peerCheckRefusal says why the caller on ctx may not change the switch. Only
// the desktop app may: an agent, a phone, a desktop-opened shell or any other
// process holding the install token could otherwise turn the check off.
func (w *Writer) peerCheckRefusal(ctx context.Context) string {
	switch {
	case w.PeerCheck == nil:
		return "this daemon has no peer check"
	case requestscope.IsLAN(ctx):
		return "only the " + brand.ProductName + " desktop app on this Mac can change it"
	}
	if _, isAgent := requestscope.AgentFrom(ctx); isAgent || !w.PeerCheck.DesktopCaller(ctx) {
		return "only the " + brand.ProductName + " desktop app can change it; use Settings > Security"
	}
	return ""
}

func (w *Writer) setPeerCheck(ctx context.Context, raw json.RawMessage) error {
	var value string
	_ = json.Unmarshal(raw, &value)
	return w.PeerCheck.Set(ctx, settingssvc.PeerCheckOrDefault(value))
}
