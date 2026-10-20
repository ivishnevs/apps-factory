package tui

// helpText lists the hotkeys (the help modal, ?).
var helpText = []struct{ section, keys string }{
	{"Global", "1-5 or tab switch screen · [ ] project · ctrl+k palette · R refresh · K kill switch · q quit · mouse: click, wheel"},
	{"Prompt bar", "Y approve · N reject · A answer the oldest item waiting on you"},
	{"Dashboard", "h/l panel · j/k move · enter open (run, task) · z zoom panel · f flow of agent"},
	{"Board", "←→↑↓ move · enter open card · / filter (a:AGENT s:STATUS ! open words) · ! needs you · z cancelled"},
	{"Task actions", "p pause · u resume · r retry · n run · x cancel · a reassign · P priority · c comment · d directive (!text urgent) · L live run · f flow"},
	{"Run", "f follow · j/k scroll · G newest · t task · esc back"},
	{"Flow", "g lanes/pairs · a agent · t task · x clear · j/k G scroll"},
	{"Feed", "! attention · / filter · x clear · j/k G scroll"},
	{"Inbox", "↑↓ item · ←→ option · enter answer · a free text · y approve · n reject · t task"},
	{"Confirm", "y/enter sends, n/esc cancels — every action asks first"},
}

func (m *Model) helpLines(w int) []string {
	lines := []string{}
	for _, h := range helpText {
		head := stAccent.Bold(true).Render(fit(h.section, 13))
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
