package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
)

func (m Model) rows() []row { return buildRows(m.snap.Groups, m.sortMode, m.expanded) }

func (m Model) listHeight() int { return max(1, m.height-3) }

func (m *Model) fixScroll() {
	n := len(m.rows())
	m.cursor = max(0, min(m.cursor, n-1))
	m.scroll = adjustScroll(m.cursor, m.scroll, m.listHeight(), n)
}

func adjustScroll(cursor, scroll, visible, total int) int {
	switch {
	case total <= visible:
		return 0
	case cursor < scroll:
		return cursor
	case cursor >= scroll+visible:
		return cursor - visible + 1
	default:
		return max(0, min(scroll, total-visible))
	}
}

func (m Model) selectedCount() int {
	n := 0
	for _, pr := range m.snap.PRs() {
		if m.selected[pr.Ref] {
			n++
		}
	}
	return n
}

func (m Model) updateList(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	if k == "q" || k == "esc" {
		return m, tea.Quit
	}
	if k == "g" {
		return m.startReload()
	}
	rows := m.rows()
	if len(rows) == 0 {
		return m, nil
	}
	if nm, cmd, handled := m.prAction(k); handled {
		return nm, cmd
	}
	cur := rows[m.cursor]
	switch k {
	case "j", "down":
		m.cursor++
	case "k", "up":
		m.cursor--
	case "s":
		m.sortMode = nextSort(m.sortMode)
		m.cursor, m.scroll = 0, 0
		m.status = "Sorted by " + m.sortMode + "."
	case "enter":
		if cur.isGroup() {
			gk := groupKey(cur.group)
			m.expanded[gk] = !m.expanded[gk]
		} else {
			m.status = "Enter expands or collapses grouped rows."
		}
	case "space":
		m.toggle(cur)
	case "c":
		if m.selectedCount() == 0 {
			m.status = "Select at least one PR before confirming."
			break
		}
		return m.startConfirm()
	}
	m.fixScroll()
	return m, nil
}

func (m Model) startReload() (tea.Model, tea.Cmd) {
	if m.reloading {
		m.status = "Already reloading..."
		return m, nil
	}
	m.reloading, m.status = true, "Reloading..."
	load, ctx := m.deps.Load, m.ctx
	return m, func() tea.Msg {
		s, err := load(ctx)
		return reloadedMsg{snap: s, err: err}
	}
}

// onReloaded swaps in a reloaded snapshot. A selection is kept only while
// its PR is still ready at the head the user saw.
func (m Model) onReloaded(msg reloadedMsg) (tea.Model, tea.Cmd) {
	m.reloading = false
	status := ""
	if msg.err != nil {
		status = "Reload failed: " + msg.err.Error()
	} else {
		heads := map[string]string{}
		for ref := range m.selected {
			if pr := m.findPR(ref); pr != nil {
				heads[ref] = pr.HeadOid
			}
		}
		m.snap = msg.snap
		moved := 0
		for ref := range m.selected {
			pr := m.findPR(ref)
			switch {
			case pr == nil || !selectable(pr):
				delete(m.selected, ref)
			case pr.HeadOid != heads[ref]:
				delete(m.selected, ref)
				moved++
			}
		}
		m.fixScroll()
		status = fmt.Sprintf("Reloaded %d PRs.", len(m.snap.PRs()))
		if n := len(m.snap.Warnings); n > 0 {
			status += fmt.Sprintf(" %d warning(s).", n)
		}
		if moved > 0 {
			status += fmt.Sprintf(" %d selection(s) cleared: head moved.", moved)
		}
	}
	if m.screen == screenList {
		m.status = status
	}
	return m, nil
}

func (m *Model) toggle(r row) {
	if r.isGroup() {
		ready := groupSelectable(r.group)
		if len(ready) == 0 {
			m.status = fmt.Sprintf("No PR in %s can be merged now; press b on a PR to see why.", groupLabel(r.group))
			return
		}
		all := true
		for _, pr := range ready {
			all = all && m.selected[pr.Ref]
		}
		for _, pr := range ready {
			if all {
				delete(m.selected, pr.Ref)
			} else {
				m.selected[pr.Ref] = true
			}
		}
		verb := "Selected"
		if all {
			verb = "Cleared"
		}
		m.status = fmt.Sprintf("%s %d PRs in the group.", verb, len(ready))
		return
	}
	pr := r.pr
	switch {
	case !selectable(pr):
		m.status = fmt.Sprintf("%s is %s; press b to see why.", pr.Ref, pr.Status)
	case m.selected[pr.Ref]:
		delete(m.selected, pr.Ref)
		m.status = "Cleared " + pr.Ref + "."
	default:
		m.selected[pr.Ref] = true
		m.status = "Selected " + pr.Ref + " for Approve+Merge."
	}
}

func (m Model) viewList() string {
	rows := m.rows()
	body := []string{}
	for i := m.scroll; i < min(len(rows), m.scroll+m.listHeight()); i++ {
		lead, box, text, badges := rowParts(rows[i], m.selected, m.expanded)
		body = append(body, rowStyle(rows[i], m.selected, i == m.cursor).Render(layoutRow(lead, box, text, badges, m.width)))
	}
	title := fmt.Sprintf("dep-triage | sort %s | %d/%d selected | %s", m.sortMode, m.selectedCount(), len(m.snap.PRs()), m.deps.Who)
	return m.frame(title, listKeys, body, m.status)
}
