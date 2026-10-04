package tui

import (
	"image/color"

	"charm.land/lipgloss/v2"

	"github.com/shinokamix/lopper/internal/verdict"
)

// theme defines every color and style.
//
// Color only carries meaning, in GitHub's Primer palette so it reads as on
// GitHub: purple for merged work, yellow for work that exists only locally,
// red for failures. ANSI palette slots would follow the terminal theme, but
// many themes make magenta look red. Everything else, the cursor and the
// selection included, differs by brightness: faint, normal, bold and a
// neutral background.
type theme struct {
	title  lipgloss.Style
	subtle lipgloss.Style
	repo   lipgloss.Style
	cursor lipgloss.Style // band under the cursor row
	picked lipgloss.Style // fainter band under selected rows
	// pickedCursor is the band under the cursor when its row is selected.
	pickedCursor lipgloss.Style
	selected     lipgloss.Style // text of selected rows
	caret        lipgloss.Style // where typed text goes
	failure      lipgloss.Style
	note         map[verdict.NoteKind]lipgloss.Style
}

func newTheme(isDark bool) theme {
	ld := lipgloss.LightDark(isDark)
	c := func(light, dark string) color.Color { return ld(lipgloss.Color(light), lipgloss.Color(dark)) }
	plain := lipgloss.NewStyle()
	subtle := plain.Faint(true)
	return theme{
		title:        plain.Bold(true),
		subtle:       subtle,
		repo:         plain.Bold(true),
		cursor:       plain.Background(c("#DCDCDC", "#3A3A3A")),
		picked:       plain.Background(c("#EEEEEE", "#2A2A2A")),
		pickedCursor: plain.Background(c("#C8C8C8", "#4C4C4C")),
		selected:     plain.Bold(true),
		caret:        plain.Reverse(true),
		failure:      plain.Foreground(c("#CF222E", "#F85149")),
		note: map[verdict.NoteKind]lipgloss.Style{
			verdict.NotePlain:  subtle,
			verdict.NoteWork:   plain.Foreground(c("#9A6700", "#D29922")),
			verdict.NoteMerged: plain.Foreground(c("#8250DF", "#A371F7")),
		},
	}
}
