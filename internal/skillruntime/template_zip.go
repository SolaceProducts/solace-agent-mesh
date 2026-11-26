// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skillruntime

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"path"
	"strings"
)

const (
	TemplateArtifactMIME = "application/vnd.sam-template+zip"
	TemplateArtifactExt  = ".samt"
)

const templateSidecarSuffix = ".template.yaml"

const templateZipAssetsPrefix = assetsRoot + "/"

const maxTemplateZipTotal = 4 * 1024 * 1024

func IsBareFilename(name string) bool {
	return name != "" && name != "." && name != ".." &&
		!strings.ContainsAny(name, `/\`) && name == path.Base(name)
}

func BuildTemplateZip(assetName, body, sidecar string) ([]byte, error) {
	if !IsBareFilename(assetName) {
		return nil, fmt.Errorf("template.file %q must be a bare filename (no path or directory traversal)", assetName)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range []struct{ name, content string }{
		{templateZipAssetsPrefix + assetName, body},
		{templateZipAssetsPrefix + sidecarFilename(assetName), sidecar},
	} {
		fw, err := zw.Create(e.name)
		if err != nil {
			return nil, err
		}
		if _, err := fw.Write([]byte(e.content)); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func ReadTemplateZip(zipBytes []byte) (assetName, body, sidecar string, err error) {
	zr, zerr := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if zerr != nil {
		return "", "", "", fmt.Errorf("not a zip archive: %w", zerr)
	}
	var total int64
	entries := map[string]string{}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if !strings.HasPrefix(f.Name, templateZipAssetsPrefix) {
			return "", "", "", fmt.Errorf("unexpected entry %q (all entries must be under %s)", f.Name, templateZipAssetsPrefix)
		}
		base := strings.TrimPrefix(f.Name, templateZipAssetsPrefix)
		if !IsBareFilename(base) {
			return "", "", "", fmt.Errorf("unsafe entry path %q", f.Name)
		}
		rc, oerr := f.Open()
		if oerr != nil {
			return "", "", "", oerr
		}
		data, rerr := io.ReadAll(io.LimitReader(rc, maxTemplateZipTotal-total+1))
		_ = rc.Close()
		if rerr != nil {
			return "", "", "", rerr
		}
		total += int64(len(data))
		if total > maxTemplateZipTotal {
			return "", "", "", fmt.Errorf("template artifact exceeds %d bytes", maxTemplateZipTotal)
		}
		entries[base] = string(data)
	}
	if len(entries) != 2 {
		return "", "", "", fmt.Errorf("template artifact must contain exactly the body and its %s sidecar (found %d entries)", templateSidecarSuffix, len(entries))
	}
	var sidecarBase string
	for base, content := range entries {
		if strings.HasSuffix(base, templateSidecarSuffix) {
			sidecarBase, sidecar = base, content
		} else {
			assetName, body = base, content
		}
	}
	if assetName == "" || sidecar == "" {
		return "", "", "", fmt.Errorf("template artifact must contain assets/<file> plus assets/<file-stem>%s", templateSidecarSuffix)
	}
	if want := sidecarFilename(assetName); want != sidecarBase {
		return "", "", "", fmt.Errorf("sidecar %q does not match body %q (expected %q)", sidecarBase, assetName, want)
	}
	return assetName, body, sidecar, nil
}
