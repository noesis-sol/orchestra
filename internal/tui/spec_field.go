package tui

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/filepicker"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/noesis-sol/orchestra/internal/project"
)

// specField is the start question's field for a spec's file: a path typed or pasted, with Tab
// completing it, or a file chosen by browsing. ~ and ~/ stand for the home folder, and a relative path
// starts at the repository. It is huh's text input, holding the path as typed, with a file picker
// (bubbles' filepicker, which huh's FilePicker wraps) that Ctrl+O opens below it. huh's FilePicker
// isn't used itself: it doesn't say which folder it shows, and choosing a file there moves the form
// on to the next field.
//
// The path must name a file of text with something in it, as a spec run reads it (project.ReadSpec).
// In huh's accessible form (TERM=dumb) the field asks for the path as a line, with the same checks.
type specField struct {
	repo, home string  // where a relative path starts, and what ~ stands for ("" when unknown)
	value      *string // the path, resolved: absolute and clean, or "" while nothing is typed
	typed      string  // the path as typed: the input's value
	suggested  string  // the typed path the suggestions were last asked for
	input      *huh.Input
	own        *huh.Theme // the input's theme: the form's, without the border the field draws itself
	theme      *huh.Theme // the form's; nil until the form gives it
	keys       *huh.KeyMap
	picker     *filepicker.Model // nil while not browsing
	pickErr    error             // the picker's error: a file it doesn't offer was chosen
	quit       key.Binding       // the form's Quit before browsing took Esc from it
	width      int
	focused    bool
	accessible bool
}

// specExtensions are the files offered as specs, by Tab and by the picker: a file of another type
// can still be typed, as long as it is text.
var specExtensions = []string{".md", ".markdown", ".txt"}

// errNotASpec is the picker's error on choosing a file of another type, as huh's FilePicker words it.
var errNotASpec = errors.New(".md, .markdown and .txt files only")

// specPickerHeight is how many entries the picker shows at once.
const specPickerHeight = 10

// The spec field's keys: Ctrl+O browses, Tab completes; while browsing, Esc closes the picker.
var (
	specBrowseKey   = key.NewBinding(key.WithKeys("ctrl+o"), key.WithHelp("ctrl+o", "browse"))
	specCompleteKey = key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "complete"))
	specCloseKey    = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "close"))
)

// errNoSpecPath is the field's error while it is empty.
var errNoSpecPath = errors.New("type the spec's path, or press Ctrl+O to browse for it")

// specSuggestionsMsg brings field the completions of typed, read from its folder.
type specSuggestionsMsg struct {
	field *specField
	typed string
	list  []string
}

// newSpecField is a field for the path of a spec in the repository repo, or anywhere else, which it
// keeps in value resolved (see resolveSpecPath), as typed so far. value must not be nil.
func newSpecField(repo string, value *string) *specField {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "" // ~ stays as typed: a folder of that name
	}
	if abs, err := filepath.Abs(repo); err == nil {
		repo = abs
	}
	f := &specField{repo: repo, home: home, value: value, own: borderless(huh.ThemeCharm())}
	f.input = huh.NewInput().
		Title("Spec file").
		Description("The Markdown or text file that describes what to build. Tab completes its path; a " +
			"relative path starts at the repository, and ~ is your home folder.").
		Validate(f.check).
		Value(&f.typed)
	f.input.WithTheme(f.own)
	*value = f.resolve(f.typed)
	return f
}

// borderless is a copy of theme whose fields draw no border: the spec field draws the input and the
// picker inside its own.
func borderless(theme *huh.Theme) *huh.Theme {
	own := *theme
	own.Focused.Base, own.Blurred.Base = lipgloss.NewStyle(), lipgloss.NewStyle()
	return &own
}

