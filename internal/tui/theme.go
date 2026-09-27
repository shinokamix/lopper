package tui

import (
	"image/color"

	"charm.land/lipgloss/v2"

	"github.com/shinokamix/lopper/internal/lopper"
)

// theme is the only place where colors and styles are defined.
type theme struct {
	title    lipgloss.Style
	subtle   lipgloss.Style
	cursor   lipgloss.Style
	selected lipgloss.Style
	level    map[lopper.Level]lipgloss.Style
}

func newTheme(isDark bool) theme {
	ld := lipgloss.LightDark(isDark)
	c := func(light, dark string) color.Color { return ld(lipgloss.Color(light), lipgloss.Color(dark)) }

	accent := c("#2F7D4F", "#7FD1A0")
	return theme{
		title:    lipgloss.NewStyle().Bold(true).Foreground(accent),
		subtle:   lipgloss.NewStyle().Foreground(c("#8A8A8A", "#6C6C6C")),
		cursor:   lipgloss.NewStyle().Background(c("#ECECEC", "#262626")),
		selected: lipgloss.NewStyle().Foreground(accent),
		level: map[lopper.Level]lipgloss.Style{
			lopper.LevelSafe:    lipgloss.NewStyle().Foreground(c("#1E8E3E", "#5FD787")),
			lopper.LevelReview:  lipgloss.NewStyle().Foreground(c("#B26B00", "#FFB454")),
			lopper.LevelKeep:    lipgloss.NewStyle().Foreground(c("#C62828", "#FF6B6B")),
			lopper.LevelUnknown: lipgloss.NewStyle().Foreground(c("#8A8A8A", "#6C6C6C")),
		},
	}
}
