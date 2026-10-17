package tui

import (
	"image/color"

	"charm.land/lipgloss/v2"

	"github.com/shinokamix/lopper/apps/lopper/internal/lopper"
)

type theme struct {
	title        lipgloss.Style
	subtle       lipgloss.Style
	repo         lipgloss.Style
	cursor       lipgloss.Style
	picked       lipgloss.Style
	pickedCursor lipgloss.Style
	selected     lipgloss.Style
	caret        lipgloss.Style
	failure      lipgloss.Style
	note         map[lopper.NoteKind]lipgloss.Style
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
		note: map[lopper.NoteKind]lipgloss.Style{
			lopper.NotePlain:  subtle,
			lopper.NoteWork:   plain.Foreground(c("#9A6700", "#D29922")),
			lopper.NoteMerged: plain.Foreground(c("#8250DF", "#A371F7")),
		},
	}
}
