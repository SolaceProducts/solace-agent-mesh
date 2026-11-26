// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !linux

package str

func cgroupMemoryLimitBytes() int64 { return 0 }

func cgroupCPUThrottle() (cpuThrottleSample, bool) { return cpuThrottleSample{}, false }

func cgroupMemoryUnreclaimableBytes() (int64, bool) { return 0, false }