// resolveSpecPath is the spec's path as typed made absolute and clean, or "" when nothing is typed.
// Spaces around it and quotes around it are dropped; ~ and ~/ start at home (unless it is ""), a
// relative path at repo. A path dropped on the terminal, its spaces escaped with backslashes, is the
// file it names when there is no file of the name with them.
func resolveSpecPath(repo, home, typed string) string {
	p := strings.TrimSpace(typed)
	if len(p) >= 2 && (p[0] == '\'' || p[0] == '"') && p[len(p)-1] == p[0] {
		p = p[1 : len(p)-1]
	}
	full := p
	switch {
	case p == "":
		return ""
	case home != "" && (p == "~" || strings.HasPrefix(p, "~/")):
		full = filepath.Join(home, p[1:])
	case !filepath.IsAbs(p):
		full = filepath.Join(repo, p)
	}
	full = filepath.Clean(full)
	if strings.Contains(p, `\`) && !exists(full) {
		if u := resolveSpecPath(repo, home, unescapeShell(p)); exists(u) {
			return u
		}
	}
	return full
}

// unescapeShell drops the backslash before each character a shell would take literally.
func unescapeShell(p string) string {
	var b strings.Builder
	escaped := false
	for _, r := range p {
		if r == '\\' && !escaped {
			escaped = true
			continue
		}
		escaped = false
		b.WriteRune(r)
	}
	return b.String()
}

// exists reports whether there is a file or folder at p.
func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func (f *specField) resolve(typed string) string { return resolveSpecPath(f.repo, f.home, typed) }

// check is the field's validation: the path must name an existing, readable, non-empty file of text.
// Its errors name the path as typed.
func (f *specField) check(typed string) error {
	p := f.resolve(typed)
	shown := strings.TrimSpace(typed)
	if p == "" {
		if f.accessible {
			return errors.New("type the spec's path")
		}
		return errNoSpecPath
	}
	info, err := os.Stat(p)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("no such file: %s", shown)
	case err != nil:
		return fmt.Errorf("cannot read %s: %w", shown, pathCause(err))
	case info.IsDir():
		return fmt.Errorf("%s is a folder", shown)
	case !info.Mode().IsRegular(): // a device or a pipe, which reading might never end
		return fmt.Errorf("%s isn't a file", shown)
	}
	_, err = project.ReadSpec(p)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, project.ErrSpecEmpty):
		return fmt.Errorf("%s is empty", shown)
	case errors.Is(err, project.ErrSpecNotText):
		return fmt.Errorf("%s doesn't look like text", shown)
	}
	return fmt.Errorf("cannot read %s: %w", shown, pathCause(err))
}

// pathCause is err without the path a *fs.PathError adds, which the field's errors give as typed.
func pathCause(err error) error {
	if pathErr, ok := errors.AsType[*fs.PathError](err); ok {
		return pathErr.Err
	}
	return err
}

// specSuggestions are Tab's completions of typed, a path as resolveSpecPath takes it: the folders in
// the folder typed so far, and the spec files there, whose names start with what follows its last /,
// each as the whole path typed with that name. Hidden entries are offered once that starts with a dot.
func specSuggestions(repo, home, typed string) []string {
	if home != "" && strings.TrimSpace(typed) == "~" {
		return []string{typed + "/"}
	}
	cut := strings.LastIndex(typed, "/") + 1
	folder, name := typed[:cut], typed[cut:]
	dir := repo
	if strings.TrimSpace(folder) != "" {
		dir = resolveSpecPath(repo, home, folder)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil // no folder there to complete in
	}
	var list []string
	for _, e := range entries {
		n := e.Name()
		if !strings.HasPrefix(n, name) || (strings.HasPrefix(n, ".") && !strings.HasPrefix(name, ".")) {
			continue
		}
		switch {
		case isFolder(dir, e):
			list = append(list, folder+n+"/")
		case isSpecName(n):
			list = append(list, folder+n)
		}
	}
	return list
}

// isFolder reports whether e, an entry of dir, is a folder or a link to one.
func isFolder(dir string, e fs.DirEntry) bool {
	if e.Type()&fs.ModeSymlink == 0 {
		return e.IsDir()
	}
	info, err := os.Stat(filepath.Join(dir, e.Name()))
	return err == nil && info.IsDir()
}

// isSpecName reports whether a file of this name is offered as a spec.
func isSpecName(name string) bool {
	return slices.ContainsFunc(specExtensions, func(ext string) bool { return strings.HasSuffix(name, ext) })
}

// shownPath is the file at the absolute path p as the field shows it: relative to the repository when
// inside it, else from ~ when in the home folder, else absolute.
func (f *specField) shownPath(p string) string {
	if rel, err := filepath.Rel(f.repo, p); err == nil && filepath.IsLocal(rel) {
		return rel
	}
	return tildePath(f.home, p)
}

// tildePath is the absolute path p from ~ when it is in the home folder home.
func tildePath(home, p string) string {
	rel, err := filepath.Rel(home, p)
	switch {
	case home == "" || err != nil:
		return p
	case rel == ".":
		return "~"
	case filepath.IsLocal(rel):
		return "~/" + filepath.ToSlash(rel)
	}
	return p
}

// suggest asks for the completions of the path as typed when they were last asked for another.
func (f *specField) suggest() tea.Cmd {
	if f.typed == f.suggested {
		return nil
	}
	return f.suggestions()
}

// suggestions asks for the completions of the path as typed, and keeps value up to date with it. The
// folder is read on a goroutine of its own, as Bubble Tea runs a command, so that a slow one doesn't
// hold up the typing; the input takes the completions if the path is still the one they complete.
func (f *specField) suggestions() tea.Cmd {
	f.suggested = f.typed
	*f.value = f.resolve(f.typed)
	field, repo, home, typed := f, f.repo, f.home, f.typed
	return func() tea.Msg {
		return specSuggestionsMsg{field: field, typed: typed, list: specSuggestions(repo, home, typed)}
	}
}

// setTyped puts p in the input, with the cursor at its end.
func (f *specField) setTyped(p string) {
	f.typed = ""
	f.input.Value(&f.typed) // empty first: the input keeps a cursor that fits the new text where it was
	f.typed = p
	f.input.Value(&f.typed)
}

// browseFrom is the folder the picker opens in: the one the path typed so far names, or the one it is
// in, or else the repository.
func (f *specField) browseFrom() string {
	if p := f.resolve(f.typed); p != "" {
		for _, dir := range []string{p, filepath.Dir(p)} {
			if info, err := os.Stat(dir); err == nil && info.IsDir() {
				return dir
			}
		}
	}
	return f.repo
}

// openPicker opens the picker below the input, and takes Esc from the form's Quit while it is open:
// huh's form checks Quit before the field sees a key, and Esc then closes the picker instead.
func (f *specField) openPicker() tea.Cmd {
	f.picker = f.newPicker(f.browseFrom())
	f.pickErr = f.unreadable(f.picker.CurrentDirectory)
	if f.keys != nil {
		f.quit = f.keys.Quit
		f.keys.Quit = withoutKey(f.keys.Quit, "esc")
	}
	return tea.WindowSize() // the form measures itself on a window size only
}

// newPicker is a picker in the folder dir, which it has read, its cursor on the first entry.
func (f *specField) newPicker(dir string) *filepicker.Model {
	fp := filepicker.New()
	fp.CurrentDirectory = dir
	fp.AllowedTypes = specExtensions
	fp.ShowHidden, fp.ShowPermissions, fp.ShowSize = false, false, false
	fp.AutoHeight = false
	fp.SetHeight(specPickerHeight)
	fp.KeyMap = f.pickerKeys()
	fp.Styles = pickerStyles(f.styleTheme())
	fp, _ = fp.Update(fp.Init()()) // the folder read now, so that the picker opens on it
	return &fp
}

// newPickerOn is a picker in the folder dir, which it has read, its cursor on the entry name, or on the
// first entry when dir lists none of that name. The cursor moves there as ↓ moves it, which can't take it
// past the end of the list even if dir changed since pickerIndex read it.
func (f *specField) newPickerOn(dir, name string) *filepicker.Model {
	fp := f.newPicker(dir)
	keys := fp.KeyMap
	fp.KeyMap = filepicker.KeyMap{Down: key.NewBinding(key.WithKeys("down"))} // ↓, whatever the form's keys
	for range pickerIndex(dir, name) {
		*fp, _ = fp.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	fp.KeyMap = keys
	return fp
}

// pickerIndex is where bubbles' picker lists the entry name of the folder dir, or -1 when it doesn't (or
// dir can't be read): the picker sorts a folder's entries folders first, then by name, and leaves hidden
// ones out. A link to a folder sorts among the files, as the picker has it.
func pickerIndex(dir, name string) int {
	entries, err := os.ReadDir(dir) // by name
	if err != nil {
		return -1
	}
	entries = slices.DeleteFunc(entries, func(e fs.DirEntry) bool {
		hidden, _ := filepicker.IsHidden(e.Name()) // an entry it can't tell about is listed, as the picker does
		return hidden
	})
	slices.SortStableFunc(entries, func(a, b fs.DirEntry) int {
		switch {
		case a.IsDir() == b.IsDir():
			return 0
		case a.IsDir():
			return -1
		}
		return 1
	})
	return slices.IndexFunc(entries, func(e fs.DirEntry) bool { return e.Name() == name })
}

// specPickerDraws is how many times at most the field draws the picker's list, reading its folder again
// each time an entry has gone, the last time letting the picker panic: an entry goes between a folder's
// reading and its drawing, microseconds apart, only by chance.
const specPickerDraws = 3

// pickerView is the picker's list. bubbles' picker draws each entry from the FileInfo the entry gives,
// without checking that it gave one: an entry removed or renamed since the folder was read gives none,
// and drawing it panics. The field then reads the folder again and draws it as it is now (see reread).
func (f *specField) pickerView() string {
	for range specPickerDraws - 1 {
		if view, ok := drawPicker(f.picker); ok {
			return view
		}
		f.reread()
	}
	return f.picker.View()
}

// drawPicker is fp's view, and false if drawing it panicked on a runtime error, as on an entry with no
// FileInfo.
func drawPicker(fp *filepicker.Model) (view string, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			if _, isRuntime := r.(runtime.Error); !isRuntime {
				panic(r) // not the picker's misstep
			}
		}
	}()
	return fp.View(), true
}

// reread reads the picker's folder again after an entry in it went, the cursor back on the first entry
// (where it was may be past the end now). A folder that can't be read any more (removed, say) gives way
// to the nearest one above it that can, and the field's error says why.
func (f *specField) reread() {
	dir := f.picker.CurrentDirectory
	if err := f.unreadable(dir); err != nil {
		f.pickErr = err
		for dir != filepath.Dir(dir) && f.unreadable(dir) != nil {
			dir = filepath.Dir(dir)
		}
	}
	f.picker = f.newPicker(dir)
}

// closePicker closes the picker, if open, and gives the form's Quit its Esc back.
func (f *specField) closePicker() tea.Cmd {
	if f.picker == nil {
		return nil
	}
	f.picker, f.pickErr = nil, nil
	if f.keys != nil {
		f.keys.Quit = f.quit
	}
	return tea.WindowSize()
}

// browse handles a key while the picker is open: Esc closes it, and choosing a spec file puts its
// path in the input and closes it, leaving the focus on the field. Choosing another file is an error,
// and so is going to a folder that can't be read: the picker stays where it was. Going up a folder puts
// the cursor on the one it left.
func (f *specField) browse(k tea.KeyMsg) tea.Cmd {
	if key.Matches(k, specCloseKey) {
		return f.closePicker()
	}
	f.pickErr = nil
	fp, cmd := f.picker.Update(k)
	if from := f.picker.CurrentDirectory; fp.CurrentDirectory != from && cmd != nil { // the folder to read
		// bubbles' picker would show the folder it left under the name of one it can't read.
		if f.pickErr = f.unreadable(fp.CurrentDirectory); f.pickErr != nil {
			return nil
		}
		if fp.CurrentDirectory == filepath.Dir(from) {
			// Up a folder, bubbles' picker puts back the cursor it had there, by its place in the list: past the
			// end of a folder that lost entries since, where choosing it panics, and on another entry in one
			// that changed. A picker of the field's own puts it on the folder it came from instead.
			fp = *f.newPickerOn(fp.CurrentDirectory, filepath.Base(from))
		} else {
			fp, _ = fp.Update(cmd()) // read now, as on opening
		}
		cmd = nil
	}
	f.picker = &fp
	if ok, p := fp.DidSelectFile(k); ok {
		f.setTyped(f.shownPath(p))
		return tea.Batch(f.closePicker(), f.suggest())
	}
	if ok, _ := fp.DidSelectDisabledFile(k); ok {
		f.pickErr = errNotASpec
	}
	return cmd
}

// unreadable is why the picker can't show the folder dir, or nil when it can.
func (f *specField) unreadable(dir string) error {
	if _, err := os.ReadDir(dir); err != nil {
		return fmt.Errorf("cannot open %s: %w", tildePath(f.home, dir), pathCause(err))
	}
	return nil
}

// pickerKeys are the picker's keys: the form's for huh's file picker, all enabled, but Esc, which
// closes the picker rather than going up a folder.
func (f *specField) pickerKeys() filepicker.KeyMap {
	keys := f.keys
	if keys == nil {
		keys = huh.NewDefaultKeyMap()
	}
	k := keys.FilePicker
	return filepicker.KeyMap{
		GoToTop: enabledKey(k.GotoTop), GoToLast: enabledKey(k.GotoBottom),
		Down: enabledKey(k.Down), Up: enabledKey(k.Up),
		PageUp: enabledKey(k.PageUp), PageDown: enabledKey(k.PageDown),
		Back: withoutKey(k.Back, "esc"), Open: enabledKey(k.Open), Select: enabledKey(k.Select),
	}
}

// enabledKey is kb enabled.
func enabledKey(kb key.Binding) key.Binding {
	return key.NewBinding(key.WithKeys(kb.Keys()...), key.WithHelp(kb.Help().Key, kb.Help().Desc))
}

// withoutKey is kb enabled, without the key k.
func withoutKey(kb key.Binding, k string) key.Binding {
	keys := slices.DeleteFunc(slices.Clone(kb.Keys()), func(s string) bool { return s == k })
	return key.NewBinding(key.WithKeys(keys...), key.WithHelp(kb.Help().Key, kb.Help().Desc))
}

// pickerStyles style the picker as huh's FilePicker does with theme.
func pickerStyles(theme *huh.Theme) filepicker.Styles {
	focused := theme.Focused
	return filepicker.Styles{
		Cursor:           focused.TextInput.Prompt,
		Directory:        focused.Directory,
		File:             focused.File,
		DisabledFile:     focused.TextInput.Placeholder,
		Selected:         focused.SelectedOption,
		DisabledSelected: focused.TextInput.Placeholder,
		EmptyDirectory:   focused.TextInput.Placeholder.PaddingLeft(2).SetString("No files found."),
	}
}

// styleTheme is the form's theme, or huh's Charm theme until the form gives one.
func (f *specField) styleTheme() *huh.Theme {
	if f.theme == nil {
		return huh.ThemeCharm()
	}
	return f.theme
}

func (f *specField) styles() *huh.FieldStyles {
	if f.focused {
		return &f.styleTheme().Focused
	}
	return &f.styleTheme().Blurred
}

// Init initializes the input.
func (f *specField) Init() tea.Cmd { return f.input.Init() }

// Update hands keys to the picker while it is open, and else to the input, but Ctrl+O, which opens
// it; and keeps the completions in step with what is typed.
func (f *specField) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var pickerCmd tea.Cmd
	switch msg := msg.(type) {
	case specSuggestionsMsg:
		if msg.field == f && msg.typed == f.typed {
			f.input.Suggestions(msg.list)
		}
		return f, nil
	case tea.KeyMsg:
		switch {
		case f.picker != nil:
			return f, f.browse(msg)
		case f.focused && key.Matches(msg, specBrowseKey):
			return f, f.openPicker()
		}
	case tea.WindowSizeMsg:
		// Not to the picker, whose list keeps its height (and its scrolling) rather than the window's.
	default:
		if f.picker != nil { // the folder it read, say
			var fp filepicker.Model
			fp, pickerCmd = f.picker.Update(msg)
			f.picker = &fp
		}
	}
	_, cmd := f.input.Update(msg)
	return f, tea.Batch(pickerCmd, cmd, f.suggest())
}

// View draws the input inside the field's border and, below it, the picker while it is open, or the
// hint that Ctrl+O opens it while the field has the focus.
func (f *specField) View() string {
	view := f.input.View()
	switch {
	case f.picker != nil:
		list := strings.TrimSuffix(f.pickerView(), "\n") // first: it may read another folder
		help := f.styleTheme().Help
		folder := tildePath(f.home, f.picker.CurrentDirectory)
		if !strings.HasSuffix(folder, string(filepath.Separator)) {
			folder += string(filepath.Separator)
		}
		view += "\n" + help.ShortDesc.Render(folder) + "\n" + list
	case f.focused:
		help := f.styleTheme().Help
		view += "\n" + help.ShortKey.Render(specBrowseKey.Help().Key) + " " + help.ShortDesc.Render("Browse…")
	}
	return f.styles().Base.Width(f.width).Render(view)
}

// Focus focuses the input, and asks for the completions of what it holds.
func (f *specField) Focus() tea.Cmd {
	f.focused = true
	return tea.Batch(f.input.Focus(), f.suggestions())
}

// Blur closes the picker and leaves the input, which checks the path.
func (f *specField) Blur() tea.Cmd {
	f.focused = false
	cmd := f.closePicker()
	return tea.Batch(cmd, f.input.Blur())
}

// Error is the picker's error while it is open, else the input's.
func (f *specField) Error() error {
	if f.picker != nil {
		return f.pickErr
	}
	return f.input.Error()
}

// entered reports whether the field was left holding a spec's path: the start question draws what
// it asks of the spec next only then.
func (f *specField) entered() bool { return !f.focused && *f.value != "" && f.input.Error() == nil }

// Run runs the field in a form of its own.
func (f *specField) Run() error { return huh.Run(f) }

// RunAccessible asks for the path as a line on w, read from r, until it names a spec file.
func (f *specField) RunAccessible(w io.Writer, r io.Reader) error {
	if _, err := fmt.Fprintln(w, "The Markdown or text file that describes what to build: a relative path "+
		"starts at the repository, and ~ is your home folder."); err != nil {
		return err
	}
	if err := f.input.RunAccessible(w, r); err != nil {
		return err
	}
	*f.value = f.resolve(f.typed)
	return nil
}

// Skip is false: the field is always asked.
func (f *specField) Skip() bool { return false }

// Zoom is false: the picker opens below the input, which stays in sight.
func (f *specField) Zoom() bool { return false }

// KeyBinds are the picker's keys while it is open, else the input's: the field's hint names Ctrl+O.
func (f *specField) KeyBinds() []key.Binding {
	if f.picker != nil {
		k := f.picker.KeyMap
		return []key.Binding{
			key.NewBinding(key.WithKeys(slices.Concat(k.Up.Keys(), k.Down.Keys())...), key.WithHelp("↑↓", "move")),
			key.NewBinding(key.WithKeys(k.Back.Keys()...), key.WithHelp("←", "up a folder")),
			key.NewBinding(key.WithKeys(k.Open.Keys()...), key.WithHelp("enter", "open or choose")),
			specCloseKey,
		}
	}
	return f.input.KeyBinds()
}

// WithTheme sets the form's theme, the first given as huh's fields keep it: the field draws its border,
// the input inside it the rest.
func (f *specField) WithTheme(theme *huh.Theme) huh.Field {
	if f.theme != nil || theme == nil {
		return f
	}
	f.theme = theme
	*f.own = *borderless(theme) // the input's theme, in place
	return f
}

// WithAccessible sets the field's accessible mode.
func (f *specField) WithAccessible(accessible bool) huh.Field {
	f.accessible = accessible
	f.input.WithAccessible(accessible) //nolint:staticcheck // huh's accessible form still calls it
	return f
}

// WithKeyMap sets the form's keys, keeping keys to take Esc from its Quit while browsing. The input
// gets them with Tab completing rather than moving on: Enter moves on.
func (f *specField) WithKeyMap(keys *huh.KeyMap) huh.Field {
	f.keys = keys
	own := *keys
	own.Input.AcceptSuggestion = specCompleteKey
	own.Input.Next = withoutKey(keys.Input.Next, "tab")
	f.input.WithKeyMap(&own)
	return f
}

// WithWidth sets the field's width, the input's inside the field's border.
func (f *specField) WithWidth(width int) huh.Field {
	f.width = width
	f.input.WithWidth(width - f.styleTheme().Focused.Base.GetHorizontalFrameSize())
	return f
}

// WithHeight does nothing: the field is as tall as what it shows, and the form scrolls to it.
func (f *specField) WithHeight(int) huh.Field { return f }

// WithPosition sets the input's position in the form, which says whether Enter moves on or submits.
func (f *specField) WithPosition(p huh.FieldPosition) huh.Field {
	f.input.WithPosition(p)
	return f
}

// GetKey is the field's key: none.
func (f *specField) GetKey() string { return "" }

// GetValue is the path, resolved.
func (f *specField) GetValue() any { return *f.value }
