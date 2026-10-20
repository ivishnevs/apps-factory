package tui

// helpText lists the hotkeys (the help modal, ?).
var helpText = []struct{ section, keys string }{
	{"Global", "1-6 or tab switch screen · [ ] project · ctrl+k palette · R refresh · K kill switch · q quit · mouse: click, wheel"},
	{"Prompt bar", "Y approve · N reject · A answer the oldest item waiting on you"},
	{"Dashboard", "h/l panel · j/k move · enter open (run, task) · z zoom panel · f flow of agent"},
	{"Board", "←→↑↓ move · enter open card · n new task · / filter (a:AGENT s:STATUS ! open words) · ! needs you · z cancelled"},
	{"New task", "tab/shift+tab field · ←→ choose · space toggle · ctrl+e description in $EDITOR · ctrl+s create · esc cancel"},
	{"Task actions", "p pause · u resume · r retry · n run · x cancel · a reassign · P priority · c comment · d directive (!text urgent) · L live run · t transcript · o open PR · f flow"},
	{"Run", "t transcript · e errors only · f follow · j/k scroll · G newest · T task · esc back"},
	{"Transcript", "j/k entry · enter expand/collapse · E all · e errors only · l local/core source · f follow · T task · esc back"},
	{"Flow", "g lanes/pairs · a agent · t task · x clear · j/k G scroll"},
	{"Feed", "! attention · / filter · x clear · j/k G scroll"},
	{"Inbox", "↑↓ item · ←→ option · enter answer · a free text · v preview artifact · y approve · n reject · t task"},
	{"Chat", "j/k report · enter write · enter send · @name address · esc stop typing · pgup/pgdn or ctrl+u/ctrl+d scroll · t transcript of the reply"},
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
