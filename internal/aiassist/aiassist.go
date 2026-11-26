// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package aiassist

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/skill"
)

const conciergeEntryPoint = "sam-concierge"

const samDocsName = "sam-docs"

const docsCorpusDir = "references/documentation"

//go:embed skills
var fsys embed.FS

func ConciergeFS() fs.FS {
	sub, err := fs.Sub(fsys, "skills")
	if err != nil {
		panic(fmt.Errorf("aiassist: fs.Sub: %w", err))
	}
	return sub
}

var ConciergeSkills = sync.OnceValue(func() []string {
	entries, err := fs.ReadDir(ConciergeFS(), ".")
	if err != nil {
		panic(fmt.Errorf("aiassist: read embedded concierge skills: %w", err))
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	if i := slices.Index(names, conciergeEntryPoint); i > 0 {
		names = slices.Insert(slices.Delete(names, i, i+1), 0, conciergeEntryPoint)
	}
	return names
})

func allSuiteSkills() []string {
	return slices.Concat(ConciergeSkills(), []string{skill.SkillName})
}

func InstallSuite(dir, version string, force bool, docs fs.FS) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve install dir: %w", err)
	}

	if !force {
		var conflicts []string
		for _, name := range allSuiteSkills() {
			if info, statErr := os.Stat(filepath.Join(abs, name, "SKILL.md")); statErr == nil && info.Size() > 0 {
				conflicts = append(conflicts, name)
			}
		}
		if len(conflicts) > 0 {
			return fmt.Errorf("refusing to overwrite existing skills %v — pass --force to replace", conflicts)
		}
	}

	if err = writeStaticSkillTree(ConciergeFS(), abs, version); err != nil {
		return fmt.Errorf("write concierge skills: %w", err)
	}
	if err := replaceDocsCorpus(abs, version, docs); err != nil {
		return fmt.Errorf("install docs corpus: %w", err)
	}
	if _, err := skill.Install(abs, version, force); err != nil {
		return fmt.Errorf("install declarative-config: %w", err)
	}
	return nil
}

func replaceDocsCorpus(abs, version string, docs fs.FS) error {
	if docs == nil {
		return nil
	}
	populated, err := hasAnyFile(docs)
	if err != nil {
		return fmt.Errorf("inspect embedded documentation corpus: %w", err)
	}
	if !populated {
		return errors.New("embedded documentation corpus is empty; keeping the installed one")
	}
	dest := filepath.Join(abs, samDocsName, filepath.FromSlash(docsCorpusDir))
	if err := os.RemoveAll(dest); err != nil {
		return fmt.Errorf("wipe %s: %w", docsCorpusDir, err)
	}
	if err := writeStaticSkillTree(docs, dest, version); err != nil {
		return fmt.Errorf("write documentation corpus: %w", err)
	}
	return nil
}

func hasAnyFile(fsys fs.FS) (bool, error) {
	var found bool
	err := fs.WalkDir(fsys, ".", func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		found = true
		return fs.SkipAll
	})
	return found, err
}

type SuiteCheckResult struct {
	MissingSkills     []string
	VersionMismatch   map[string]string
	DeclMissingRefs   []string
	DocsCorpusMissing bool
}

func (r SuiteCheckResult) OK() bool {
	return len(r.MissingSkills) == 0 && len(r.VersionMismatch) == 0 && len(r.DeclMissingRefs) == 0 && !r.DocsCorpusMissing
}

func CheckSuite(dir, version string, expectDocs bool) (SuiteCheckResult, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return SuiteCheckResult{}, err
	}
	res := SuiteCheckResult{VersionMismatch: map[string]string{}}
	for _, name := range allSuiteSkills() {
		fm, ferr := skill.ParseFrontmatter(filepath.Join(abs, name, "SKILL.md"))
		if ferr != nil {
			res.MissingSkills = append(res.MissingSkills, name)
			continue
		}
		if fm.Version() != version {
			res.VersionMismatch[name] = fm.Version()
		}
	}
	if expectDocs && !slices.Contains(res.MissingSkills, samDocsName) {
		present, herr := hasDocsCorpus(abs)
		if herr != nil {
			return res, herr
		}
		res.DocsCorpusMissing = !present
	}
	if !slices.Contains(res.MissingSkills, skill.SkillName) {
		ct, cerr := skill.CheckTree(abs)
		if cerr != nil {
			return res, fmt.Errorf("check declarative-config references: %w", cerr)
		}
		res.DeclMissingRefs = ct.Missing
	}
	return res, nil
}

func hasDocsCorpus(abs string) (bool, error) {
	entries, err := os.ReadDir(filepath.Join(abs, samDocsName, filepath.FromSlash(docsCorpusDir)))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read documentation corpus: %w", err)
	}
	return len(entries) > 0, nil
}
