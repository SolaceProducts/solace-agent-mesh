// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build linux

package str

import (
	"os"
	"path/filepath"
	"strconv"
)

const procRoot = "/proc"

func procTreeCPU(roots []int) (map[int]treeSample, bool) {
	procs, ok := readProcTable()
	if !ok {
		return nil, false
	}
	return sumTreeCPU(procs, roots), true
}

func procCallMemory(roots []int) (map[int]int64, bool) {
	procs, ok := readProcTable()
	if !ok {
		return nil, false
	}
	out := make(map[int]int64, len(roots))
	for root, members := range callMembers(procs, roots) {
		var total int64
		for _, pid := range members {
			b, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "status"))
			if err == nil {
				total += parseStatusMemory(string(b))
			}
		}
		out[root] = total
	}
	return out, true
}

func processDescendants(pid int) []int {
	procs, ok := readProcTable()
	if !ok {
		return nil
	}
	return descendantsOf(procs, pid)
}

func readProcTable() (map[int]procCPU, bool) {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil, false
	}
	procs := make(map[int]procCPU, len(entries))
	for _, e := range entries {
		name := e.Name()
		if name[0] < '0' || name[0] > '9' {
			continue
		}
		pid, err := strconv.Atoi(name)
		if err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join(procRoot, name, "stat"))
		if err != nil {
			continue
		}
		ppid, runnable, ticks, ok := parseProcStat(string(b))
		if !ok {
			continue
		}
		procs[pid] = procCPU{ppid: ppid, ticks: ticks, runnable: runnable, pgrp: parseProcStatPgrp(string(b))}
	}
	return procs, true
}
