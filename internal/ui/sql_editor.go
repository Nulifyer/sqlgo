package ui

import (
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	coreeditor "github.com/nulifyer/sqlgo/internal/editor"
)

type CompletionProvider func(force bool, text string, cursor int) (coreeditor.CompletionContext, []coreeditor.CompletionItem, error)

type SQLEditor struct {
	*tview.Box

	textArea *tview.TextArea
	popup    *tview.List

	completionProvider CompletionProvider
	onChanged          func()
	onMoved            func()

	completionCtx   coreeditor.CompletionContext
	completionItems []coreeditor.CompletionItem
	popupVisible    bool
}

func NewSQLEditor() *SQLEditor {
	e := &SQLEditor{
		Box:      tview.NewBox(),
		textArea: tview.NewTextArea(),
		popup:    tview.NewList(),
	}

	e.popup.SetBorder(true).SetTitle(" Completions ")
	e.popup.ShowSecondaryText(false)
	e.popup.SetSelectedFocusOnly(false)
	e.popup.SetHighlightFullLine(true)
	e.popup.SetSelectedFunc(func(index int, mainText string, secondaryText string, shortcut rune) {
		e.acceptAutocomplete()
	})

	e.textArea.SetChangedFunc(func() {
		e.refreshAutocomplete(false)
		if e.onChanged != nil {
			e.onChanged()
		}
	})
	e.textArea.SetMovedFunc(func() {
		e.refreshAutocomplete(false)
		if e.onMoved != nil {
			e.onMoved()
		}
	})

	return e
}

func (e *SQLEditor) SetRect(x, y, width, height int) {
	e.Box.SetRect(x, y, width, height)
	e.textArea.SetRect(x, y, width, height)
	e.updatePopupRect()
}

func (e *SQLEditor) Draw(screen tcell.Screen) {
	e.textArea.Draw(screen)
	if e.popupVisible {
		e.updatePopupRect()
		e.popup.Draw(screen)
	}
}

func (e *SQLEditor) Focus(delegate func(p tview.Primitive)) {
	e.Box.Focus(nil)
	e.textArea.Focus(func(p tview.Primitive) {})
}

func (e *SQLEditor) Blur() {
	e.Box.Blur()
	e.textArea.Blur()
	e.HideAutocomplete()
}

func (e *SQLEditor) HasFocus() bool {
	return e.Box.HasFocus() || e.textArea.HasFocus()
}

func (e *SQLEditor) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return e.Box.WrapInputHandler(func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
		if event == nil {
			return
		}

		if e.HandleAutocompleteKey(event) {
			return
		}

		if event.Key() == tcell.KeyEnter {
			selection, start, end := e.textArea.GetSelection()
			cursor := end
			if selection == "" {
				start = cursor
			}
			e.textArea.Replace(start, end, "\n"+coreeditor.NextLineIndent(e.textArea.GetText(), cursor))
			return
		}

		if handler := e.textArea.InputHandler(); handler != nil {
			handler(event, func(p tview.Primitive) {
				setFocus(e)
			})
		}
	})
}

func (e *SQLEditor) MouseHandler() func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (consumed bool, capture tview.Primitive) {
	return e.Box.WrapMouseHandler(func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (consumed bool, capture tview.Primitive) {
		if e.popupVisible {
			px, py, pw, ph := e.popup.GetRect()
			if x, y := event.Position(); x >= px && x < px+pw && y >= py && y < py+ph {
				if handler := e.popup.MouseHandler(); handler != nil {
					consumed, _ = handler(action, event, func(p tview.Primitive) {
						setFocus(e)
					})
					if consumed {
						return true, e
					}
				}
			}
		}

		if handler := e.textArea.MouseHandler(); handler != nil {
			consumed, _ = handler(action, event, func(p tview.Primitive) {
				setFocus(e)
				e.Focus(nil)
			})
			if consumed {
				return true, e
			}
		}
		return false, nil
	})
}

func (e *SQLEditor) PasteHandler() func(text string, setFocus func(p tview.Primitive)) {
	return e.Box.WrapPasteHandler(func(text string, setFocus func(p tview.Primitive)) {
		if handler := e.textArea.PasteHandler(); handler != nil {
			handler(text, func(p tview.Primitive) {
				setFocus(e)
			})
		}
	})
}

func (e *SQLEditor) HandleAutocompleteKey(event *tcell.EventKey) bool {
	if isAutocompleteTrigger(event) {
		e.TriggerAutocomplete()
		return true
	}

	if isOutdentTrigger(event) {
		e.outdentSelection()
		return true
	}

	if !e.popupVisible || len(e.completionItems) == 0 {
		return false
	}

	switch event.Key() {
	case tcell.KeyEsc:
		e.HideAutocomplete()
		return true
	case tcell.KeyDown:
		e.moveAutocompleteSelection(1)
		return true
	case tcell.KeyTab:
		if event.Modifiers()&tcell.ModShift != 0 {
			e.outdentSelection()
			return true
		}
		e.acceptAutocomplete()
		return true
	case tcell.KeyUp:
		e.moveAutocompleteSelection(-1)
		return true
	}

	return false
}

