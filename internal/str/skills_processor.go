// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/skillruntime"
)

const (
	skillStagingDir = ".extract-staging"
	stagedNewDir    = "new"
	stagedOldDir    = "old"

	swapRetryBase        = 2 * time.Second
	swapRetryMax         = 5 * time.Minute
	swapRetryMaxAttempts = 8
)

var (
	errSkillDirNotUpdated = errors.New("not updated")
	errSkillDirDamaged    = errors.New("left partially updated")
)

func skillDirNotUpdated(err error) error {
	return fmt.Errorf("%w: %w", errSkillDirNotUpdated, err)
}

type swapRetry struct {
	archiveModTime time.Time
	archiveSize    int64
	attempts       int
	notBefore      time.Time
}

type SkillProcessor struct {
	skillsDir string
	logger    *slog.Logger
	now       func() time.Time
	swap      func(skillDir, incoming, replaced string) error

	mu      sync.Mutex
	retries map[string]swapRetry
}

func NewSkillProcessor(skillsDir string, logger *slog.Logger) *SkillProcessor {
	return &SkillProcessor{
		skillsDir: skillsDir,
		logger:    logger.With("logger_name", "skill-processor"),
		now:       time.Now,
		swap:      replaceSkillContents,
		retries:   make(map[string]swapRetry),
	}
}

func (p *SkillProcessor) waitingToRetry(skill string, archive os.FileInfo) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	r, ok := p.retries[skill]
	return ok && r.archiveModTime.Equal(archive.ModTime()) && r.archiveSize == archive.Size() &&
		p.now().Before(r.notBefore)
}

func (p *SkillProcessor) recordSwapFailure(skill string, archive os.FileInfo) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.retries[skill]
	if !r.archiveModTime.Equal(archive.ModTime()) || r.archiveSize != archive.Size() {
		r = swapRetry{archiveModTime: archive.ModTime(), archiveSize: archive.Size()}
	}
	r.notBefore = p.now().Add(min(swapRetryBase<<r.attempts, swapRetryMax))
	r.attempts = min(r.attempts+1, swapRetryMaxAttempts)
	p.retries[skill] = r
}

func (p *SkillProcessor) clearSwapFailure(skill string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.retries, skill)
}

func (p *SkillProcessor) ProcessAll() (bool, error) {
	if p.skillsDir == "" {
		return false, nil
	}

	entries, err := os.ReadDir(p.skillsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}

	changed := false
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillDir := filepath.Join(p.skillsDir, entry.Name())
		zipPath := filepath.Join(skillDir, "skill.zip")

		info, statErr := os.Stat(zipPath)
		if statErr != nil || !info.Mode().IsRegular() {
			continue
		}

		marker := zipPath + ".extract-error"
		if markerInfo, markerErr := os.Stat(marker); markerErr == nil &&
			!info.ModTime().After(markerInfo.ModTime()) {
			continue
		}
		if p.waitingToRetry(entry.Name(), info) {
			continue
		}

		p.logger.Debug("extracting skill ZIP", "skill", entry.Name(), "path", zipPath)
		if p.unpackSkill(skillDir, entry.Name(), zipPath, marker) {
			changed = true
		}
	}

	return changed, nil
}

func (p *SkillProcessor) unpackSkill(skillDir, skill, zipPath, marker string) bool {
	record := filepath.Join(skillDir, skillruntime.BundleHashFile)
	opened, digest, err := p.unpackArchiveAt(zipPath, skillDir)
	if errors.Is(err, errSkillDirNotUpdated) {
		p.recordSwapFailure(skill, opened)
		if errors.Is(err, errSkillDirDamaged) {
			p.logger.Error("skill directory left partially updated; retrying later", "skill", skill, "error", err)
		} else {
			p.logger.Warn("skill directory could not be updated; retrying later", "skill", skill, "error", err)
		}
		return false
	}
	if err != nil {
		p.failUnpack(skill, marker, err)
		return false
	}
	_ = os.Remove(marker)
	if !p.keepUnpacked(zipPath, opened, skill) {
		return true
	}
	if err := skillruntime.RecordBundleHash(skillDir, digest); err != nil {
		_ = os.Remove(record)
		p.logger.Warn("failed to record skill ZIP digest", "skill", skill, "error", err)
	}
	p.logger.Debug("skill ZIP extracted", "skill", skill)
	return true
}

func (p *SkillProcessor) failUnpack(skill, marker string, err error) {
	p.logger.Warn("skill ZIP extraction failed", "skill", skill, "error", err)
	_ = os.Remove(filepath.Join(filepath.Dir(marker), skillruntime.BundleHashFile))
	_ = os.WriteFile(marker, []byte(err.Error()), 0o644)
}

func (p *SkillProcessor) keepUnpacked(zipPath string, opened os.FileInfo, skill string) bool {
	if current, err := os.Stat(zipPath); err != nil || !os.SameFile(opened, current) {
		p.clearSwapFailure(skill)
		p.logger.Debug("skill ZIP replaced while extracting; extracting the new one next", "skill", skill)
		return false
	}
	kept := zipPath + ".extracted"
	if err := os.Rename(zipPath, kept); err != nil {
		p.recordSwapFailure(skill, opened)
		p.logger.Warn("failed to rename extracted skill ZIP; unpacking it again later", "path", zipPath, "error", err)
		return true
	}
	p.clearSwapFailure(skill)
	if renamed, err := os.Stat(kept); err != nil || !os.SameFile(opened, renamed) {
		if _, statErr := os.Stat(zipPath); os.IsNotExist(statErr) {
			_ = os.Rename(kept, zipPath)
		}
		p.logger.Debug("skill ZIP replaced while extracting; extracting the new one next", "skill", skill)
		return false
	}
	return true
}

