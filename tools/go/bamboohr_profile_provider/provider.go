// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package main

import (
	"context"
	"fmt"
	"log/slog"
	"mime"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
)

type provider struct {
	client    *client
	lookupKey string
	cacheTTL  time.Duration
	logger    *slog.Logger

	cacheMu      sync.Mutex
	directory    *directorySnapshot
	directoryAt  time.Time
	inflightDone chan struct{}
	inflightErr  error
}

type directorySnapshot struct {
	byEmail map[string]rawDirectoryEmployee
	all     []rawDirectoryEmployee
}

func newProvider(cfg *bamboohrConfig, logger *slog.Logger) *provider {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &provider{
		client:    newClient(cfg, logger),
		lookupKey: cfg.LookupKey,
		cacheTTL:  cfg.CacheTTL,
		logger: logger.With(
			"logger_name", "bamboohr-provider",
			"subdomain", cfg.Subdomain,
		),
	}
}

func (p *provider) ensureDirectory(ctx context.Context) (*directorySnapshot, error) {
	p.cacheMu.Lock()
	if p.directory != nil && time.Since(p.directoryAt) < p.cacheTTL {
		snap := p.directory
		p.cacheMu.Unlock()
		return snap, nil
	}

	if p.inflightDone != nil {
		wait := p.inflightDone
		p.cacheMu.Unlock()
		<-wait
		p.cacheMu.Lock()
		if p.inflightErr != nil {
			err := p.inflightErr
			p.cacheMu.Unlock()
			return nil, err
		}
		snap := p.directory
		p.cacheMu.Unlock()
		return snap, nil
	}

	done := make(chan struct{})
	p.inflightDone = done
	p.cacheMu.Unlock()

	snap, err := p.refreshDirectory(ctx)

	p.cacheMu.Lock()
	p.inflightErr = err
	if err == nil {
		p.directory = snap
		p.directoryAt = time.Now()
	}
	close(done)
	p.inflightDone = nil
	p.cacheMu.Unlock()

	if err != nil {
		return nil, err
	}
	return snap, nil
}

func (p *provider) refreshDirectory(ctx context.Context) (*directorySnapshot, error) {
	employees, err := p.client.fetchEmployeeDirectory(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch directory: %w", err)
	}
	byEmail := make(map[string]rawDirectoryEmployee, len(employees))
	for _, emp := range employees {
		if email := strings.ToLower(emp.WorkEmail); email != "" {
			byEmail[email] = emp
		}
	}
	displayNameMap := make(map[string]string, len(employees))
	for _, emp := range employees {
		if emp.DisplayName != "" && emp.WorkEmail != "" {
			displayNameMap[emp.DisplayName] = strings.ToLower(emp.WorkEmail)
		}
	}
	for i := range employees {
		emp := &employees[i]
		switch {
		case emp.SupervisorEmail != "":
			emp.SupervisorEmail = strings.ToLower(emp.SupervisorEmail)
		case emp.Supervisor != "":
			if mail, ok := displayNameMap[emp.Supervisor]; ok {
				emp.SupervisorEmail = mail
			}
		}
		if email := strings.ToLower(emp.WorkEmail); email != "" {
			entry := byEmail[email]
			entry.SupervisorEmail = emp.SupervisorEmail
			byEmail[email] = entry
		}
	}
	return &directorySnapshot{byEmail: byEmail, all: employees}, nil
}

func toProfile(emp rawDirectoryEmployee) *Profile {
	if emp.WorkEmail == "" {
		return nil
	}
	workEmail := strings.ToLower(emp.WorkEmail)

	display := emp.DisplayName
	if emp.PreferredName != "" && emp.LastName != "" {
		display = emp.PreferredName + " " + emp.LastName
	} else if emp.PreferredName != "" {
		display = emp.PreferredName
	}

	var supervisorID string
	if emp.SupervisorEmail != "" {
		supervisorID = strings.ToLower(emp.SupervisorEmail)
	}

	return &Profile{
		ID:            workEmail,
		DisplayName:   display,
		PreferredName: emp.PreferredName,
		WorkEmail:     workEmail,
		JobTitle:      emp.JobTitle,
		Department:    emp.Department,
		Location:      emp.Location,
		SupervisorID:  supervisorID,
		HireDate:      emp.HireDate,
		MobilePhone:   emp.MobilePhone,
	}
}

func (p *provider) enrichByLookupValue(ctx context.Context, lookupValue string) (*Profile, error) {
	if strings.TrimSpace(lookupValue) == "" {
		return nil, nil
	}
	snap, err := p.ensureDirectory(ctx)
	if err != nil {
		return nil, err
	}
	emp, ok := snap.byEmail[strings.ToLower(lookupValue)]
	if !ok {
		p.logger.Debug("profile not found", "lookupValueFP", strutil.PIIFingerprint(lookupValue))
		return nil, nil
	}
	return toProfile(emp), nil
}

func (p *provider) searchUsers(ctx context.Context, query string, limit int) ([]*Profile, error) {
	if len(strings.TrimSpace(query)) < 2 {
		return []*Profile{}, nil
	}
	if limit <= 0 {
		limit = 10
	}
	snap, err := p.ensureDirectory(ctx)
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(query)
	results := make([]*Profile, 0, limit)
	for _, emp := range snap.all {
		if len(results) >= limit {
			break
		}
		if !matches(emp, q) {
			continue
		}
		if cp := toProfile(emp); cp != nil {
			results = append(results, cp)
		}
	}
	return results, nil
}

