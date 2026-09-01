package api

import (
	"strings"

	"github.com/yama6a/codarr/internal/pkg/domain"
)

// Secrets are stored in plaintext, as the *arrs do (plan.md 18.4). This file enforces
// the other half: a GET never returns one, and a PUT only overwrites a non-mask value.

// masked is what every read returns in place of a secret. Empty stays empty, so
// the UI can tell "no token configured yet" from "a token is set".
func masked(stored string) string {
	if stored == "" {
		return ""
	}

	return domain.MaskedSecret
}

// The exact mask leaves the stored value alone, as does an empty submission: the UI
// renders a placeholder, and clearing it by accident would disconnect the instance.
func keepOrReplace(stored, submitted string) string {
	trimmed := strings.TrimSpace(submitted)
	if trimmed == "" || trimmed == domain.MaskedSecret {
		return stored
	}

	return trimmed
}
