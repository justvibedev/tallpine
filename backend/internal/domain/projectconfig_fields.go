package domain

import (
	"fmt"
	"strings"
	"unicode"
)

// Field-level validators used by ProjectConfig.Validate.

// MaxVerifyCommandLen bounds VerifyCommand so it stays a one-line prompt hint.
const MaxVerifyCommandLen = 500

func validateVerifyCommand(cmd string) error {
	if len(cmd) > MaxVerifyCommandLen {
		return fmt.Errorf("verifyCommand: must be at most %d characters", MaxVerifyCommandLen)
	}
	for _, r := range cmd {
		if unicode.IsControl(r) {
			return fmt.Errorf("verifyCommand: must be a single line without control characters")
		}
	}
	return nil
}

func validEnvName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

func validateNoWhitespaceField(name, value string) error {
	if value == "" {
		return nil
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("%s: must not have leading or trailing whitespace", name)
	}
	return nil
}

func validateNameComponent(name, value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	if strings.ContainsAny(trimmed, `/\`) || trimmed == "." || trimmed == ".." {
		return fmt.Errorf("%s: must not contain path separators or traversal components", name)
	}
	return nil
}
