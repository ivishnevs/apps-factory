package tui

// helpText lists the hotkeys (the help modal, ?).
var helpText = []struct{ section, keys string }{
	{"Global", "1-8 or tab switch screen · [ ] project · ctrl+k palette · ctrl+n new project · R refresh · K kill switch · q quit · mouse: click, wheel"},
	{"Prompt bar", "Y approve · N reject · A answer the oldest item waiting on you · R reply to / M mark read a notification"},
	{"Dashboard", "h/l panel · j/k move · enter open (run, task) · z zoom panel · f flow of agent"},
	{"Board", "←→↑↓ move · enter open card · n new task · / filter (a:AGENT s:STATUS ! open words) · ! needs you · z cancelled"},
	{"New task", "tab/shift+tab field · ←→ choose · space toggle · ctrl+e description in $EDITOR · ctrl+s create · esc cancel"},
	{"New project", "tab/shift+tab field · type a name (the ID follows it) · ←→ repository: new local, new private GitHub, existing (path or URL, branch, audit first) · space toggle · ctrl+s create · esc cancel"},
	{"Task actions", "p pause · u resume · r retry · n run · x cancel · a reassign · P priority · c comment · d directive (!text urgent) · L live run · t transcript · o open PR · f flow"},
	{"Run", "t transcript · e errors only · f follow · j/k scroll · G newest · T task · esc back"},
	{"Transcript", "j/k entry · enter expand/collapse · E all · e errors only · l local/core source · f follow · T task · esc back"},
	{"Flow", "g lanes/pairs · a agent · t task · x clear · j/k G scroll"},
	{"Feed", "! attention · / filter · x clear · j/k G scroll"},
	{"Inbox", "↑↓ item · ←→ option · enter answer · a free text · v preview artifact · y approve · n reject · t task · r reply · m mark read (notifications)"},
	{"Chat", "j/k report · enter write · enter send · @name address · esc stop typing · pgup/pgdn or ctrl+u/ctrl+d scroll · t transcript of the reply"},
	{"Nodes", "h/l nodes/pauses · j/k move · p pause (the node, or a project, agent, quota key) · u resume · K kill switch"},
	{"Costs", "r range: 7 days, 30 days, today"},
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
