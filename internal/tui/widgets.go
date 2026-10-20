package tui

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// frame draws lines inside a border of exactly w×h cells with title centered in the top border. A focused
// frame has an accent border and title (the focus ring); thick draws the heavy border (a selected card).
func frame(title string, lines []string, w, h int, border color.Color, focused, thick bool) []string {
	if w < 4 || h < 2 {
		return nil
	}
	b := lipgloss.RoundedBorder()
	if thick {
		b = lipgloss.ThickBorder()
	}
	if focused {
		border = theme.accent
	}
	bs := lipgloss.NewStyle().Foreground(border)
	inner := w - 4
	head := ""
	if title != "" {
		ts := stFaint
		if focused {
			ts = stTitle
		}
		head = " " + ts.Render(cut(ansiStrip(title), max(w-6, 1))) + " "
		if ansiStrip(title) != title {
			head = " " + cut(title, max(w-6, 1)) + " " // a styled title keeps its own styles
		}
	}
	hw := ansi.StringWidth(head)
	leftRun := max((w-2-hw)/2, 0)
	rightRun := max(w-2-hw-leftRun, 0)
	out := []string{bs.Render(b.TopLeft+strings.Repeat(b.Top, leftRun)) + head + bs.Render(strings.Repeat(b.Top, rightRun)+b.TopRight)}
	for i := 0; i < h-2; i++ {
		l := ""
		if i < len(lines) {
			l = lines[i]
		}
		out = append(out, bs.Render(b.Left)+" "+fit(l, inner)+" "+bs.Render(b.Right))
	}
	return append(out, bs.Render(b.BottomLeft+strings.Repeat(b.Bottom, max(w-2, 0))+b.BottomRight))
}

// panel is a dashboard panel: thin rounded border, centered title, accent focus ring.
func panel(title string, lines []string, w, h int, focused bool) []string {
	return frame(title, lines, w, h, theme.faint, focused, false)
}

// card is a board card: thin border in the column's color, heavy accent border when selected.
func card(lines []string, w, h int, accent color.Color, selected bool) []string {
	return frame("", lines, w, h, accent, selected, selected)
}

// scrollTo keeps line sel visible in a window of h lines; it returns the first visible line.
func scrollTo(sel, offset, h, n int) int {
	if sel < offset {
		offset = sel
	}
	if sel >= offset+h {
		offset = sel - h + 1
	}
	return max(min(offset, max(n-h, 0)), 0)
}

var sparkRunes = []rune("▁▂▃▄▅▆▇█")

// spark is a sparkline of values in color c.
func spark(values []float64, c color.Color) string {
	hi := 0.0
	for _, v := range values {
		hi = max(hi, v)
	}
	var b strings.Builder
	for _, v := range values {
		i := 0
		if hi > 0 {
			i = int(v / hi * float64(len(sparkRunes)-1))
		}
		if v > 0 && i == 0 {
			i = 1
		}
		b.WriteRune(sparkRunes[i])
	}
	return lipgloss.NewStyle().Foreground(c).Render(b.String())
}

// gauge is a w-cell bar filled to frac with a green→yellow→red gradient, and an empty dotted rest.
func gauge(frac float64, w int) string {
	if w <= 0 {
		return ""
	}
	frac = min(max(frac, 0), 1)
	n := int(frac*float64(w) + 0.5)
	ramp := lipgloss.Blend1D(max(w, 2), theme.green, theme.yellow, theme.red)
	var b strings.Builder
	for i := 0; i < w; i++ {
		if i < n {
			b.WriteString(lipgloss.NewStyle().Foreground(ramp[i]).Render("█"))
		} else {
			b.WriteString(stDim.Render("░"))
		}
	}
	return b.String()
}

// hbar is a solid bar of n cells (of at most w) in color c, for counts.
func hbar(n, total, w int, c color.Color) string {
	if total <= 0 || w <= 0 {
		return ""
	}
	cells := n * w / total
	if n > 0 && cells == 0 {
		cells = 1
	}
	return lipgloss.NewStyle().Foreground(c).Render(strings.Repeat("▇", cells)) + stDim.Render(strings.Repeat("·", w-cells))
}

// kv is a two-column key/value row.
func kv(k string, v string, kw int) string {
	return stFaint.Render(fit(k, kw)) + v
}

// thousands formats n with separators: 45,230.
func thousands(n int64) string {
	s := fmt.Sprint(n)
	if n < 0 {
		return s
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// pulse is the dot of something live: it breathes with the animation frame.
func pulse(frameN int, c color.Color) string {
	glyphs := []string{"●", "●", "◉", "◉", "●", "●"}
	return lipgloss.NewStyle().Foreground(c).Render(glyphs[frameN%len(glyphs)])
}

// rightAlign puts right at the end of a w-cell line that starts with left.
func rightAlign(left, right string, w int) string {
	rw := ansi.StringWidth(right)
	return fit(left, max(w-rw-1, 0)) + " " + right
}

// spliceLines puts block at column x of lines (each line padded to w).
func spliceLines(lines []string, blk []string, x, y, w int) []string {
	out := append([]string(nil), lines...)
	for len(out) < y+len(blk) {
		out = append(out, "")
	}
	bw := 0
	for _, l := range blk {
		bw = max(bw, ansi.StringWidth(l))
	}
	for i, l := range blk {
		base := fit(out[y+i], w)
		right := ""
		if x+bw < w {
			right = ansi.Cut(base, x+bw, w)
		}
		out[y+i] = ansi.Truncate(base, x, "") + fit(l, min(bw, w-x)) + right
	}
	return out
}
