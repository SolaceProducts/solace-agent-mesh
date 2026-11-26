// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sacexpr

import (
	"fmt"
	"strconv"
	"strings"
)

func getPath(data any, path string) (any, error) {
	if path == "" {
		return data, nil
	}

	segments := parsePath(path)
	current := data

	for _, seg := range segments {
		if current == nil {
			return nil, nil
		}

		if seg.isIndex {
			current = indexInto(current, seg.index)
		} else {
			current = lookupKey(current, seg.key)
		}
	}

	return current, nil
}

func setPath(data any, path string, value any) (any, error) {
	if path == "" {
		return value, nil
	}

	segments := parsePath(path)
	if len(segments) == 0 {
		return value, nil
	}

	if data == nil {
		data = make(map[string]any)
	}

	root := data
	current := data

	for i, seg := range segments {
		isLast := i == len(segments)-1

		if seg.isIndex {
			arr, ok := current.([]any)
			if !ok {
				return root, fmt.Errorf("cannot index into %T at path segment [%d]", current, seg.index)
			}
			idx := resolveIndex(seg.index, len(arr))
			if idx < 0 || idx >= len(arr) {
				return root, fmt.Errorf("index %d out of range (len %d)", seg.index, len(arr))
			}
			if isLast {
				arr[idx] = value
			} else {
				current = arr[idx]
			}
		} else {
			m, ok := current.(map[string]any)
			if !ok {
				return root, fmt.Errorf("cannot set key %q on %T", seg.key, current)
			}
			if isLast {
				m[seg.key] = value
			} else {
				next, exists := m[seg.key]
				if !exists || next == nil {
					nextSeg := segments[i+1]
					if nextSeg.isIndex {
						return root, fmt.Errorf("cannot auto-create slice for key %q", seg.key)
					}
					newMap := make(map[string]any)
					m[seg.key] = newMap
					current = newMap
				} else {
					current = next
				}
			}
		}
	}

	return root, nil
}

type pathSegment struct {
	key     string
	index   int
	isIndex bool
}

func parsePath(path string) []pathSegment {
	var segments []pathSegment
	i := 0

	for i < len(path) {
		if path[i] == '[' {
			end := strings.IndexByte(path[i:], ']')
			if end == -1 {
				segments = append(segments, pathSegment{key: path[i:]})
				break
			}
			idxStr := path[i+1 : i+end]
			idx, err := strconv.Atoi(idxStr)
			if err != nil {
				segments = append(segments, pathSegment{key: path[i : i+end+1]})
			} else {
				segments = append(segments, pathSegment{index: idx, isIndex: true})
			}
			i += end + 1
			if i < len(path) && path[i] == '.' {
				i++
			}
		} else {
			end := len(path)
			for j := i; j < len(path); j++ {
				if path[j] == '.' || path[j] == '[' {
					end = j
					break
				}
			}
			key := path[i:end]
			if key != "" {
				segments = append(segments, pathSegment{key: key})
			}
			i = end
			if i < len(path) && path[i] == '.' {
				i++
			}
		}
	}

	return segments
}

func lookupKey(data any, key string) any {
	switch m := data.(type) {
	case map[string]any:
		return m[key]
	default:
		return nil
	}
}

func indexInto(data any, index int) any {
	switch arr := data.(type) {
	case []any:
		idx := resolveIndex(index, len(arr))
		if idx < 0 || idx >= len(arr) {
			return nil
		}
		return arr[idx]
	default:
		return nil
	}
}

func resolveIndex(index, length int) int {
	if index < 0 {
		return length + index
	}
	return index
}
