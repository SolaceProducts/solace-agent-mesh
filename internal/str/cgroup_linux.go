// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build linux

package str

import (
	"os"
	"strconv"
	"strings"
)

const unlimitedThreshold int64 = 1 << 62

func cgroupMemoryLimitBytes() int64 {
	if v, ok := readCgroupMemoryValue("/sys/fs/cgroup/memory.max"); ok {
		return v
	}
	if v, ok := readCgroupMemoryValue("/sys/fs/cgroup/memory/memory.limit_in_bytes"); ok {
		return v
	}
	return 0
}

func cgroupCPUThrottle() (cpuThrottleSample, bool) {
	for _, path := range []string{
		"/sys/fs/cgroup/cpu.stat",
		"/sys/fs/cgroup/cpu,cpuacct/cpu.stat",
		"/sys/fs/cgroup/cpu/cpu.stat",
	} {
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if s, ok := parseCPUStat(b); ok {
			return s, true
		}
	}
	return cpuThrottleSample{}, false
}

func readCgroupMemoryValue(path string) (int64, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	s := strings.TrimSpace(string(b))
	if s == "" || s == "max" {
		return 0, false
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v <= 0 || v >= unlimitedThreshold {
		return 0, false
	}
	return v, true
}

func cgroupMemoryUnreclaimableBytes() (int64, bool) {
	if usage, ok := readCgroupMemoryValue("/sys/fs/cgroup/memory.current"); ok {
		return unreclaimableFromStat(usage, readCgroupStatFile("/sys/fs/cgroup/memory.stat"), cgroupV2StatKeys), true
	}
	if usage, ok := readCgroupMemoryValue("/sys/fs/cgroup/memory/memory.usage_in_bytes"); ok {
		return unreclaimableFromStat(usage, readCgroupStatFile("/sys/fs/cgroup/memory/memory.stat"), cgroupV1StatKeys), true
	}
	return 0, false
}

func readCgroupStatFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}
