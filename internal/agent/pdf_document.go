// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"

	"github.com/SolaceProducts/solace-agent-mesh/internal/indexing/convert"
)

const minPDFCharsPerPage = 50

const minSubstantialImageDim = 256

const pdfParseTimeout = 20 * time.Second

func init() { api.DisableConfigDir() } //nolint:staticcheck // SA1019: LoadConfiguration migration needs per-call config; separate change

func runPDFParse[T any](ctx context.Context, fn func() (T, error)) (T, error) {
	type result struct {
		v   T
		err error
	}
	ch := make(chan result, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				var zero T
				ch <- result{zero, fmt.Errorf("pdf parser panic: %v", r)}
			}
		}()
		v, err := fn()
		ch <- result{v, err}
	}()

	select {
	case r := <-ch:
		return r.v, r.err
	case <-time.After(pdfParseTimeout):
		var zero T
		return zero, fmt.Errorf("pdf parse timeout after %s", pdfParseTimeout)
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

type pdfStructure struct {
	pageCount   int
	substantial bool
}

func probePDFStructure(ctx context.Context, content []byte) (pdfStructure, error) {
	return runPDFParse(ctx, func() (pdfStructure, error) {
		conf := model.NewDefaultConfiguration()
		conf.Cmd = model.LISTIMAGES
		pctx, err := api.ReadValidateAndOptimize(ctx, bytes.NewReader(content), conf, nil)
		if err != nil {
			return pdfStructure{}, err
		}
		pages, err := api.PagesForSelection(pctx.PageCount, nil, true)
		if err != nil {
			return pdfStructure{}, err
		}
		imgs, _, err := pdfcpu.Images(ctx, pctx, pages)
		if err != nil {
			return pdfStructure{}, err
		}
		s := pdfStructure{pageCount: pctx.PageCount}
		for _, pageImages := range imgs {
			for _, img := range pageImages {
				if min(img.Width, img.Height) >= minSubstantialImageDim {
					s.substantial = true
					return s, nil
				}
			}
		}
		return s, nil
	})
}

func pdfHasSparseTextPage(ctx context.Context, content []byte, pageCount int) (bool, error) {
	res, err := convert.NewInProcessPDFExtractor().Extract(ctx, "", content)
	if err != nil {
		return false, err
	}
	if res == nil {
		return false, errors.New("pdf text extraction returned no result")
	}
	if pageCount < 1 {
		pageCount = 1
	}
	textRichPages := 0
	for _, c := range res.Citations {
		if c.CharEnd-c.CharStart >= minPDFCharsPerPage {
			textRichPages++
		}
	}
	return textRichPages < pageCount, nil
}
