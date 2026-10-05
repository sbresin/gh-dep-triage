package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/sbresin/gh-dep-triage/internal/model"
)

// unblockKeys are the list keys that queue an action for the PR under the cursor.
var unblockKeys = map[string]string{"r": model.ActionRebase, "R": model.ActionRerun, "x": model.ActionClose}

func (m Model) unblock(k string, cur row) (tea.Model, tea.Cmd) {
	if cur.isGroup() {
		m.status = "Expand the group to act on one PR."
		return m, nil
	}
	pr, action := cur.pr, unblockKeys[k]
	var args map[string]string
	switch action {
	case model.ActionRerun:
		if pr.Checks.Failed == 0 {
			m.status = "No failing checks on " + pr.Ref + "."
			return m, nil
		}
	case model.ActionClose:
		if !pr.HasBlocker(model.BlockerSuperseded) {
			m.status = pr.Ref + " is not superseded."
			return m, nil
		}
		args = map[string]string{"reason": model.BlockerSuperseded}
	}
	return m.openConfirm(action, args, []*model.PR{pr}, nil), nil
}
