package api

import (
	"strings"

	"github.com/yama6a/codarr/internal/pkg/domain"
)

// Secrets are stored in plaintext, matching what the *arrs themselves do
// (plan.md 18.4). What this file enforces is the other half of that rule: a GET
// never returns one, and a PUT only overwrites when the value supplied is not
// the mask.

// masked is what every read returns in place of a secret. Empty stays empty, so
// the UI can tell "no token configured yet" from "a token is set".
func masked(stored string) string {
	if stored == "" {
		return ""
	}

	return domain.MaskedSecret
}

// keepOrReplace is the write half. The exact mask leaves the stored value
// alone; anything else replaces it. An empty submission also leaves it alone,
// because the field the UI renders is a placeholder rather than the value and
// clearing it by accident would silently disconnect the instance.
func keepOrReplace(stored, submitted string) string {
	trimmed := strings.TrimSpace(submitted)
	if trimmed == "" || trimmed == domain.MaskedSecret {
		return stored
	}

	return trimmed
}
