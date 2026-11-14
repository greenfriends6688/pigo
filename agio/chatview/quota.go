package chatview

import (
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/ui/chat"
	agio "pigo/agio"
	"pigo/agio/agent"
	"pigo/agio/chatview/locales"
	"pigo/agio/quota"
)

// quotaRows are an account's limits as rows: a bar of what is left for
// each window and an amount for each balance.
func quotaRows(msgs locales.Messages, q *agio.Quota, now time.Time) []chat.QuotaRow {
	if q.Usage == nil {
		return nil
	}
	var rows []chat.QuotaRow
	for _, w := range q.Usage.Windows {
		left := min(max(100-w.Percent, 0), 100)
		row := chat.QuotaRow{Label: quotaWindowLabel(msgs, w), Value: msgs.QuotaLeft(fmt.Sprintf("%.0f", left)), Fraction: left / 100}
		if !w.ResetsAt.IsZero() {
			row.Detail = quotaReset(msgs, w.ResetsAt, now)
		}
		rows = append(rows, row)
	}
	for _, b := range q.Usage.Balances {
		rows = append(rows, chat.QuotaRow{Label: msgs.QuotaBalance(), Value: formatBalance(b), Fraction: -1})
	}
	return rows
}

// quotaPanel is an account's limits with the time to its next read, a
// refresh and a link to its usage page.
func quotaPanel(m *Model, msgs locales.Messages, p quota.Provider) chat.Quota {
	find := func() *agio.Quota {
		for _, q := range m.State().Quotas {
			if q.Provider == p {
				return &q
			}
		}
		return &agio.Quota{Provider: p}
	}
	q := find()
	o := chat.Quota{ID: string(p), Title: p.Name(), Leading: leading(quotaIcon(p)), Note: quotaNote(msgs, q),
		RefreshLabel: msgs.QuotaRefresh(p.Name()),
		Loading:      live(m, func() bool { q := find(); return q.Usage == nil && q.Err == "" }),
		Refreshing:   live(m, func() bool { return find().Checking }),
		Rows:         live(m, func() []chat.QuotaRow { return quotaRows(msgs, find(), m.now()) }),
		Countdown:    live(m, func() string { return quotaCountdown(msgs, find(), m.now()) }),
		OnRefresh:    func() { m.Do(func(b agio.Backend) error { b.RefreshQuota(p); return nil }, nil) }}
	if q.Usage != nil && q.Usage.Plan != "" {
		o.Title += " · " + q.Usage.Plan
	}
	if page := p.UsagePage(); page != "" {
		o.OpenLabel = msgs.QuotaOpenPage(p.Name())
		o.OnOpen = func() { m.cx.OpenURL(page, nil) }
	}
	return o
}

// quotaNote explains a failed read. Earlier limits stay meaningful while
// the provider rate-limits reads, so that says nothing.
func quotaNote(msgs locales.Messages, q *agio.Quota) string {
	switch {
	case q.Err == "", q.Usage != nil && q.Status == http.StatusTooManyRequests:
		return ""
	case q.Expired:
		return msgs.QuotaExpired()
	case q.Status == http.StatusTooManyRequests:
		return msgs.QuotaRateLimited()
	case q.Status == http.StatusUnauthorized || q.Status == http.StatusForbidden:
		if q.Message != "" {
			return q.Message
		}
		return msgs.QuotaSignin()
	}
	return msgs.QuotaFailed(q.Err)
}

func quotaWindowLabel(msgs locales.Messages, w quota.Window) string {
	var label string
	switch {
	case w.Minutes <= 0:
		label = msgs.QuotaLimit()
	case w.Minutes == 7*24*60:
		label = msgs.QuotaWeekly()
	case w.Minutes < 24*60:
		label = msgs.QuotaHours((w.Minutes + 59) / 60)
	default:
		label = msgs.QuotaDays((w.Minutes + 24*60 - 1) / (24 * 60))
	}
	if w.Scope != "" {
		label += " · " + w.Scope
	}
	return label
}

// quotaReset uses the two largest whole units remaining, dropping a zero
// second unit, with seconds rounded up.
func quotaReset(msgs locales.Messages, at, now time.Time) string {
	d := at.Sub(now)
	switch {
	case d <= 0:
		return msgs.QuotaResetsSoon()
	case d < time.Minute:
		return msgs.QuotaResetsSeconds(int((d + time.Second - 1) / time.Second))
	case d < time.Hour:
		return msgs.QuotaResetsMinutes(int(d / time.Minute))
	case d < 24*time.Hour:
		hours, minutes := int(d/time.Hour), int(d%time.Hour/time.Minute)
		if minutes == 0 {
			return msgs.QuotaResetsHours(hours)
		}
		return msgs.QuotaResetsHoursMinutes(hours, minutes)
	}
	days, hours := int(d/(24*time.Hour)), int(d%(24*time.Hour)/time.Hour)
	if hours == 0 {
		return msgs.QuotaResetsDays(days)
	}
	return msgs.QuotaResetsDaysHours(days, hours)
}