func (e *SQLEditor) TriggerAutocomplete() {
	e.refreshAutocomplete(true)
}

func (e *SQLEditor) HideAutocomplete() {
	e.popupVisible = false
	e.completionItems = nil
	e.completionCtx = coreeditor.CompletionContext{}
}

func (e *SQLEditor) SetCompletionProvider(provider CompletionProvider) *SQLEditor {
	e.completionProvider = provider
	return e
}

func (e *SQLEditor) SetChangedFunc(handler func()) *SQLEditor {
	e.onChanged = handler
	return e
}

func (e *SQLEditor) SetMovedFunc(handler func()) *SQLEditor {
	e.onMoved = handler
	return e
}

func (e *SQLEditor) SetBorder(show bool) *SQLEditor {
	e.textArea.SetBorder(show)
	return e
}

func (e *SQLEditor) SetTitle(title string) *SQLEditor {
	e.textArea.SetTitle(title)
	return e
}

func (e *SQLEditor) SetWrap(wrap bool) *SQLEditor {
	e.textArea.SetWrap(wrap)
	return e
}

func (e *SQLEditor) SetWordWrap(wrap bool) *SQLEditor {
	e.textArea.SetWordWrap(wrap)
	return e
}

func (e *SQLEditor) SetPlaceholder(text string) *SQLEditor {
	e.textArea.SetPlaceholder(text)
	return e
}

func (e *SQLEditor) GetText() string {
	return e.textArea.GetText()
}

func (e *SQLEditor) SetText(text string, cursorAtEnd bool) *SQLEditor {
	e.textArea.SetText(text, cursorAtEnd)
	e.refreshAutocomplete(false)
	return e
}

func (e *SQLEditor) Replace(start, end int, text string) *SQLEditor {
	e.textArea.Replace(start, end, text)
	return e
}

func (e *SQLEditor) GetSelection() (string, int, int) {
	return e.textArea.GetSelection()
}

func (e *SQLEditor) Select(start, end int) *SQLEditor {
	e.textArea.Select(start, end)
	e.refreshAutocomplete(false)
	return e
}

func (e *SQLEditor) GetCursor() (int, int, int, int) {
	return e.textArea.GetCursor()
}

func (e *SQLEditor) GetInnerRect() (int, int, int, int) {
	return e.textArea.GetInnerRect()
}

func (e *SQLEditor) refreshAutocomplete(force bool) {
	if e.completionProvider == nil {
		e.HideAutocomplete()
		return
	}

	_, _, cursor := e.textArea.GetSelection()
	ctx, items, err := e.completionProvider(force, e.textArea.GetText(), cursor)
	if err != nil {
		e.HideAutocomplete()
		return
	}
	if !shouldShowAutocomplete(ctx, force) || len(items) == 0 || shouldHideAutocomplete(ctx, items) {
		e.HideAutocomplete()
		return
	}

	selectedInsert := ""
	if e.popupVisible && len(e.completionItems) > 0 {
		index := e.popup.GetCurrentItem()
		if index >= 0 && index < len(e.completionItems) {
			selectedInsert = e.completionItems[index].Insert
		}
	}

	e.completionCtx = ctx
	e.completionItems = items
	e.popupVisible = true

	e.popup.Clear()
	maxItems := min(len(items), 8)
	for _, item := range items[:maxItems] {
		main := tview.Escape(item.Label)
		if item.Kind != "" {
			main = main + " [gray](" + tview.Escape(item.Kind) + ")[-]"
		}
		e.popup.AddItem(main, "", 0, nil)
	}

	e.popup.SetCurrentItem(0)
	if selectedInsert != "" {
		for index, item := range items[:maxItems] {
			if item.Insert == selectedInsert {
				e.popup.SetCurrentItem(index)
				break
			}
		}
	}

	e.updatePopupRect()
}

func (e *SQLEditor) acceptAutocomplete() {
	if !e.popupVisible || len(e.completionItems) == 0 {
		return
	}
	index := e.popup.GetCurrentItem()
	if index < 0 || index >= len(e.completionItems) {
		return
	}
	item := e.completionItems[index]
	e.textArea.Replace(e.completionCtx.Start, e.completionCtx.End, item.Insert)
	e.HideAutocomplete()
}

func (e *SQLEditor) moveAutocompleteSelection(delta int) {
	if !e.popupVisible || len(e.completionItems) == 0 {
		return
	}
	index := e.popup.GetCurrentItem() + delta
	if index < 0 {
		index = 0
	}
	maxIndex := min(len(e.completionItems), 8) - 1
	if index > maxIndex {
		index = maxIndex
	}
	e.popup.SetCurrentItem(index)
}

