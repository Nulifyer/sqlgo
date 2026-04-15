package tui

import "time"

// Timeouts for store and network operations.
const (
	storeBootTimeout    = 10 * time.Second
	storeReadTimeout    = 5 * time.Second
	storeQuickTimeout   = 3 * time.Second
	storeHistoryTimeout = 2 * time.Second
	connectTimeout      = 10 * time.Second
	schemaTimeout       = 10 * time.Second
	explainTimeout      = 5 * time.Second
)

// Channel buffer sizes for the main loop.
const (
	resultChanBuf = 8
	inputChanBuf  = 8
)

// UI cadence.
const (
	progressThrottle = 50 * time.Millisecond
)

// Layout geometry shared across the main view.
const (
	statusBarH       = 1
	bodyMinH         = 6
	explorerMinW     = 18
	explorerMaxW     = 40
	explorerReserveR = 20
	queryMinH        = 5
)

// Editor / table behavior.
const (
	softTabWidth       = 4
	tablePageStep      = 10
	defaultSelectLimit = 100
)

// Network.
const (
	defaultSSHPort = 22
	maxTCPPort     = 65535
)

// Overlay sizing guard. Every modal dialog caps its width/height at
// (term - dialogMargin) so there's always a couple of rows/cols of
// surrounding context visible.
const dialogMargin = 4