// quotaCountdown is the time to the next read as MM:SS, with hours for
// longer backoffs, rounded up so it never shows zero early.
func quotaCountdown(msgs locales.Messages, q *agio.Quota, now time.Time) string {
	if q.Checking || q.Next.IsZero() {
		return msgs.QuotaRefreshing()
	}
	d := max(q.Next.Sub(now), 0)
	seconds := int64((d + time.Second - 1) / time.Second)
	if seconds >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", seconds/3600, seconds/60%60, seconds%60)
	}
	return fmt.Sprintf("%02d:%02d", seconds/60, seconds%60)
}

func formatBalance(b quota.Balance) string {
	switch b.Currency {
	case "CNY":
		return "¥" + b.Amount
	case "USD":
		return "$" + b.Amount
	}
	return b.Amount + " " + b.Currency
}

// quotaSummary is the one limit the composer shows for a model, and its
// tooltip: the shortest window that applies to it, unless one is
// exhausted, then the one that blocks longest, else a balance. It uses the
// latest limits read, never assuming a window refilled once its reset
// passed.
func quotaSummary(msgs locales.Messages, s agio.State, key string, now time.Time) (chat.QuotaRow, string, bool) {
	q := s.Quota(key)
	if q == nil || q.Usage == nil {
		return chat.QuotaRow{}, "", false
	}
	rows := quotaRows(msgs, q, now)
	info, _ := s.Model(key)
	u := q.Usage
	index := -1
	for i, w := range u.Windows {
		if quotaWindowApplies(w, info) && (index < 0 || quotaWindowBefore(w, u.Windows[index])) {
			index = i
		}
	}
	if index < 0 {
		if len(u.Balances) == 0 {
			return chat.QuotaRow{}, "", false
		}
		index = len(u.Windows)
	}
	row := rows[index]
	title := q.Provider.Name()
	if u.Plan != "" {
		title += " · " + u.Plan
	}
	parts := []string{title, row.Label + " · " + row.Value}
	if row.Detail != "" {
		parts = append(parts, row.Detail)
	}
	if note := quotaNote(msgs, q); note != "" {
		parts = append(parts, note)
	}
	return row, strings.Join(parts, "\n"), true
}

// quotaWindowApplies reports whether a window limits a model: a scoped one
// must name it, as whole words across punctuation and case.
func quotaWindowApplies(w quota.Window, m agent.Model) bool {
	if w.Scope == "" {
		return true
	}
	normalize := func(s string) string {
		return strings.Join(strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		}), " ")
	}
	scope := normalize(w.Scope)
	if scope == "" {
		return false
	}
	for _, name := range []string{m.ID, m.Name} {
		if strings.Contains(" "+normalize(name)+" ", " "+scope+" ") {
			return true
		}
	}
	return false
}

func quotaWindowBefore(a, b quota.Window) bool {
	if aOut, bOut := a.Percent >= 100, b.Percent >= 100; aOut != bOut {
		return aOut
	} else if aOut {
		return quotaResetsLater(a, b)
	}
	if a.Minutes != b.Minutes {
		return a.Minutes > 0 && (b.Minutes <= 0 || a.Minutes < b.Minutes)
	}
	return quotaResetsLater(b, a)
}

// quotaResetsLater orders unknown reset times last.
func quotaResetsLater(a, b quota.Window) bool {
	if a.ResetsAt.IsZero() || b.ResetsAt.IsZero() {
		return a.ResetsAt.IsZero() && !b.ResetsAt.IsZero()
	}
	return a.ResetsAt.After(b.ResetsAt)
}

// quotaFooter shows, at the composer's end, the selected model's limit,
// opening its account's limits. A balance has no bar, so it shows its
// amount.
func (v Conversation) quotaFooter(cx *core.Scope) core.Node {
	m := v.Model
	msgs := locales.For(cx)
	model := func() string { return m.Conversation(v.ID).Model }
	// The panel's title and note are fixed, so they rebuild it.
	key := func() string {
		q := m.State().Quota(model())
		if _, _, ok := quotaSummary(msgs, m.State(), model(), time.Time{}); !ok || q == nil {
			return ""
		}
		return fmt.Sprint(q.Provider, "\x00", q.Usage.Plan, "\x00", quotaNote(msgs, q))
	}
	return region(m, key, func(cx *core.Scope) core.Node {
		q := m.State().Quota(model())
		if key() == "" {
			return core.Group(nil)
		}
		p := q.Provider
		return chat.QuotaSummary{Label: msgs.QuotaDetails(), Quotas: []chat.Quota{quotaPanel(m, msgs, p)}, TestID: "quota",
			Row:     live(m, func() chat.QuotaRow { r, _, _ := quotaSummary(msgs, m.State(), model(), m.now()); return r }),
			Tooltip: live(m, func() string { _, t, _ := quotaSummary(msgs, m.State(), model(), m.now()); return t })}
	})
}
