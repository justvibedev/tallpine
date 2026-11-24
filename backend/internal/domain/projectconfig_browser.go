package domain

import "fmt"

// Browser backends of ProjectConfig.BrowserBackend (B849). An unset or empty
// value means BrowserBackendElectron.
const (
	BrowserBackendElectron = "electron"
	BrowserBackendChrome   = "chrome"
)

func validateBrowserBackend(v *string) error {
	if v == nil {
		return nil
	}
	switch *v {
	case "", BrowserBackendElectron, BrowserBackendChrome:
		return nil
	}
	return fmt.Errorf("browserBackend: %q is not electron or chrome", *v)
}

// EffectiveBrowserBackend is the project's browser backend, electron when it
// sets none.
func (c ProjectConfig) EffectiveBrowserBackend() string {
	if c.BrowserBackend != nil && *c.BrowserBackend == BrowserBackendChrome {
		return BrowserBackendChrome
	}
	return BrowserBackendElectron
}
