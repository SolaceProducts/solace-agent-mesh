// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package budgetstate

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"math/big"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type Recipient struct {
	PlatformUserID   string
	LoginID          string
	CanReviewBudgets bool
}

type AdminDirectory interface {
	BudgetAdmins(ctx context.Context) ([]Recipient, error)
}

type Alerting struct {
	Admins        AdminDirectory
	NotifyEnabled func() bool
	Metrics       runtime.Metrics
}

const adminResolveTimeout = 30 * time.Second

const alertPassDeadline = 5 * time.Second

const msgBudgetLevelCrossed = "budget level crossed"

const msgMeshLevelFell = "mesh budget level fell"

const metricCrossings = "budget.alert_crossings"

const budgetsLinkPath = "/budgets"

const (
	alertSubjectMesh       = "mesh"
	alertSubjectUser       = "user"
	alertSubjectSystemUser = "system_user"
)

var alertCategories = map[string]map[budget.Level]string{
	alertSubjectMesh:       {budget.LevelWarn: a2a.CategoryBudgetMeshWarn, budget.LevelLimit: a2a.CategoryBudgetMeshLimit},
	alertSubjectUser:       {budget.LevelWarn: a2a.CategoryBudgetSelfWarn, budget.LevelLimit: a2a.CategoryBudgetSelfLimit},
	alertSubjectSystemUser: {budget.LevelWarn: a2a.CategoryBudgetSystemUserWarn, budget.LevelLimit: a2a.CategoryBudgetSystemUserLimit},
}

func (m *monitor) alert(ctx context.Context, now time.Time, a assessment) {
	deadline := m.clock.Now().Add(alertPassDeadline)
	m.pruneAlertStates(ctx, a.periodStart)
	rows, err := m.store.AlertStates(ctx, a.periodStart)
	if err != nil {
		if ctx.Err() == nil {
			m.logger.Warn("budget alert states could not be read; crossings are alerted on a later recompute", "periodStart", a.periodStart, "error", err)
		}
		return
	}
	stored := make(map[alertSubject]store.BudgetAlertState, len(rows))
	for _, row := range rows {
		stored[alertSubject{row.SubjectType, row.SubjectRef}] = row
	}
	admins := &adminRecipients{directory: m.alerting.Admins}
	standings := alertStandings(a.standings)
	for i, st := range standings {
		if ctx.Err() != nil {
			return
		}
		if !m.clock.Now().Before(deadline) {
			m.logDeadlineReached(len(standings) - i)
			return
		}
		subject := alertSubject{st.subjectType, st.alertRef()}
		row, ok := stored[subject]
		delete(stored, subject)
		m.alertEdge(ctx, now, a, st, row, ok, admins)
	}
	m.deadlineReached = false
	for _, row := range stored {
		if ctx.Err() != nil {
			return
		}
		if budget.Level(row.AlertedLevel) != budget.LevelNone {
			m.recordAlertState(ctx, now, a.periodStart, standing{subjectType: row.SubjectType, subjectRef: row.SubjectRef, level: budget.LevelNone, capKey: row.CapKey})
		}
	}
}

func (m *monitor) logDeadlineReached(remaining int) {
	const msg = "budget alert pass reached its deadline; the remaining subjects are alerted on a later recompute"
	if m.deadlineReached {
		m.logger.Debug(msg, "remaining", remaining)
		return
	}
	m.deadlineReached = true
	m.logger.Info(msg, "remaining", remaining)
}

type alertSubject struct{ subjectType, subjectRef string }

func alertStandings(standings []standing) []standing {
	out := make([]standing, 0, len(standings))
	index := make(map[alertSubject]int, len(standings))
	for _, st := range standings {
		subject := alertSubject{st.subjectType, st.alertRef()}
		i, seen := index[subject]
		switch {
		case !seen:
			index[subject] = len(out)
			out = append(out, st)
		case st.level.Above(out[i].level):
			out[i] = st
		}
	}
	return out
}

func (m *monitor) alertEdge(ctx context.Context, now time.Time, a assessment, st standing, row store.BudgetAlertState, stored bool, admins *adminRecipients) {
	alerted := budget.LevelNone
	if stored && row.CapKey == st.capKey {
		alerted = budget.Level(row.AlertedLevel)
	}
	switch {
	case st.level.Above(alerted):
		subject, err := m.raise(ctx, a, st, admins)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			m.logger.Warn("budget alert could not be sent; it is retried on a later recompute", "budgetLevel", st.level, "subjectType", st.subjectType, "error", err)
			return
		}
		if m.recordAlertState(ctx, now, a.periodStart, st) {
			m.logCrossing(a, st, subject)
		}
	case alerted.Above(st.level), stored && row.CapKey != st.capKey:
		fell := stored && budget.Level(row.AlertedLevel).Above(st.level)
		if m.recordAlertState(ctx, now, a.periodStart, st) && fell && st.subjectType == store.BudgetSubjectMesh {
			m.logger.Info(msgMeshLevelFell, "budgetLevel", st.level, "periodEnd", a.state.PeriodEnd)
		}
	}
}

