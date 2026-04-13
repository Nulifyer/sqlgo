package tui

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
// of the stack. Mode flags with zero values mean "off"; the one
// exception is AltScreen, whose default is "on" via defaultView() so
// layers that don't implement ViewProvider get the existing behavior.
//
// The screen tracks the last applied View and emits only the sequences
// for flags that flipped, so per-frame View() calls are cheap when
// nothing changes.
type View struct {
	AltScreen    bool
	MouseEnabled bool
	PasteEnabled bool
	WindowTitle  string
}

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

func (a *app) pushLayer(l Layer) {
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
