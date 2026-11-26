// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package desktop

import (
	"fmt"
	"strconv"
	"strings"
)

type platformFacts struct {
	OS         string
	Arch       string
	OSVersion  string
	Translated bool
}

const (
	minMacOSMajor   = 11
	minWindowsMajor = 10
)

func platformRefusal(f platformFacts) string {
	switch f.OS {
	case "darwin":
		if f.Translated {
			return "This build of Solace Agent Mesh is for Intel Macs, but it is running through Rosetta translation on an Apple silicon Mac, which is not supported. Please install the Apple silicon build of Solace Agent Mesh instead."
		}
		if v := darwinMajor(f.OSVersion); v > 0 && v < minMacOSMajor {
			return fmt.Sprintf("Solace Agent Mesh requires macOS %d or later. This Mac is running macOS %s.", minMacOSMajor, f.OSVersion)
		}
	case "windows":
		if f.Translated {
			return "This build of Solace Agent Mesh is for x64 (Intel or AMD) PCs, but it is running through emulation on an Arm-based Windows PC, which is not supported. Solace Agent Mesh requires an x64 processor."
		}
		if v, _ := parseMajorMinor(f.OSVersion); v > 0 && v < minWindowsMajor {
			return fmt.Sprintf("Solace Agent Mesh requires Windows %d or later. This PC is running Windows version %s.", minWindowsMajor, f.OSVersion)
		}
	}
	return ""
}

func darwinMajor(version string) int {
	major, minor := parseMajorMinor(version)
	if major == 10 && minor >= 16 {
		return 11
	}
	return major
}

func parseMajorMinor(version string) (int, int) {
	parts := strings.SplitN(version, ".", 3)
	major, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || major < 0 {
		return 0, 0
	}
	minor := 0
	if len(parts) > 1 {
		if m, merr := strconv.Atoi(parts[1]); merr == nil && m >= 0 {
			minor = m
		}
	}
	return major, minor
}
