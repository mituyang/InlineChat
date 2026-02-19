package security

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

var weakPasswordBlacklist = map[string]struct{}{
	"123456":         {},
	"12345678":       {},
	"password":       {},
	"password123":    {},
	"qwerty123":      {},
	"admin123456":    {},
	"letmein123":     {},
	"agent12345!":    {},
	"superadmin123!": {},
	"changeme123!":   {},
	"changeme":       {},
}

func ValidatePasswordPolicy(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return fmt.Errorf("password is required")
	}

	length := utf8.RuneCountInString(raw)
	if length < 12 || length > 72 {
		return fmt.Errorf("password must be between 12 and 72 characters")
	}

	var hasUpper, hasLower, hasDigit, hasSpecial bool
	for _, r := range raw {
		if unicode.IsSpace(r) {
			return fmt.Errorf("password must not contain spaces")
		}
		switch {
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsLower(r):
			hasLower = true
		case unicode.IsDigit(r):
			hasDigit = true
		default:
			hasSpecial = true
		}
	}

	if !hasUpper || !hasLower || !hasDigit || !hasSpecial {
		return fmt.Errorf("password must include upper, lower, digit and special character")
	}

	if _, hit := weakPasswordBlacklist[strings.ToLower(raw)]; hit {
		return fmt.Errorf("password is too weak or commonly leaked")
	}

	return nil
}