func (p *SkillProcessor) unpackArchiveAt(zipPath, skillDir string) (os.FileInfo, string, error) {
	f, err := os.Open(zipPath)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil {
		return nil, "", err
	}
	digest, err := p.unpackArchive(f, opened.Size(), skillDir)
	return opened, digest, err
}

func (p *SkillProcessor) unpackArchive(f *os.File, size int64, skillDir string) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, io.NewSectionReader(f, 0, size)); err != nil {
		return "", fmt.Errorf("digest: %w", err)
	}
	zr, err := zip.NewReader(f, size)
	if err != nil {
		return "", err
	}
	staging := filepath.Join(skillDir, skillStagingDir)
	incoming := filepath.Join(staging, stagedNewDir)
	replaced := filepath.Join(staging, stagedOldDir)
	if err := os.RemoveAll(staging); err != nil {
		return "", skillDirNotUpdated(err)
	}
	defer p.removeStaging(staging)
	if err := errors.Join(os.MkdirAll(incoming, 0o755), os.MkdirAll(replaced, 0o755)); err != nil {
		return "", skillDirNotUpdated(err)
	}
	if err := extractZip(zr, incoming, isRuntimeOwnedName); err != nil {
		return "", err
	}
	if err := p.swap(skillDir, incoming, replaced); err != nil {
		if errors.Is(err, errSkillDirDamaged) {
			_ = os.Remove(filepath.Join(skillDir, skillruntime.BundleHashFile))
		}
		return "", skillDirNotUpdated(err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (p *SkillProcessor) removeStaging(staging string) {
	if err := os.RemoveAll(staging); err != nil {
		p.logger.Warn("failed to remove skill staging directory", "dir", staging, "error", err)
	}
}

type skillSwap struct {
	skillDir, incoming, replaced string
	undo                         []func() error
}

func replaceSkillContents(skillDir, incoming, replaced string) (err error) {
	s := &skillSwap{skillDir: skillDir, incoming: incoming, replaced: replaced}
	defer func() {
		if err == nil {
			return
		}
		if undoErr := s.rollback(); undoErr != nil {
			err = fmt.Errorf("%w: %w; undo: %w", errSkillDirDamaged, err, undoErr)
		}
	}()

	entries, err := os.ReadDir(incoming)
	if err != nil {
		return err
	}
	existing, err := os.ReadDir(skillDir)
	if err != nil {
		return err
	}
	incomingNames := make(map[string]bool, len(entries))
	for _, e := range entries {
		incomingNames[e.Name()] = !isRuntimeOwnedName(e.Name())
	}
	existingByName := make(map[string]os.DirEntry, len(existing))
	for _, e := range existing {
		existingByName[e.Name()] = e
		if incomingNames[e.Name()] || (isRuntimeOwnedName(e.Name()) && e.Name() != skillruntime.BundleHashFile) {
			continue
		}
		if err := s.moveAside(e.Name()); err != nil {
			return err
		}
	}

	for _, e := range entries {
		if !incomingNames[e.Name()] {
			continue
		}
		if old, ok := existingByName[e.Name()]; ok {
			if err := s.setAside(old, e); err != nil {
				return err
			}
		}
		if err := s.place(e.Name()); err != nil {
			return err
		}
	}
	return nil
}

func (s *skillSwap) live(name string) string  { return filepath.Join(s.skillDir, name) }
func (s *skillSwap) aside(name string) string { return filepath.Join(s.replaced, name) }

func (s *skillSwap) moveAside(name string) error {
	if err := os.Rename(s.live(name), s.aside(name)); err != nil {
		return err
	}
	s.undo = append(s.undo, func() error { return os.Rename(s.aside(name), s.live(name)) })
	return nil
}

func (s *skillSwap) setAside(old, replacement os.DirEntry) error {
	if old.Type().IsRegular() && replacement.Type().IsRegular() &&
		os.Link(s.live(old.Name()), s.aside(old.Name())) == nil {
		s.undo = append(s.undo, func() error { return os.Rename(s.aside(old.Name()), s.live(old.Name())) })
		return nil
	}
	return s.moveAside(old.Name())
}

func (s *skillSwap) place(name string) error {
	if err := os.Rename(filepath.Join(s.incoming, name), s.live(name)); err != nil {
		return err
	}
	s.undo = append(s.undo, func() error { return os.RemoveAll(s.live(name)) })
	return nil
}

func (s *skillSwap) rollback() error {
	var errs []error
	for i := len(s.undo) - 1; i >= 0; i-- {
		errs = append(errs, s.undo[i]())
	}
	return errors.Join(errs...)
}

func isRuntimeOwnedName(name string) bool {
	top, _, _ := strings.Cut(path.Clean(name), "/")
	top = strings.ToLower(top)
	return top == skillStagingDir || strings.HasPrefix(strings.TrimPrefix(top, "."), "skill.zip")
}
