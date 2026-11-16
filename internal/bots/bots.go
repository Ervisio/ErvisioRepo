// Package bots holds the system bot accounts created by the database
// migrations (APPS-PLAN A0). Bots are users whose namespace kind is "bot":
// their login ends in "[bot]", they have no password, email, session or
// second factor, and nobody can sign in as them.
package bots

import (
	"strings"

	"github.com/google/uuid"
)

// Kind is the namespace kind of a bot account.
const Kind = "bot"

// Suffix ends the login of every bot; normal usernames cannot contain it.
const Suffix = "[bot]"

// System bots (migration 00290_bots.sql).
var (
	// SyncID is ervisio-sync[bot], the author of the writes of the GitHub
	// sync (internal/ghsync).
	SyncID = uuid.MustParse("00000000-0000-7000-8000-00000000b001")
	// ActionsID is ervisio-actions[bot], the author of what workflows do
	// with their job token (comments, pushes, releases, statuses).
	ActionsID = uuid.MustParse("00000000-0000-7000-8000-00000000b002")
)

// Logins of the system bots.
const (
	SyncLogin    = "ervisio-sync[bot]"
	ActionsLogin = "ervisio-actions[bot]"
)

// IsSystem reports whether id is one of the system bots.
func IsSystem(id uuid.UUID) bool { return id == SyncID || id == ActionsID }

// IsLogin reports whether name is a bot login.
func IsLogin(name string) bool { return strings.HasSuffix(strings.ToLower(name), Suffix) }