func matches(emp rawDirectoryEmployee, lowerQuery string) bool {
	if emp.DisplayName != "" && strings.Contains(strings.ToLower(emp.DisplayName), lowerQuery) {
		return true
	}
	if emp.WorkEmail != "" && strings.Contains(strings.ToLower(emp.WorkEmail), lowerQuery) {
		return true
	}
	if emp.PreferredName != "" && strings.Contains(strings.ToLower(emp.PreferredName), lowerQuery) {
		return true
	}
	return false
}

func (p *provider) getUserProfile(ctx context.Context, userID string) (map[string]any, error) {
	if userID == "" {
		return nil, nil
	}
	snap, err := p.ensureDirectory(ctx)
	if err != nil {
		return nil, err
	}
	emp, ok := snap.byEmail[strings.ToLower(userID)]
	if !ok {
		return nil, nil
	}
	return rawToMap(emp), nil
}

func (p *provider) getTimeOff(ctx context.Context, userID, startDate, endDate string) ([]TimeOffEntry, error) {
	if userID == "" {
		return nil, nil
	}
	bambooID, err := p.resolveBambooID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if bambooID == "" {
		return nil, nil
	}

	startDate, endDate = defaultTimeOffWindow(startDate, endDate)
	entries, err := p.client.fetchWhosOut(ctx, startDate, endDate)
	if err != nil {
		p.logger.Warn("time off fetch failed", "error", err, "userIDFP", strutil.PIIFingerprint(userID))
		return nil, nil
	}
	return entriesForEmployee(entries, bambooID), nil
}

func (p *provider) getTimeOffMulti(ctx context.Context, userIDs []string, startDate, endDate string) ([]UserTimeOff, error) {
	startDate, endDate = defaultTimeOffWindow(startDate, endDate)

	type target struct{ userID, bambooID string }
	targets := make([]target, 0, len(userIDs))
	anyResolved := false
	for _, userID := range userIDs {
		bambooID, err := p.resolveBambooID(ctx, userID)
		if err != nil {
			return nil, err
		}
		if bambooID != "" {
			anyResolved = true
		}
		targets = append(targets, target{userID: userID, bambooID: bambooID})
	}

	var entries []whosOutEntry
	if anyResolved {
		var err error
		if entries, err = p.client.fetchWhosOut(ctx, startDate, endDate); err != nil {
			p.logger.Warn("time off fetch failed", "error", err, "userCount", len(userIDs))
			entries = nil
		}
	}

	out := make([]UserTimeOff, 0, len(targets))
	for _, tgt := range targets {
		userEntries := []TimeOffEntry{}
		if tgt.bambooID != "" {
			userEntries = entriesForEmployee(entries, tgt.bambooID)
		}
		out = append(out, UserTimeOff{UserID: tgt.userID, Entries: userEntries})
	}
	return out, nil
}

func (p *provider) resolveBambooID(ctx context.Context, userID string) (string, error) {
	profile, err := p.getUserProfile(ctx, userID)
	if err != nil {
		return "", err
	}
	if profile == nil {
		p.logger.Debug("time off: employee not found", "userIDFP", strutil.PIIFingerprint(userID))
		return "", nil
	}
	bambooID, _ := profile["id"].(string)
	if bambooID == "" {
		p.logger.Warn("time off: profile has no bamboo id", "userIDFP", strutil.PIIFingerprint(userID))
	}
	return bambooID, nil
}

func defaultTimeOffWindow(startDate, endDate string) (string, string) {
	now := time.Now().UTC()
	if startDate == "" {
		startDate = now.Format("2006-01-02")
	}
	if endDate == "" {
		endDate = now.AddDate(0, 0, 30).Format("2006-01-02")
	}
	return startDate, endDate
}

func entriesForEmployee(entries []whosOutEntry, bambooID string) []TimeOffEntry {
	out := make([]TimeOffEntry, 0)
	for _, e := range entries {
		if !matchesEmployee(e.EmployeeID, bambooID) {
			continue
		}
		typ := e.Type
		if typ == "" {
			typ = "Time Off"
		}
		out = append(out, TimeOffEntry{
			Start:  e.Start,
			End:    e.End,
			Type:   typ,
			Amount: "full_day",
		})
	}
	return out
}

func (p *provider) getUserProfilePicture(ctx context.Context, userID string) ([]byte, string, error) {
	if userID == "" {
		return nil, "", nil
	}
	snap, err := p.ensureDirectory(ctx)
	if err != nil {
		return nil, "", err
	}
	emp, ok := snap.byEmail[strings.ToLower(userID)]
	if !ok || emp.PhotoURL == "" {
		return nil, "", nil
	}
	body, contentType, err := p.client.fetchPhoto(ctx, emp.PhotoURL)
	if err != nil {
		p.logger.Warn("photo fetch failed", "error", err, "userIDFP", strutil.PIIFingerprint(userID))
		return nil, "", nil
	}
	if contentType == "" {
		if guessed := mime.TypeByExtension(path.Ext(emp.PhotoURL)); guessed != "" {
			contentType = guessed
		} else {
			contentType = "image/jpeg"
		}
	}
	return body, contentType, nil
}

func rawToMap(emp rawDirectoryEmployee) map[string]any {
	m := map[string]any{
		"id":              emp.ID,
		"displayName":     emp.DisplayName,
		"firstName":       emp.FirstName,
		"lastName":        emp.LastName,
		"preferredName":   emp.PreferredName,
		"jobTitle":        emp.JobTitle,
		"workEmail":       strings.ToLower(emp.WorkEmail),
		"department":      emp.Department,
		"location":        emp.Location,
		"supervisor":      emp.Supervisor,
		"supervisorEmail": emp.SupervisorEmail,
		"hireDate":        emp.HireDate,
		"mobilePhone":     emp.MobilePhone,
		"photoUrl":        emp.PhotoURL,
	}
	for k, v := range m {
		if s, ok := v.(string); ok && s == "" {
			delete(m, k)
		}
	}
	return m
}
