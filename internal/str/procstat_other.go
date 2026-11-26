// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !linux

package str

func processDescendants(int) []int { return nil }

func procTreeCPU([]int) (map[int]treeSample, bool) { return nil, false }

func procCallMemory([]int) (map[int]int64, bool) { return nil, false }
