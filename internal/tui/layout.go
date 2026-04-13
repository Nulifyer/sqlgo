package tui

// layout computes the geometry of the three-panel UI given a terminal size.
//
//	+----------+----------------------------+
//	|          |                            |
//	| Explorer |          Query             |
//	|          |                            |
//	|          +----------------------------+
//	|          |                            |
//	|          |          Results           |
//	|          |                            |
//	+----------+----------------------------+
//	| status line                           |
//	+---------------------------------------+
//
// All rect coordinates are 1-based and inclusive on both edges. Borders are
// drawn ON the rect edges, so panel content lives at row+1..row+h-2.
type rect struct {
	row, col, w, h int
}

type panels struct {
	explorer rect
	query    rect
	results  rect
	status   rect
}

func computeLayout(termW, termH int) panels {
	// Reserve one row at the bottom for the status line.
	statusH := statusBarH
	bodyH := termH - statusH
	if bodyH < bodyMinH {
		bodyH = bodyMinH
	}

	// Explorer takes ~25% width (min/max clamped).
	explW := termW / 4
	if explW < explorerMinW {
		explW = explorerMinW
	}
	if explW > explorerMaxW {
		explW = explorerMaxW
	}
	if explW > termW-explorerReserveR {
		explW = termW - explorerReserveR
	}

	rightW := termW - explW
	// Query takes ~40% of body height, results the rest.
	queryH := bodyH * 4 / 10
	if queryH < queryMinH {
		queryH = queryMinH
	}
	resultsH := bodyH - queryH

	return panels{
		explorer: rect{row: 1, col: 1, w: explW, h: bodyH},
		query:    rect{row: 1, col: explW + 1, w: rightW, h: queryH},
		results:  rect{row: 1 + queryH, col: explW + 1, w: rightW, h: resultsH},
		status:   rect{row: bodyH + 1, col: 1, w: termW, h: statusH},
	}
}
