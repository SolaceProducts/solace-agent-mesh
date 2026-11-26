// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package indexing

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/indexing/convert"
)

func BuildProjectIndex(
	ctx context.Context,
	projectID, userID string,
	artSvc *artifact.ScopedService,
	sources []SourceFile,
	pdfExtractor convert.PDFExtractor,
	logger *slog.Logger,
	onConversionFailure func(filename, errMsg string),
) ([]byte, error) {
	if pdfExtractor == nil {
		pdfExtractor = convert.NewInProcessPDFExtractor()
	}
	reportFailure := func(filename string, err error) {
		if onConversionFailure != nil {
			onConversionFailure(filename, err.Error())
		}
	}
	storageSessionID := "project-" + projectID

	builder := NewIndexBuilder(ChunkSizeChars, OverlapChars)
	fileCount := 0

	for _, source := range sources {
		filename := source.Filename
		if !IsIndexSource(filename) {
			continue
		}
		version := &source.Version

		loc := artSvc.Resolve("", userID, storageSessionID, filename)

		if convert.ShouldConvert(filename, "") {
			convertedName := convert.ConvertedFilename(filename)
			convertedLoc := artSvc.Resolve("", userID, storageSessionID, convertedName)
			convertedContent, _, loadErr := artSvc.Inner.Load(ctx, convertedLoc, nil)

			if loadErr != nil || len(convertedContent) == 0 {
				sourceContent, sourceMIME, loadErr := artSvc.Inner.Load(ctx, loc, version)
				if loadErr != nil {
					logger.Warn("failed to load project artifact for conversion",
						"filename", filename, "error", loadErr)
					reportFailure(filename, loadErr)
					continue
				}

				convResult, convErr := convertOne(ctx, pdfExtractor, filename, sourceContent, sourceMIME)
				if convErr != nil {
					logger.Warn("failed to convert project artifact",
						"filename", filename, "error", convErr)
					reportFailure(filename, convErr)
					continue
				}

				_, saveErr := artSvc.Inner.Save(ctx, convertedLoc, []byte(convResult.Text), "text/plain")
				if saveErr != nil {
					logger.Warn("failed to save converted text",
						"filename", filename, "error", saveErr)
				}

				builder.AddDocument(Document{
					Filename:   convertedName,
					SourceFile: filename,
					Version:    1,
					Conversion: convResult,
				})
			} else {
				sourceContent, sourceMIME, loadErr := artSvc.Inner.Load(ctx, loc, version)
				if loadErr != nil {
					logger.Warn("failed to load source for re-indexing",
						"filename", filename, "error", loadErr)
					reportFailure(filename, loadErr)
					continue
				}
				convResult, convErr := convertOne(ctx, pdfExtractor, filename, sourceContent, sourceMIME)
				if convErr != nil {
					logger.Warn("failed to convert for re-indexing",
						"filename", filename, "error", convErr)
					reportFailure(filename, convErr)
					continue
				}

				builder.AddDocument(Document{
					Filename:   convertedName,
					SourceFile: filename,
					Version:    1,
					Conversion: convResult,
				})
			}
		} else {
			content, mimeType, loadErr := artSvc.Inner.Load(ctx, loc, version)
			if loadErr != nil {
				logger.Warn("failed to load text artifact",
					"filename", filename, "error", loadErr)
				reportFailure(filename, loadErr)
				continue
			}
			if mimeType == "" {
				mimeType = http.DetectContentType(content)
			}
			if !convert.IsTextFile(mimeType) {
				logger.Debug("skipping non-text artifact", "filename", filename, "mime", mimeType)
				continue
			}

			convResult, convErr := convert.Convert(filename, content, "")
			if convErr != nil {
				logger.Warn("failed to process text artifact",
					"filename", filename, "error", convErr)
				reportFailure(filename, convErr)
				continue
			}

			builder.AddDocument(Document{
				Filename:   filename,
				SourceFile: "",
				Version:    1,
				Conversion: convResult,
			})
		}

		fileCount++
	}

	built, err := builder.Build(projectID)
	if err != nil {
		return nil, fmt.Errorf("build index: %w", err)
	}
	built.Sources = Sources{Files: sources, IndexedFiles: fileCount}

	zipData, err := SerializeToZIP(built)
	if err != nil {
		return nil, fmt.Errorf("serialize index: %w", err)
	}

	logger.Info("built project BM25 index",
		"projectID", projectID,
		"files", fileCount,
		"chunks", built.Manifest.ChunkCount)

	return zipData, nil
}

func convertOne(
	ctx context.Context,
	pdfExtractor convert.PDFExtractor,
	filename string,
	content []byte,
	mimeType string,
) (*convert.Result, error) {
	if pdfExtractor != nil && isPDF(filename, mimeType) {
		return pdfExtractor.Extract(ctx, filename, content)
	}
	return convert.Convert(filename, content, mimeType)
}

func isPDF(filename, mimeType string) bool {
	if mimeType == "application/pdf" {
		return true
	}
	return strings.EqualFold(filepath.Ext(filename), ".pdf")
}
