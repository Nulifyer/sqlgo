package tui

import (
	"reflect"

	"github.com/Nulifyer/sqlgo/internal/tui/term"
)

// Layer is a drawable, input-handling slice of the UI. The app keeps a
// stack of layers; each frame every layer draws into its own cell buffer
// and the screen composites them bottom-to-top. The topmost layer
// receives keys exclusively — overlays are modal.
//
// Layers mutate app state directly via the *app receiver, so cross-layer
// actions like "connect from the picker, then dismiss the picker" are
// expressed as plain imperative code rather than a message bus.
//
// Stack invariant: a.layers[0] is always *mainLayer and is never popped.
// Overlays (picker, form, future popups) push/pop on top of it.
type Layer interface {
	// Draw renders this layer into its own cell buffer. Cells this layer
	// leaves untouched are transparent — the layer beneath shows through
	// at composite time.
	Draw(a *app, c *cellbuf)
	// HandleKey processes a key press. Only called on the topmost layer.
	HandleKey(a *app, k Key)
	// Hints returns the key hint line for this layer given the current
	// app state. The bottom status bar displays the topmost layer's
	// hints, so modal overlays (picker/form) can show their own keys
	// even though the mainLayer is the one doing the drawing.
	Hints(a *app) string
}

// View declares terminal modes a layer wants active while it is on top
// of the stack. Real definition lives in term/; this is a local alias
// so existing layer code keeps compiling unchanged.
type View = term.View

// ViewProvider is implemented by layers that want to override the
// default terminal modes while they're on top. The topmost
// implementing layer wins; layers below it don't contribute.
type ViewProvider interface {
	View(a *app) View
}

// defaultView is the baseline used when no layer on the stack
// implements ViewProvider. Alt-screen on matches the pre-ViewProvider
// behavior; everything else off.
func defaultView() View {
	return View{AltScreen: true}
}

// effectiveView walks the stack top-down looking for a ViewProvider.
// The topmost provider wins. If none implement it, defaultView.
func (a *app) effectiveView() View {
	for i := len(a.layers) - 1; i >= 0; i-- {
		if vp, ok := a.layers[i].(ViewProvider); ok {
			return vp.View(a)
		}
	}
	return defaultView()
}

// pushLayer adds l to the top of the stack. A double-push of the same
// concrete type is dropped: the original stays on top. This prevents a
// key-repeat (F2 held, Ctrl+O bounced) from stacking two copies of the
// same modal, which would then require two Escapes to close.
func (a *app) pushLayer(l Layer) {
	if len(a.layers) > 0 {
		top := a.layers[len(a.layers)-1]
		if reflect.TypeOf(top) == reflect.TypeOf(l) {
			return
		}
	}
	a.layers = append(a.layers, l)
}

func (a *app) popLayer() {
	if len(a.layers) > 1 {
		a.layers = a.layers[:len(a.layers)-1]
	}
}

func (a *app) topLayer() Layer {
	return a.layers[len(a.layers)-1]
}

// mainLayerPtr returns the always-present bottom layer as a *mainLayer.
// Helpers on the app that update main-view state (query results, status)
// reach through this.
func (a *app) mainLayerPtr() *mainLayer {
	return a.layers[0].(*mainLayer)
}
