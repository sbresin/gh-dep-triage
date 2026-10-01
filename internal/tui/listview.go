package tui

import "charm.land/lipgloss/v2"

// listRow is a line on the confirm and results screens. Header rows are
// skipped by the cursor.
type listRow struct {
	header bool
	text   string
	ref    string
	style  lipgloss.Style
}

type listView struct {
	rows   []listRow
	cursor int
	scroll int
}

func newListView(rows []listRow) listView {
	lv := listView{rows: rows}
	for i, r := range rows {
		if !r.header {
			lv.cursor = i
			break
		}
	}
	return lv
}

// move steps the cursor by delta (±1) to the next non-header row, if any.
func (lv *listView) move(delta, visible int) {
	for i := lv.cursor + delta; i >= 0 && i < len(lv.rows); i += delta {
		if !lv.rows[i].header {
			lv.cursor = i
			break
		}
	}
	lv.scroll = adjustScroll(lv.cursor, lv.scroll, visible, len(lv.rows))
}

func (lv listView) focusedRef() string {
	if lv.cursor < len(lv.rows) && !lv.rows[lv.cursor].header {
		return lv.rows[lv.cursor].ref
	}
	return ""
}

func (lv listView) render(width, visible int) []string {
	out := []string{}
	for i := lv.scroll; i < min(len(lv.rows), lv.scroll+visible); i++ {
		r := lv.rows[i]
		text, style := r.text, r.style
		if !r.header {
			text = "  " + text
			if i == lv.cursor {
				style = style.Reverse(true)
			}
		}
		out = append(out, style.Render(clip(text, width)))
	}
	return out
}
