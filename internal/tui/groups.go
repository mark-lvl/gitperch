package tui

import (
	"fmt"
	"sort"

	"github.com/mark-lvl/gitperch/internal/app"
)

// groupKey is the parent row's path; rows without worktree information stand
// alone.
func groupKey(row app.Row) string {
	if row.Worktree != nil && row.Worktree.MainPath != "" {
		return row.Worktree.MainPath
	}
	return row.Path
}

func isParent(row app.Row) bool { return groupKey(row) == row.Path }

func (m *Model) matchesView(row app.Row) bool { return matches(row, m.filter) && m.inScope(row) }

// narrowed reports whether search or a scope hides rows, in which case
// matching children appear without expanding their group.
func (m *Model) narrowed() bool { return m.filter != "" || m.scope != 0 }

// isContext reports a row that is visible only as the parent of a match.
func (m *Model) isContext(index int) bool { return !m.matchesView(m.rows[index]) }

// visibleRows orders groups as the flat list was ordered before (attention,
// then name and path), keeps each parent first and lists children by path.
func (m *Model) visibleRows() []int {
	type group struct {
		parent   int
		children []int
		matched  bool
		rank     int
	}
	groups := map[string]*group{}
	var keys []string
	for i, row := range m.rows {
		k := groupKey(row)
		g := groups[k]
		if g == nil {
			g = &group{parent: -1}
			groups[k] = g
			keys = append(keys, k)
		}
		if isParent(row) {
			g.parent = i
		} else {
			g.children = append(g.children, i)
		}
		if m.matchesView(row) {
			g.matched = true
			g.rank = max(g.rank, m.attentionRank(row))
		}
	}
	var ordered []*group
	for _, k := range keys {
		g := groups[k]
		if !g.matched {
			continue
		}
		if g.parent < 0 { // parent missing from the snapshot: promote first child
			g.parent, g.children = g.children[0], g.children[1:]
		}
		sort.SliceStable(g.children, func(i, j int) bool { return m.rows[g.children[i]].Path < m.rows[g.children[j]].Path })
		ordered = append(ordered, g)
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := m.rows[ordered[i].parent], m.rows[ordered[j].parent]
		if m.attentionFirst && ordered[i].rank != ordered[j].rank {
			return ordered[i].rank > ordered[j].rank
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Path < b.Path
	})
	indices := make([]int, 0, len(m.rows))
	for _, g := range ordered {
		indices = append(indices, g.parent)
		expanded := m.expanded[groupKey(m.rows[g.parent])]
		for _, c := range g.children {
			// Search and scopes reveal matching children; otherwise a group
			// shows its children only when expanded.
			if m.narrowed() && m.matchesView(m.rows[c]) || !m.narrowed() && expanded {
				indices = append(indices, c)
			}
		}
	}
	return indices
}

// treePrefix draws the branch glyph for a child; the first row of a group (its
// parent, or the promoted first child when the parent is absent) has none.
func (m *Model) treePrefix(pos int, indices []int) string {
	key := groupKey(m.rows[indices[pos]])
	if pos == 0 || groupKey(m.rows[indices[pos-1]]) != key {
		return ""
	}
	if pos+1 < len(indices) && groupKey(m.rows[indices[pos+1]]) == key {
		return "├─ "
	}
	return "└─ "
}

// groupBadge summarizes a collapsed parent's worktrees in full, e.g.
// "⑂3 · 1 stale · 1 needs attention".
func (m *Model) groupBadge(row app.Row) string {
	if badges := m.groupBadges(row); len(badges) > 0 {
		return badges[0]
	}
	return ""
}

// groupBadges returns the collapsed-group badge from widest to narrowest: the
// full wording, a compact "⑂3 ◌1 !1", and the worktree count alone. Stale
// records are counted on their own; attention counts the other hidden
// worktrees that would raise the group in the attention order.
func (m *Model) groupBadges(row app.Row) []string {
	if !isParent(row) || m.expanded[row.Path] {
		return nil
	}
	linked, stale, attention := 0, 0, 0
	for _, other := range m.rows {
		if other.Path != row.Path && groupKey(other) == row.Path {
			linked++
			switch {
			case other.Worktree != nil && other.Worktree.Prunable:
				stale++
			case m.attentionRank(other) > 0:
				attention++
			}
		}
	}
	if linked == 0 {
		return nil
	}
	count := fmt.Sprintf("%s%d", m.symbols().worktree, linked)
	full, compact := count, count
	if stale > 0 {
		full += fmt.Sprintf(" · %d stale", stale)
		compact += fmt.Sprintf(" ◌%d", stale)
	}
	if attention == 1 {
		full += " · 1 needs attention"
	} else if attention > 1 {
		full += fmt.Sprintf(" · %d need attention", attention)
	}
	if attention > 0 {
		compact += fmt.Sprintf(" !%d", attention)
	}
	if compact == count {
		return []string{count}
	}
	if full == compact {
		return []string{full, count}
	}
	return []string{full, compact, count}
}