func (m *monitor) recordAlertState(ctx context.Context, now time.Time, periodStart int64, st standing) bool {
	err := m.store.SetAlertState(ctx, store.BudgetAlertState{
		SubjectType:  st.subjectType,
		SubjectRef:   st.alertRef(),
		PeriodStart:  periodStart,
		AlertedLevel: string(st.level),
		CapKey:       st.capKey,
		UpdatedTime:  now.UnixMilli(),
	})
	if err != nil {
		if ctx.Err() == nil {
			m.logger.Warn("budget alert state could not be recorded; a later recompute may alert again", "subjectType", st.subjectType, "error", err)
		}
		return false
	}
	return true
}

func (m *monitor) logCrossing(a assessment, st standing, subject string) {
	runtime.MetricsOrNoop(m.alerting.Metrics).Counter(metricCrossings, 1, "subject", subject, "level", string(st.level))
	attrs := []any{"subject", subject, "budgetLevel", st.level, "percent", percentOf(st), "periodEnd", a.state.PeriodEnd}
	if id, _ := st.userIDOrName(); id != "" {
		attrs = append(attrs, "userId", id)
	}
	if subject == alertSubjectUser {
		m.logger.Debug(msgBudgetLevelCrossed, attrs...)
		return
	}
	m.logger.Info(msgBudgetLevelCrossed, attrs...)
}

func (m *monitor) pruneAlertStates(ctx context.Context, periodStart int64) {
	if m.prunedBefore == periodStart {
		return
	}
	deleted, err := m.store.DeleteAlertStatesBefore(ctx, periodStart)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		m.logger.Warn("budget alert states of past periods could not be deleted", "error", err)
		return
	}
	m.prunedBefore = periodStart
	if deleted > 0 {
		m.logger.Debug("deleted budget alert states of past periods", "deleted", deleted)
	}
}

func (m *monitor) raise(ctx context.Context, a assessment, st standing, admins *adminRecipients) (string, error) {
	msg, err := m.alertFor(ctx, a, st, admins)
	if err != nil {
		return msg.subject, err
	}
	if len(msg.recipients) == 0 {
		if msg.toAdmins {
			m.logger.Warn("budget alert has no administrator to go to; the crossing is recorded", "subject", msg.subject, "budgetLevel", st.level)
		}
		return msg.subject, nil
	}
	if m.alerting.NotifyEnabled == nil || !m.alerting.NotifyEnabled() {
		return msg.subject, nil
	}
	return msg.subject, m.publishAlert(ctx, a, st, msg)
}

type alertMessage struct {
	subject    string
	category   string
	toAdmins   bool
	recipients []Recipient
	title      string
	body       string
	budgetID   string
}

func (m *monitor) alertFor(ctx context.Context, a assessment, st standing, admins *adminRecipients) (alertMessage, error) {
	if st.subjectType == store.BudgetSubjectMesh {
		msg := adminAlert(alertSubjectMesh, "The Agent Mesh", "Agent Mesh budget", a, st)
		var err error
		msg.recipients, err = admins.get(ctx)
		return msg, err
	}
	id, sub := st.userIDOrName()
	var user *store.User
	if id != "" {
		var err error
		if user, err = m.store.User(ctx, id); err != nil {
			return alertMessage{subject: alertSubjectUser}, err
		}
	}
	if systemName, ok := systemUserName(user, sub); ok {
		msg := adminAlert(alertSubjectSystemUser, "Service account "+systemName, "Budget for service account "+systemName, a, st)
		var err error
		msg.recipients, err = admins.get(ctx)
		return msg, err
	}
	msg := userAlert(a, st)
	switch {
	case user != nil && user.Active:
		msg.recipients = []Recipient{{PlatformUserID: user.ID, LoginID: cmp.Or(derefString(user.Email), user.Sub)}}
	case id == "" && sub != "":
		msg.recipients = []Recipient{{LoginID: sub}}
	}
	return msg, nil
}

func (st standing) userIDOrName() (id, sub string) {
	switch subject := st.alertedAs(); subject.Kind {
	case store.BudgetPeriodUserID:
		return subject.Ref, ""
	case store.BudgetPeriodUserSub:
		return "", subject.Ref
	default:
		return "", ""
	}
}

func (st standing) alertedAs() store.BudgetPeriodSubject {
	if st.systemUserID != "" {
		return byID(st.systemUserID)
	}
	return st.subject
}

func (st standing) alertRef() string {
	if st.systemUserID != "" {
		return byID(st.systemUserID).UserKey()
	}
	return st.subjectRef
}

