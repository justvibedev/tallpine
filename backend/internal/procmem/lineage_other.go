//go:build !darwin && !linux

package procmem

func readParentLink(int) (parentLink, error) { return parentLink{}, ErrLineageUnsupported }

// ExecutablePath is unsupported here and always returns "".
func ExecutablePath(int) string { return "" }

// ChildPIDs is unavailable on this platform.
func ChildPIDs(int) ([]int, error) { return nil, ErrLineageUnsupported }
