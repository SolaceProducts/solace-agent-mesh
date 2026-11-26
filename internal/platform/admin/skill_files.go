// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"archive/zip"
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

var errSkillBundleMissing = errors.New("skill bundle not uploaded")

const maxSkillPreviewBytes = 1 << 20

const (
	skillReferencesDir = "references/"
	skillAssetsDir     = "assets/"
	skillManifestFile  = "SKILL.md"
)

func (s *SkillService) SkillFiles(ctx context.Context, id string) (*SkillFilesResponse, error) {
	_, data, err := s.readSkillZIP(ctx, id)
	if errors.Is(err, errSkillBundleMissing) {
		return &SkillFilesResponse{References: []SkillFileRef{}, Assets: []SkillFileRef{}}, nil
	}
	if err != nil {
		return nil, err
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, &ValidationError{Message: "skill bundle is not a readable ZIP"}
	}

	prefix := skillZIPTopPrefix(zr)
	resp := &SkillFilesResponse{References: []SkillFileRef{}, Assets: []SkillFileRef{}}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || strings.Contains(f.Name, "..") || strings.HasPrefix(f.Name, "/") {
			continue
		}
		rel, underPrefix := strings.CutPrefix(f.Name, prefix)
		if !underPrefix {
			continue
		}
		if r, ok := strings.CutPrefix(rel, skillReferencesDir); ok && r != "" {
			resp.References = append(resp.References, SkillFileRef{Path: r, SizeBytes: int64(f.UncompressedSize64)})
		} else if a, ok := strings.CutPrefix(rel, skillAssetsDir); ok && a != "" {
			resp.Assets = append(resp.Assets, SkillFileRef{Path: a, SizeBytes: int64(f.UncompressedSize64)})
		}
	}
	byPath := func(a, b SkillFileRef) int { return cmp.Compare(a.Path, b.Path) }
	slices.SortFunc(resp.References, byPath)
	slices.SortFunc(resp.Assets, byPath)
	return resp, nil
}

func (s *SkillService) SkillFileContent(ctx context.Context, id, relPath string) ([]byte, string, error) {
	if !isPreviewableSkillPath(relPath) {
		return nil, "", &ValidationError{Field: "path", Message: "file path must be SKILL.md or under references/ or assets/"}
	}
	_, data, err := s.readSkillZIP(ctx, id)
	if errors.Is(err, errSkillBundleMissing) {
		return nil, "", &NotFoundError{Resource: "skill file", ID: relPath}
	}
	if err != nil {
		return nil, "", err
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, "", &ValidationError{Message: "skill bundle is not a readable ZIP"}
	}

	target := skillZIPTopPrefix(zr) + relPath
	for _, f := range zr.File {
		if f.Name != target || f.FileInfo().IsDir() {
			continue
		}
		if f.UncompressedSize64 > maxSkillPreviewBytes {
			return nil, "", &ValidationError{Field: "path", Message: fmt.Sprintf("file too large to preview (max %d MB)", maxSkillPreviewBytes>>20)}
		}
		rc, openErr := f.Open()
		if openErr != nil {
			return nil, "", fmt.Errorf("open skill file: %w", openErr)
		}
		content, readErr := io.ReadAll(io.LimitReader(rc, maxSkillPreviewBytes))
		_ = rc.Close()
		if readErr != nil {
			return nil, "", fmt.Errorf("read skill file: %w", readErr)
		}
		if isSkillManifestName(relPath) {
			if content, err = redactSkillMD(content, relPath == skillManifestFile); err != nil {
				return nil, "", err
			}
		}
		return content, skillFileContentType(relPath, content), nil
	}
	return nil, "", &NotFoundError{Resource: "skill file", ID: relPath}
}

func (s *SkillService) readSkillZIP(ctx context.Context, id string) (*store.Skill, []byte, error) {
	skill, err := s.skillRepo.GetByID(ctx, id)
	if err != nil {
		return nil, nil, fmt.Errorf(getSkillErrFmt, err)
	}
	if skill == nil {
		return nil, nil, &NotFoundError{Resource: "skill", ID: id}
	}
	if s.resourceStore == nil {
		return nil, nil, &ValidationError{Message: "skill ZIP storage is not configured"}
	}
	rc, err := getRuntimeZIP(ctx, s.resourceStore, skillRuntimeKey(s.namespace, skill.Name))
	if err != nil {
		if objectstore.IsNotFound(err) {
			return nil, nil, errSkillBundleMissing
		}
		return nil, nil, fmt.Errorf("get skill ZIP: %w", err)
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(io.LimitReader(rc, s.maxZipCompressed+1))
	if err != nil {
		return nil, nil, fmt.Errorf("read skill ZIP: %w", err)
	}
	if int64(len(data)) > s.maxZipCompressed {
		return nil, nil, &ValidationError{Message: "skill bundle exceeds the size limit"}
	}
	return skill, data, nil
}

func skillZIPTopPrefix(zr *zip.Reader) string {
	topDir := ""
	for _, f := range zr.File {
		if !strings.EqualFold(path.Base(f.Name), skillManifestFile) {
			continue
		}
		dir := strings.TrimSuffix(f.Name, path.Base(f.Name))
		seg := strings.TrimSuffix(dir, "/")
		if seg == "" {
			return ""
		}
		if !strings.Contains(seg, "/") && !isSkillCategoryDir(seg) {
			topDir = dir
		}
	}
	return topDir
}

func isSkillCategoryDir(seg string) bool {
	return seg == "references" || seg == "assets" || seg == "tools"
}

func isPreviewableSkillPath(relPath string) bool {
	if relPath == "" || strings.HasPrefix(relPath, "/") || strings.Contains(relPath, "..") {
		return false
	}
	return relPath == skillManifestFile ||
		strings.HasPrefix(relPath, skillReferencesDir) ||
		strings.HasPrefix(relPath, skillAssetsDir)
}

func skillFileContentType(relPath string, content []byte) string {
	ct := mime.TypeByExtension(filepath.Ext(relPath))
	if ct == "" && len(content) > 0 {
		ct = http.DetectContentType(content)
	}
	if ct == "" {
		return "text/plain; charset=utf-8"
	}
	lower := strings.ToLower(ct)
	if isBrowserExecutedInline(lower) {
		return "text/plain; charset=utf-8"
	}
	return ct
}

func isBrowserExecutedInline(lower string) bool {
	return strings.HasPrefix(lower, "text/html") || strings.HasPrefix(lower, "application/xhtml") || strings.Contains(lower, "svg")
}

type skillFileReader struct {
	svc *SkillService
}

func (r skillFileReader) SkillFile(ctx context.Context, skillID, path string) ([]byte, error) {
	data, _, err := r.svc.SkillFileContent(ctx, skillID, path)
	return data, err
}
