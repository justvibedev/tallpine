package domain

// BoolPtr returns a pointer to v, for settings where nil means "inherit".
func BoolPtr(v bool) *bool { return &v }
