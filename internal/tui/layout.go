package tui

import (
	"github.com/Nulifyer/sqlgo/internal/tui/term"
)

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
type rect = term.Rect

type panels struct {
	explorer rect
	query    rect
	results  rect
	status   rect
}

func computeLayout(termW, termH int) panels {
	if termW < 0 {
		termW = 0
	}
	if termH < 0 {
		termH = 0
	}
	// Reserve one row at the bottom for the status line.
	statusH := statusBarH
	if statusH > termH {
		statusH = termH
	}
	bodyH := termH - statusH

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
	if explW < 0 {
		explW = 0
	}
	if explW > termW {
		explW = termW
	}

	rightW := termW - explW
	// Query takes ~40% of body height, results the rest.
	queryH := bodyH * 4 / 10
	if queryH < queryMinH {
		queryH = queryMinH
	}
	if queryH > bodyH {
		queryH = bodyH
	}
	resultsH := bodyH - queryH

	return panels{
		explorer: rect{Row: 1, Col: 1, W: explW, H: bodyH},
		query:    rect{Row: 1, Col: explW + 1, W: rightW, H: queryH},
		results:  rect{Row: 1 + queryH, Col: explW + 1, W: rightW, H: resultsH},
		status:   rect{Row: bodyH + 1, Col: 1, W: termW, H: statusH},
	}
}
