package engine

import (
	"slices"
	"strings"
)

// TrustedAuthor tells if login is the bot of the Mobius App appSlug, a trusted user or a trusted bot.
// GitHub compares logins with no regard to letter case.
func (e *Engine) TrustedAuthor(appSlug, login string) bool {
	return strings.EqualFold(login, appSlug+"[bot]") ||
		slices.ContainsFunc(slices.Concat(e.config.TrustedUsers, e.config.TrustedBots), func(trusted string) bool {
			return strings.EqualFold(trusted, login)
		})
}
