package tui

// helpText lists the hotkeys (the help overlay, ?).
var helpText = []struct{ section, keys string }{
	{"Global", "1-6 or tab switch view · ? help · R refresh · K kill switch (stop/start company) · q quit"},
	{"Lists", "j/k ↑/↓ move · g/G top/bottom · pgup/pgdn · enter open · esc back"},
	{"Dashboard", "enter live run (or tasks) of agent · t its tasks · f its flow"},
	{"Tasks", "/ filter: p:PROJECT s:STATUS a:AGENT ! open conv words · ! attention · enter detail"},
	{"Task actions", "p pause · u resume · r retry · n run · x cancel · a reassign · P priority · c comment · d directive (!text urgent) · L live run · f flow"},
	{"Run", "f follow · j/k scroll · G newest · t task · esc back"},
	{"Flow", "g graph/timeline · p project · a agent · t task · x clear · j/k G scroll"},
	{"Feed", "! attention · p project · / filter · x clear · j/k G scroll"},
	{"Inbox", "←/→ option · enter answer · a free text · y approve · n reject · t task"},
	{"Confirm", "y/enter sends, n/esc cancels; red marks what waits on you"},
}

func (m *Model) helpLines(w int) []string {
	lines := []string{stTitle.Render("factory ui — hotkeys") + stFaint.Render("  (any key closes)"), ""}
	for _, h := range helpText {
		head := stBold.Render(fit(h.section, 13))
		for i, l := range wrap(h.keys, max(w-14, 20)) {
			if i == 0 {
				lines = append(lines, head+" "+l)
			} else {
				lines = append(lines, fit("", 14)+l)
			}
		}
	}
	return lines
}
