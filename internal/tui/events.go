package tui

import "github.com/Nulifyer/sqlgo/internal/tui/term"

// All event types are thin aliases so existing tui/ code compiles without
// changes. The real definitions (and the inputMsg marker) live in term/.

type InputMsg = term.InputMsg

type PasteMsg = term.PasteMsg

type MouseButton = term.MouseButton

const (
	MouseButtonNone       = term.MouseButtonNone
	MouseButtonLeft       = term.MouseButtonLeft
	MouseButtonMiddle     = term.MouseButtonMiddle
	MouseButtonRight      = term.MouseButtonRight
	MouseButtonWheelUp    = term.MouseButtonWheelUp
	MouseButtonWheelDown  = term.MouseButtonWheelDown
	MouseButtonWheelLeft  = term.MouseButtonWheelLeft
	MouseButtonWheelRight = term.MouseButtonWheelRight
)

type MouseAction = term.MouseAction

const (
	MouseActionPress   = term.MouseActionPress
	MouseActionRelease = term.MouseActionRelease
	MouseActionMotion  = term.MouseActionMotion
)

type MouseMsg = term.MouseMsg

type FocusMsg = term.FocusMsg
type BlurMsg = term.BlurMsg

type ResizeMsg = term.ResizeMsg

// InputHandler is the optional extension on Layer for mouse, paste, and
// focus events. Layers implementing it receive every non-Key event
// delivered to the top of the stack; returning true marks the event
// consumed. Layers that don't implement it ignore non-Key events --
// Key events continue to flow through HandleKey unchanged.
type InputHandler interface {
	HandleInput(a *app, msg InputMsg) bool
}
