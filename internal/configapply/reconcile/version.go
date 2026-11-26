// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"golang.org/x/mod/semver"
)

const gatewayProductID = "sam-gateway-go"

const versionEndpoint = "/api/v1/version"

type PlatformVersion struct {
	Products         []ProductVersion `json:"products"`
	MinClientVersion string           `json:"minClientVersion"`
	APIVersion       string           `json:"apiVersion"`
}

type ProductVersion struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

func (pv *PlatformVersion) gatewayVersion() string {
	for _, p := range pv.Products {
		if p.ID == gatewayProductID {
			return p.Version
		}
	}
	return ""
}

func (c *Client) FetchVersion(ctx context.Context) (*PlatformVersion, error) {
	var pv PlatformVersion
	if err := c.do(ctx, http.MethodGet, versionEndpoint, nil, &pv); err != nil {
		return nil, err
	}
	return &pv, nil
}

type CompatLevel int

const (
	CompatOK CompatLevel = iota
	CompatWarn
	CompatFatal
)

var semverCore = regexp.MustCompile(`v(\d+)\.(\d+)\.(\d+)`)

func normalizeSemver(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if v == "" || v == "dev" {
		return "", false
	}
	m := semverCore.FindStringSubmatch(v)
	if m == nil {
		return "", false
	}
	norm := "v" + m[1] + "." + m[2] + "." + m[3]
	if !semver.IsValid(norm) {
		return "", false
	}
	return norm, true
}

const rebuildHint = "rebuild it: go build -o $(go env GOPATH)/bin/sam ./cmd/sam"

func EvaluateVersionCompat(clientVersion string, pv *PlatformVersion) (CompatLevel, string) {
	if pv == nil {
		return CompatOK, ""
	}
	clientNorm, ok := normalizeSemver(clientVersion)
	if !ok {
		return CompatOK, ""
	}

	if floor, floorOK := normalizeSemver(pv.MinClientVersion); floorOK && semver.Compare(clientNorm, floor) < 0 {
		return CompatFatal, fmt.Sprintf(
			"your sam CLI (%s) is older than the platform requires (min %s); upgrade it to the version released with this server",
			clientNorm, floor)
	}

	serverNorm, ok := normalizeSemver(pv.gatewayVersion())
	if !ok {
		return CompatOK, ""
	}
	if semver.Compare(clientNorm, serverNorm) >= 0 {
		return CompatOK, ""
	}
	if semver.Major(clientNorm) != semver.Major(serverNorm) {
		return CompatFatal, fmt.Sprintf(
			"your sam CLI (%s) is a major version behind the platform (%s); %s",
			clientNorm, serverNorm, rebuildHint)
	}
	if semver.MajorMinor(clientNorm) != semver.MajorMinor(serverNorm) {
		return CompatWarn, fmt.Sprintf(
			"your sam CLI (%s) is behind the platform (%s); consider rebuilding it",
			clientNorm, serverNorm)
	}
	return CompatOK, ""
}