func (e *SQLEditor) updatePopupRect() {
	if !e.popupVisible || len(e.completionItems) == 0 {
		return
	}

	x, y, width, height := e.textArea.GetInnerRect()
	_, _, cursorRow, cursorColumn := e.textArea.GetCursor()

	itemCount := min(len(e.completionItems), 8)
	popupWidth := 32
	for _, item := range e.completionItems[:itemCount] {
		lineWidth := runeWidth(item.Label)
		if item.Kind != "" {
			lineWidth += runeWidth(item.Kind) + 4
		}
		if lineWidth+4 > popupWidth {
			popupWidth = lineWidth + 4
		}
	}
	popupWidth = min(popupWidth, max(width, 24))
	popupHeight := min(itemCount+2, max(height, 3))

	popupX := x + cursorColumn
	if popupX+popupWidth > x+width {
		popupX = x + width - popupWidth
	}
	if popupX < x {
		popupX = x
	}

	popupY := y + cursorRow + 1
	if popupY+popupHeight > y+height {
		popupY = y + cursorRow - popupHeight
	}
	if popupY < y {
		popupY = y
	}

	e.popup.SetRect(popupX, popupY, popupWidth, popupHeight)
}

func shouldShowAutocomplete(ctx coreeditor.CompletionContext, force bool) bool {
	if force {
		return true
	}
	return strings.TrimSpace(ctx.Prefix) != ""
}

func shouldHideAutocomplete(ctx coreeditor.CompletionContext, items []coreeditor.CompletionItem) bool {
	if len(items) != 1 {
		return false
	}
	prefix := strings.ToLower(strings.TrimSpace(ctx.Prefix))
	if prefix == "" {
		return false
	}
	item := items[0]
	return strings.ToLower(item.Label) == prefix || strings.ToLower(item.Insert) == prefix
}

func (e *SQLEditor) outdentSelection() {
	selection, start, end := e.textArea.GetSelection()
	text := e.textArea.GetText()
	updated, nextStart, nextEnd := outdentText(text, start, end, selection != "")
	if updated == text {
		return
	}
	e.textArea.SetText(updated, false)
	e.textArea.Select(nextStart, nextEnd)
}

func outdentText(text string, start, end int, hasSelection bool) (string, int, int) {
	if start > end {
		start, end = end, start
	}
	if start < 0 {
		start = 0
	}
	if end < 0 {
		end = 0
	}
	if start > len(text) {
		start = len(text)
	}
	if end > len(text) {
		end = len(text)
	}

	if !hasSelection || start == end {
		lineStart := currentLineStart(text, end)
		lineEnd := currentLineEnd(text, end)
		line, removed := trimLeadingIndent(text[lineStart:lineEnd])
		if removed == 0 {
			return text, end, end
		}
		updated := text[:lineStart] + line + text[lineEnd:]
		cursor := max(lineStart, end-removed)
		return updated, cursor, cursor
	}

	blockStart := currentLineStart(text, start)
	blockEnd := currentLineEnd(text, end)
	segments := strings.SplitAfter(text[blockStart:blockEnd], "\n")
	current := blockStart
	nextStart, nextEnd := start, end

	var b strings.Builder
	for _, segment := range segments {
		line := segment
		newline := ""
		if strings.HasSuffix(line, "\n") {
			line = strings.TrimSuffix(line, "\n")
			newline = "\n"
		}

		trimmed, removed := trimLeadingIndent(line)
		if removed > 0 {
			if current <= nextStart {
				nextStart -= min(removed, nextStart-current)
			}
			if current <= nextEnd {
				nextEnd -= min(removed, nextEnd-current)
			}
		}

		b.WriteString(trimmed)
		b.WriteString(newline)
		current += len(segment)
	}

	updated := text[:blockStart] + b.String() + text[blockEnd:]
	return updated, max(blockStart, nextStart), max(blockStart, nextEnd)
}

func trimLeadingIndent(line string) (string, int) {
	switch {
	case strings.HasPrefix(line, "\t"):
		return strings.TrimPrefix(line, "\t"), 1
	case strings.HasPrefix(line, "    "):
		return strings.TrimPrefix(line, "    "), 4
	}

	removed := 0
	for removed < len(line) && removed < 4 && line[removed] == ' ' {
		removed++
	}
	if removed == 0 {
		return line, 0
	}
	return line[removed:], removed
}

func currentLineStart(text string, offset int) int {
	if offset < 0 {
		return 0
	}
	if offset > len(text) {
		offset = len(text)
	}
	return strings.LastIndex(text[:offset], "\n") + 1
}

func currentLineEnd(text string, offset int) int {
	if offset < 0 {
		offset = 0
	}
	if offset > len(text) {
		offset = len(text)
	}
	if index := strings.Index(text[offset:], "\n"); index >= 0 {
		return offset + index
	}
	return len(text)
}

func isOutdentTrigger(event *tcell.EventKey) bool {
	if event == nil {
		return false
	}
	if event.Key() == tcell.KeyBacktab {
		return true
	}
	return event.Key() == tcell.KeyTab && event.Modifiers()&tcell.ModShift != 0
}
