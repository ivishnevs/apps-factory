package storage

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"appfactory/internal/messaging"
)

// People languages (people-topics D15).
var PeopleLanguages = []string{"en", "ru"}

// PeopleLanguage is the language the company's people read briefs in.
func (s *Store) PeopleLanguage(ctx context.Context, company string) (string, error) {
	var lang string
	err := s.DB.QueryRowContext(ctx, "SELECT people_language FROM companies WHERE id=?", company).Scan(&lang)
	return lang, err
}

// SetPeopleLanguage sets it.
func (s *Store) SetPeopleLanguage(ctx context.Context, company, lang string) error {
	if lang != "en" && lang != "ru" {
		return inputError("people_language: en or ru")
	}
	res, err := s.DB.ExecContext(ctx, "UPDATE companies SET people_language=? WHERE id=?", lang, company)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return nil
}

func peopleLanguageTx(ctx context.Context, tx *Tx) string {
	var lang string
	if err := tx.QueryRowContext(ctx, "SELECT people_language FROM companies WHERE id=?", tx.company).Scan(&lang); err != nil || lang == "" {
		return "en"
	}
	return lang
}

// What happened, per escalation reason (people-topics D8).
var escalationSituation = map[string][2]string{
	"retries_exhausted":       {"stopped after several failed runs in a row", "остановилась после нескольких неудачных запусков подряд"},
	"budget_exhausted":        {"used up its run budget", "израсходовала бюджет запусков"},
	"no_exit":                 {"runs end without a result", "запуски заканчиваются без результата"},
	"change_missing":          {"names an OpenSpec change that does not exist", "ссылается на несуществующий OpenSpec-ченж"},
	"session_lost":            {"lost its session (the agent's earlier context)", "потеряла сессию (прежний контекст агента)"},
	"inspect":                 {"needs a look before it continues", "требует проверки, прежде чем продолжить"},
	"stalled":                 {"waits on dependencies that do not move", "ждёт зависимостей, которые не двигаются"},
	"pinned_node_unavailable": {"its machine (node) is unavailable", "её машина (нода) недоступна"},
	"workspace_diverged":      {"its workspace diverged from the repository", "её рабочая копия разошлась с репозиторием"},
	"wait_deadline":           {"passed the deadline of its wait", "пропустила дедлайн ожидания"},
	"wait_exhausted":          {"waited too many times", "ждала слишком много раз"},
	"event_deadline":          {"the event it waits for did not come by the deadline", "ожидаемое событие не наступило к дедлайну"},
	QuotaPausedReason:         {"hit the subscription limit", "упёрлась в лимит подписки"},
}

// What each escalation answer does.
var answerEffect = map[string][2]string{
	"retry":           {"runs it again now", "запустить заново сейчас"},
	"cancel":          {"closes the task", "закрыть задачу"},
	"reassign":        {"leaves it unassigned for you to assign", "снять исполнителя, чтобы вы назначили другого"},
	"accept":          {"accepts the last run's result as it is", "принять результат последнего запуска как есть"},
	"spec":            {"marks it as a spec-writing task and runs it again", "пометить задачей на написание спеки и запустить заново"},
	"new_session":     {"starts it again in a new session; the agent loses its earlier context", "запустить в новой сессии; агент потеряет прежний контекст"},
	"continue":        {"lets it continue as it is", "дать продолжить как есть"},
	"keep_waiting":    {"keeps waiting for its dependencies", "продолжать ждать зависимости"},
	"drop_dependency": {"drops the dependency it waits for and lets it run", "убрать зависимость и дать задаче запуститься"},
	"reset_workspace": {"resets its workspace to the default branch (unpushed work is saved)", "сбросить рабочую копию на основную ветку (незапушенная работа сохраняется)"},
	"extend":          {"gives the wait a new deadline", "дать ожиданию новый дедлайн"},
	"keep":            {"keeps the plan", "оставить план"},
	"wait":            {"waits until the limit resets", "подождать сброса лимита"},
	"resume":          {"lifts the pause now (after switching the account)", "снять паузу сейчас (после смены аккаунта)"},
}

// escalationBrief is Core's brief of its own escalation (people-topics D8).
func escalationBrief(lang, reason, agent, title, summary string, answers []string) *messaging.Brief {
	i := 0
	if lang == "ru" {
		i = 1
	}
	what := escalationSituation[reason][i]
	if what == "" {
		what = strings.ReplaceAll(reason, "_", " ")
	}
	subject := title
	if agent != "" {
		subject = agent + ": " + title
	}
	b := &messaging.Brief{Title: subject, Ask: [2]string{"How should it continue?", "Как продолжить?"}[i]}
	if r := []rune(b.Title); len(r) > messaging.MaxTitle {
		b.Title = string(r[:messaging.MaxTitle-1]) + "…"
	}
	var after []string
	for _, a := range answers {
		if e := answerEffect[a][i]; e != "" {
			after = append(after, a+": "+e)
		}
	}
	b.After = strings.Join(after, "\n")
	b.Context = fmt.Sprintf([2]string{"The task %s.\n\n%s", "Задача %s.\n\n%s"}[i], what, humanCause(summary))
	return b
}

var (
	idRe      = regexp.MustCompile(`\b[0-9a-f]{32}\b`)
	exactLine = regexp.MustCompile(`(?m)^(Exact artifact version|Exact call): .*$`)
	prefixRe  = regexp.MustCompile(`^(Artifact approval|Approval) [0-9a-f]{32}: `)
	escRe     = regexp.MustCompile(`^Escalation [a-z_]+ on (task|quota key) [^\n:]*?: `)
)

// humanCause is a summary without ids.
func humanCause(s string) string {
	s = idRe.ReplaceAllStringFunc(s, func(id string) string { return id[:8] })
	return strings.TrimSpace(s)
}

// PeopleText is a message body for people: without the exact JSON lines, approval prefixes and full ids (people-topics
// D7).
func PeopleText(body string) string {
	s := exactLine.ReplaceAllString(body, "")
	s = prefixRe.ReplaceAllString(s, "")
	s = escRe.ReplaceAllString(s, "")
	s = idRe.ReplaceAllStringFunc(s, func(id string) string { return id[:8] })
	s = regexp.MustCompile(`\n{3,}`).ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}
