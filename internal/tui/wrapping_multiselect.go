package tui

import (
	"slices"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// wrappingMultiSelect is a huh MultiSelect whose options' labels wrap between words at the field's
// width, each line after the first under the label's start. huh v1.0.0 draws a label on one line and
// leaves a long one to the field's style, which wraps it back to the field's left margin, under the
// cursor and the checkbox. It wraps the labels itself whenever the field's width changes, and gives
// them back whole in huh's accessible form, which prints each option on a line of its own.
type wrappingMultiSelect[T comparable] struct {
	*huh.MultiSelect[T]
	opts       []huh.Option[T] // as given, the labels whole
	shown      []huh.Option[T] // the MultiSelect's own, which hold the ticks on screen
	value      *[]T            // the field's value
	theme      *huh.Theme      // the field's, for the room its frame, cursor and checkbox take
	room       int             // the columns the labels are wrapped to; 0 while they are whole
	accessible bool
}

// newWrappingMultiSelect gives m the options, then value, and wraps their labels at its width.
func newWrappingMultiSelect[T comparable](m *huh.MultiSelect[T], value *[]T,
	opts ...huh.Option[T]) *wrappingMultiSelect[T] {
	// m keeps the slice it is given and ticks the value's options in it.
	return &wrappingMultiSelect[T]{MultiSelect: m.Options(opts...).Value(value), opts: slices.Clone(opts),
		shown: opts, value: value}
}

// WithTheme sets the field's theme, the first one given, as huh does.
func (w *wrappingMultiSelect[T]) WithTheme(theme *huh.Theme) huh.Field {
	if w.theme == nil {
		w.theme = theme
	}
	w.MultiSelect.WithTheme(theme)
	return w
}

// WithWidth sets the field's width and wraps the labels to the room it leaves them.
func (w *wrappingMultiSelect[T]) WithWidth(width int) huh.Field {
	w.MultiSelect.WithWidth(width)
	if !w.accessible {
		w.wrap(max(width-w.indent(), 0))
	}
	return w
}

// WithAccessible sets the field's accessible mode, in which the labels are whole.
func (w *wrappingMultiSelect[T]) WithAccessible(accessible bool) huh.Field {
	w.accessible = accessible
	if accessible {
		w.wrap(0)
	}
	w.MultiSelect.WithAccessible(accessible) //nolint:staticcheck // huh's accessible form still calls it
	return w
}

// Update hands msg to the MultiSelect and returns the wrappingMultiSelect, which the form keeps in
// place of the field.
func (w *wrappingMultiSelect[T]) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	_, cmd := w.MultiSelect.Update(msg)
	return w, cmd
}

// indent is the columns left of a label: the field's frame, the cursor and the checkbox, the widest
// of the focused and the blurred field's.
func (w *wrappingMultiSelect[T]) indent() int {
	theme := w.theme
	if theme == nil {
		theme = huh.ThemeCharm() // the MultiSelect's own default
	}
	indent := 0
	for _, s := range []huh.FieldStyles{theme.Focused, theme.Blurred} {
		prefix := max(lipgloss.Width(s.SelectedPrefix.String()), lipgloss.Width(s.UnselectedPrefix.String()))
		indent = max(indent, s.Base.GetHorizontalFrameSize()+lipgloss.Width(s.MultiSelectSelector.String())+prefix)
	}
	return indent
}

// wrap sets the options with their labels wrapped to room columns, or whole for 0, keeping the
// ticks on screen and the cursor on its option. huh's Options can tick an option but never untick
// one, and ticks the value's options, which select all and select none leave as they were until the
// field is left; so wrap sets the value from the ticks first, as leaving the field would. Setting the
// options moves huh's cursor to the first selected option, so it moves it back as the arrow keys would.
func (w *wrappingMultiSelect[T]) wrap(room int) {
	if room == w.room {
		return
	}
	w.room = room
	opts := slices.Clone(w.opts)
	value, changed := make([]T, 0), false
	for i := range opts {
		if room > 0 {
			opts[i].Key = ansi.Wrap(opts[i].Key, room, "")
		}
		ticked := w.shown[i] == w.shown[i].Selected(true) // Option hides its tick but compares it
		opts[i] = opts[i].Selected(ticked)
		if ticked {
			value = append(value, opts[i].Value)
		}
		changed = changed || ticked != slices.Contains(*w.value, opts[i].Value)
	}
	if changed { // else the value keeps its order, settings.json's until the field is left
		*w.value = value
	}
	hovered, ok := w.Hovered()
	w.Options(opts...)
	w.shown = opts
	at := func(v T) int { return slices.IndexFunc(w.opts, func(o huh.Option[T]) bool { return o.Value == v }) }
	now, _ := w.Hovered()
	if !ok || at(hovered) < 0 {
		return
	}
	for step := at(hovered) - at(now); step != 0; {
		if step < 0 {
			w.MultiSelect.Update(tea.KeyMsg{Type: tea.KeyUp})
			step++
		} else {
			w.MultiSelect.Update(tea.KeyMsg{Type: tea.KeyDown})
			step--
		}
	}
}
