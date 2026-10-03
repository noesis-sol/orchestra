package tui

import (
	"slices"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
)

// boundedSelect is a huh Select whose cursor stops at the first and last option, as a list's does.
// huh's wraps around on purpose (Up on the first option moves to the last), and nothing turns that
// off; with two options every Up or Down then moves the cursor, so it seems to bounce between them.
// It drops an Up on the first option and a Down on the last before the Select sees them. It drops
// the filter key too, and leaves it out of the help line: filtering narrows the list, so its ends
// would no longer be the first and last option, and a handful of options gains nothing from it.
// Turning Filter off in the keymap isn't enough: the Select turns it back on whenever it is left.
type boundedSelect[T comparable] struct {
	*huh.Select[T]
	first, last      T
	up, down, filter key.Binding // the form's keys for the Select, enabled
}

// newBoundedSelect gives s the options, then value, and stops its cursor at the options' ends. In
// that order: options set after the value scroll to its option, hiding those above it.
func newBoundedSelect[T comparable](s *huh.Select[T], value *T, opts ...huh.Option[T]) *boundedSelect[T] {
	return &boundedSelect[T]{
		Select: s.Options(opts...).Value(value),
		first:  opts[0].Value,
		last:   opts[len(opts)-1].Value,
	}
}

// WithKeyMap sets the form's keys on the Select, and takes its Up, Down and filter keys.
func (b *boundedSelect[T]) WithKeyMap(k *huh.KeyMap) huh.Field {
	b.Select.WithKeyMap(k)
	enabled := func(kb key.Binding) key.Binding { return key.NewBinding(key.WithKeys(kb.Keys()...)) }
	b.up, b.down, b.filter = enabled(k.Select.Up), enabled(k.Select.Down), enabled(k.Select.Filter)
	return b
}

// Update hands msg to the Select unless it is a key the select drops. It returns the boundedSelect,
// which the form keeps in place of the field.
func (b *boundedSelect[T]) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok && b.drops(k) {
		return b, nil
	}
	_, cmd := b.Select.Update(msg) // the Select itself
	return b, cmd
}

// drops says whether the select drops k: the filter key, Up on the first option, Down on the last.
func (b *boundedSelect[T]) drops(k tea.KeyMsg) bool {
	if key.Matches(k, b.filter) {
		return true
	}
	hovered, ok := b.Hovered()
	switch {
	case !ok:
		return false
	case key.Matches(k, b.up):
		return hovered == b.first
	case key.Matches(k, b.down):
		return hovered == b.last
	}
	return false
}

// KeyBinds are the Select's keys for the help line, without the filter key.
func (b *boundedSelect[T]) KeyBinds() []key.Binding {
	return slices.DeleteFunc(b.Select.KeyBinds(), func(kb key.Binding) bool {
		return slices.Equal(kb.Keys(), b.filter.Keys())
	})
}
