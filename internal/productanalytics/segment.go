// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package productanalytics

import (
	"regexp"
	"runtime"

	"github.com/SolaceProducts/solace-agent-mesh/internal/samversion"
)

const (
	SurfaceUI     = "ui"
	SurfaceCLI    = "cli"
	SurfaceServer = "server"
)

var surfaces = []string{SurfaceUI, SurfaceCLI, SurfaceServer}

const (
	propAppVersion = "appVersion"
	propOS         = "os"
	propArch       = "arch"
)

const (
	PropIdentity    = "identity"
	PropEmailDomain = "emailDomain"
	traitEmail      = "$email"
)

type ClientPlatform struct {
	OS         string
	Arch       string
	AppVersion string
}

func ParseClientPlatform(os, arch, appVersion string) ClientPlatform {
	return ClientPlatform{
		OS:         known(os, goosValues),
		Arch:       known(arch, goarchValues),
		AppVersion: versionShaped(appVersion),
	}
}

func localPlatform() ClientPlatform {
	return ClientPlatform{OS: runtime.GOOS, Arch: runtime.GOARCH, AppVersion: samversion.Version}
}

var goosValues = newStringSet(
	"aix", "android", "darwin", "dragonfly", "freebsd", "illumos", "ios", "js",
	"linux", "netbsd", "openbsd", "plan9", "solaris", "wasip1", "windows",
)

var goarchValues = newStringSet(
	"386", "amd64", "arm", "arm64", "loong64", "mips", "mips64", "mips64le",
	"mipsle", "ppc64", "ppc64le", "riscv64", "s390x", "wasm",
)

func newStringSet(values ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, v := range values {
		set[v] = struct{}{}
	}
	return set
}

func known(value string, vocabulary map[string]struct{}) string {
	if _, ok := vocabulary[value]; !ok {
		return ""
	}
	return value
}

var reAppVersion = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._+-]{0,63}$`)

func versionShaped(value string) string {
	if !reAppVersion.MatchString(value) {
		return ""
	}
	return value
}

func enrich(props map[string]any, surface string, host ClientPlatform) map[string]any {
	if props == nil {
		props = make(map[string]any, 4)
	}
	host = ParseClientPlatform(host.OS, host.Arch, host.AppVersion)
	stampOrStrip(props, propAppVersion, host.AppVersion)
	stampOrStrip(props, propOS, host.OS)
	stampOrStrip(props, propArch, host.Arch)
	props[PropInterface] = surface
	props[PropBuildVariant] = buildVariant
	return props
}

func stampOrStrip(props map[string]any, key, value string) {
	if value == "" {
		delete(props, key)
		return
	}
	props[key] = value
}

func installTraits() map[string]any {
	return map[string]any{
		propAppVersion: samversion.Version,
		propOS:         runtime.GOOS,
		propArch:       runtime.GOARCH,
	}
}

func stampSubject(props map[string]any, sub subject) {
	props[PropIdentity] = sub.side
	delete(props, PropEmailDomain)
	if domain := emailDomain(sub.email); sub.side == IdentityAccount && domain != "" {
		props[PropEmailDomain] = domain
	}
}

func subjectTraits(sub subject) map[string]any {
	traits := installTraits()
	if sub.side != IdentityAccount || sub.email == "" {
		return traits
	}
	traits[traitEmail] = sub.email
	if domain := emailDomain(sub.email); domain != "" {
		traits[PropEmailDomain] = domain
	}
	return traits
}