func systemUserName(user *store.User, sub string) (string, bool) {
	switch {
	case user != nil:
		return cmp.Or(derefString(user.DisplayName), user.Sub), user.ProvisionedVia == rbac.ProvisionedViaSystem
	case sub != "":
		return sub, rbac.IsSystemPrincipal(sub)
	default:
		return "", false
	}
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func adminAlert(subject, who, what string, a assessment, st standing) alertMessage {
	pct := percentOf(st)
	spent, budgetText := dollars(st.spent), budgetDescription(st)
	resets := resetDate(a.state.PeriodEnd)
	msg := alertMessage{subject: subject, category: alertCategories[subject][st.level], toAdmins: true, budgetID: st.budget.ID}
	if st.level == budget.LevelLimit {
		msg.title = what + " reached"
		msg.body = fmt.Sprintf("%s has spent %s of its %s this period (%d%%), so its new requests are refused until the budget resets on %s or is raised.",
			who, spent, budgetText, pct, resets)
		return msg
	}
	msg.title = fmt.Sprintf("%s at %d%%", what, pct)
	msg.body = fmt.Sprintf("%s has spent %s of its %s this period (%d%%). The budget resets on %s.", who, spent, budgetText, pct, resets)
	return msg
}

func userAlert(a assessment, st standing) alertMessage {
	pct := percentOf(st)
	resets := resetDate(a.state.PeriodEnd)
	msg := alertMessage{subject: alertSubjectUser, category: alertCategories[alertSubjectUser][st.level], budgetID: st.budget.ID}
	if st.level == budget.LevelLimit {
		msg.title = "You've reached your budget"
		msg.body = fmt.Sprintf("You've used %d%% of your LLM budget for this period, so your new requests are refused until it resets on %s.", pct, resets)
		return msg
	}
	msg.title = fmt.Sprintf("You've used %d%% of your budget", pct)
	msg.body = fmt.Sprintf("You've used %d%% of your LLM budget for this period. It resets on %s.", pct, resets)
	return msg
}

func budgetDescription(st standing) string {
	limit := budget.EffectiveCap(st.budget.Amount, st.topUp)
	if limit == nil {
		return "unlimited budget"
	}
	if st.topUp == 0 {
		return dollars(*limit) + " budget"
	}
	return fmt.Sprintf("%s budget (%s plus a %s top-up)", dollars(*limit), dollars(*st.budget.Amount), dollars(st.topUp))
}

func dollars(amount int64) string {
	return "$" + budget.FormatAmount(amount)
}

func percentOf(st standing) int {
	limit := budget.EffectiveCap(st.budget.Amount, st.topUp)
	switch {
	case limit == nil:
		return 0
	case *limit <= 0:
		return 100
	case st.spent <= 0:
		return 0
	}
	pct := new(big.Int).Mul(big.NewInt(st.spent), big.NewInt(100))
	pct.Quo(pct, big.NewInt(*limit))
	if !pct.IsInt64() || pct.Int64() > math.MaxInt {
		return math.MaxInt
	}
	return int(pct.Int64())
}

func resetDate(periodEnd time.Time) string {
	return periodEnd.UTC().Format("2 January 2006") + " (UTC)"
}

func (m *monitor) publishAlert(ctx context.Context, a assessment, st standing, msg alertMessage) error {
	dedup := fmt.Sprintf("budget:%s:%s:%d:%s:%s", st.subjectType, st.alertRef(), a.periodStart, st.level, st.capKey)
	base := map[string]any{
		"budget_id":  msg.budgetID,
		"subject":    msg.subject,
		"kind":       string(st.level),
		"percent":    percentOf(st),
		"period_end": a.state.PeriodEnd.UTC().Format("2006-01-02T15:04:05.000Z"),
	}
	for _, r := range msg.recipients {
		metadata := base
		if msg.toAdmins && r.CanReviewBudgets {
			metadata = maps.Clone(base)
			metadata["link_path"] = budgetsLinkPath
			metadata["link_label"] = "Review budget"
		}
		req := a2a.NotifyRequest{
			RequestID:               idgen.New(),
			RecipientUserID:         r.LoginID,
			RecipientPlatformUserID: r.PlatformUserID,
			Mode:                    a2a.NotifyModeNotify,
			SenderAgent:             "budget",
			Category:                msg.category,
			Title:                   msg.title,
			Message:                 msg.body,
			Dedup:                   dedup,
			Metadata:                metadata,
		}
		payload, err := json.Marshal(req)
		if err != nil {
			return fmt.Errorf("marshal budget alert: %w", err)
		}
		topic := a2a.NotifyRequestTopic(m.namespace, cmp.Or(r.PlatformUserID, r.LoginID))
		if err := m.broker.PublishGuaranteed(ctx, topic, &broker.Message{Topic: topic, Payload: payload}); err != nil {
			return fmt.Errorf("publish budget alert: %w", err)
		}
	}
	return nil
}

type adminRecipients struct {
	directory AdminDirectory
	resolved  bool
	admins    []Recipient
	err       error
}

func (r *adminRecipients) get(ctx context.Context) ([]Recipient, error) {
	if r.directory == nil {
		return nil, nil
	}
	if !r.resolved {
		resolveCtx, cancel := context.WithTimeout(ctx, adminResolveTimeout)
		defer cancel()
		r.admins, r.err = r.directory.BudgetAdmins(resolveCtx)
		if r.err != nil {
			r.admins, r.err = nil, fmt.Errorf("resolve budget administrators: %w", r.err)
		}
		r.resolved = true
	}
	return r.admins, r.err
}
