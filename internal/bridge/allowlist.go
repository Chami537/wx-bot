package bridge

// Allowlist is the set of wxids permitted to talk to a binding.
type Allowlist struct {
	ids map[string]struct{}
}

// NewAllowlist builds an allowlist. If allowWxids is empty, the default is
// loginUserID (the ilink_user_id saved at login).
func NewAllowlist(allowWxids []string, loginUserID string) Allowlist {
	ids := make(map[string]struct{})
	if len(allowWxids) == 0 {
		if loginUserID != "" {
			ids[loginUserID] = struct{}{}
		}
		return Allowlist{ids: ids}
	}
	for _, id := range allowWxids {
		if id != "" {
			ids[id] = struct{}{}
		}
	}
	return Allowlist{ids: ids}
}

// Contains reports whether wxid is allowed.
func (a Allowlist) Contains(wxid string) bool {
	_, ok := a.ids[wxid]
	return ok
}

// IDs returns the allowlisted wxids in unspecified order.
func (a Allowlist) IDs() []string {
	out := make([]string, 0, len(a.ids))
	for id := range a.ids {
		out = append(out, id)
	}
	return out
}
