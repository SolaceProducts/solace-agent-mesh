// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
	"github.com/SolaceProducts/solace-agent-mesh/internal/skillsecret"
)

const (
	skillRedactionFailedMsg       = skillsecret.RedactionRefusedMessage
	skillUnresolvedSentinelMsg    = "SKILL.md contains " + skillsecret.RedactedSentinel + " values with no stored value to keep"
	skillUnresolvedSentinelAdvice = "to keep stored secrets leave every secret in this tool as <REDACTED> and change nothing else in it; otherwise supply all of them as real values or ${VAR} references"
)

func isSkillManifestName(name string) bool {
	return strings.EqualFold(path.Base(name), skillManifestFile)
}

func redactSkillMD(content []byte, topLevel bool) ([]byte, error) {
	if !topLevel {
		out, _, _ := skillsecret.RedactNested(content)
		return out, nil
	}
	out, _, err := skillsecret.Redact(content)
	if err != nil {
		return nil, &ValidationError{Message: skillRedactionFailedMsg}
	}
	return out, nil
}

func rewriteSkillManifests(data []byte, transform func(name string, content []byte, topLevel bool) ([]byte, error)) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, &ValidationError{Message: "skill bundle is not a readable ZIP"}
	}
	replacements := map[*zip.File][]byte{}
	topPrefix := skillZIPTopPrefix(zr)
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || !isSkillManifestName(f.Name) {
			continue
		}
		if f.UncompressedSize64 > uint64(maxZipUncompressed) {
			return nil, &ValidationError{Message: "SKILL.md is too large"}
		}
		content, readErr := readSkillZIPEntry(f)
		if readErr != nil {
			return nil, readErr
		}
		topLevel := strings.TrimSuffix(f.Name, path.Base(f.Name)) == topPrefix
		rewritten, transformErr := transform(f.Name, content, topLevel)
		if transformErr != nil {
			return nil, transformErr
		}
		if !bytes.Equal(rewritten, content) {
			replacements[f] = rewritten
		}
	}
	if len(replacements) == 0 {
		return data, nil
	}
	return rebuildSkillZIP(zr, replacements)
}

var errSkillEntryUnreadable = errors.New("skill bundle entry unreadable")

func readSkillZIPEntry(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("open skill bundle entry: %w: %w", errSkillEntryUnreadable, err)
	}
	defer func() { _ = rc.Close() }()
	content, err := io.ReadAll(io.LimitReader(rc, int64(maxZipUncompressed)+1))
	if err != nil {
		return nil, fmt.Errorf("read skill bundle entry: %w: %w", errSkillEntryUnreadable, err)
	}
	return content, nil
}

func rebuildSkillZIP(zr *zip.Reader, replacements map[*zip.File][]byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if err := zw.SetComment(zr.Comment); err != nil {
		return nil, fmt.Errorf("set skill bundle comment: %w", err)
	}
	for _, f := range zr.File {
		content, replaced := replacements[f]
		if !replaced {
			if err := zw.Copy(f); err != nil {
				return nil, fmt.Errorf("copy skill bundle entry: %w", err)
			}
			continue
		}
		hdr := &zip.FileHeader{Name: f.Name, Method: zip.Deflate, Modified: f.Modified}
		hdr.SetMode(f.Mode())
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return nil, fmt.Errorf("create skill bundle entry: %w", err)
		}
		if _, err := w.Write(content); err != nil {
			return nil, fmt.Errorf("write skill bundle entry: %w", err)
		}
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("finalize skill bundle: %w", err)
	}
	return buf.Bytes(), nil
}

func (s *SkillService) restoreSkillSecrets(ctx context.Context, skillName string, data []byte) ([]byte, error) {
	if _, err := zip.NewReader(bytes.NewReader(data), int64(len(data))); err != nil {
		return data, nil
	}
	var (
		unresolved []string
		stored     map[string][]byte
		loaded     bool
	)
	rebuilt, err := rewriteSkillManifests(data, func(name string, incoming []byte, _ bool) ([]byte, error) {
		if !bytes.Contains(incoming, []byte(skillsecret.RedactedSentinel)) {
			return incoming, nil
		}
		if !loaded {
			var storedErr error
			if stored, storedErr = s.storedSkillManifests(ctx, skillName); storedErr != nil {
				return nil, storedErr
			}
			loaded = true
		}
		out, missing, restoreErr := skillsecret.Restore(incoming, stored[name])
		if restoreErr != nil {
			return nil, &ValidationError{Message: skillRedactionFailedMsg}
		}
		unresolved = append(unresolved, missing...)
		return out, nil
	})
	if errors.Is(err, errSkillEntryUnreadable) {
		return nil, &ValidationError{Message: "skill bundle entry could not be read"}
	}
	if err != nil {
		return nil, err
	}
	if len(unresolved) > 0 {
		details := make(map[string][]string, len(unresolved))
		for _, p := range unresolved {
			details[p] = []string{skillUnresolvedSentinelAdvice}
		}
		return nil, &ValidationError{Message: skillUnresolvedSentinelMsg, Details: details}
	}
	return rebuilt, nil
}

func (s *SkillService) storedSkillManifests(ctx context.Context, skillName string) (map[string][]byte, error) {
	if s.resourceStore == nil {
		return nil, nil
	}
	rc, err := getRuntimeZIP(ctx, s.resourceStore, skillRuntimeKey(s.namespace, skillName))
	if err != nil {
		if objectstore.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("get stored skill ZIP: %w", err)
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(io.LimitReader(rc, s.maxZipCompressed+1))
	if err != nil {
		return nil, fmt.Errorf("read stored skill ZIP: %w", err)
	}
	if int64(len(data)) > s.maxZipCompressed {
		return nil, &ValidationError{Message: "stored skill bundle exceeds the size limit"}
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, nil
	}
	manifests := map[string][]byte{}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || !isSkillManifestName(f.Name) || f.UncompressedSize64 > uint64(maxZipUncompressed) {
			continue
		}
		content, readErr := readSkillZIPEntry(f)
		if readErr != nil {
			return nil, fmt.Errorf("read stored skill bundle entry: %v", readErr)
		}
		manifests[f.Name] = content
	}
	return manifests, nil
}
