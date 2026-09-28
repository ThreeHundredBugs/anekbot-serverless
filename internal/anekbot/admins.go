package anekbot

import "strings"

// Admins is a normalized set of Telegram usernames gating admin-only features.
type Admins struct {
	usernames map[string]struct{}
}

func NewAdmins(usernames []string) *Admins {
	set := make(map[string]struct{}, len(usernames))
	for _, u := range usernames {
		u = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(u), "@"))
		if u != "" {
			set[u] = struct{}{}
		}
	}
	return &Admins{usernames: set}
}

func (a *Admins) IsAdmin(username string) bool {
	if a == nil || username == "" {
		return false
	}
	_, ok := a.usernames[strings.ToLower(username)]
	return ok
}
