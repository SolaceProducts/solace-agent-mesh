// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package config

import (
	"fmt"
	"maps"
	"strconv"
	"strings"
	"time"
)

var _ Config = (*mapConfig)(nil)

func FromMap(data map[string]any) Config {
	return &mapConfig{data: data}
}

type mapConfig struct {
	data map[string]any
}

func (c *mapConfig) GetString(key string) string {
	v, ok := c.data[key]
	if !ok {
		return ""
	}
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func (c *mapConfig) GetInt(key string) int {
	v, ok := c.data[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case string:
		if i, err := strconv.Atoi(n); err == nil {
			return i
		}
		return 0
	default:
		return 0
	}
}

func (c *mapConfig) GetBool(key string) bool {
	v, ok := c.data[key]
	if !ok {
		return false
	}
	switch b := v.(type) {
	case bool:
		return b
	case string:
		switch strings.ToLower(b) {
		case "true", "1", "yes", "on":
			return true
		default:
			return false
		}
	case int:
		return b != 0
	case int64:
		return b != 0
	case float64:
		return b != 0
	default:
		return false
	}
}

func (c *mapConfig) GetDuration(key string) time.Duration {
	v, ok := c.data[key]
	if !ok {
		return 0
	}
	switch d := v.(type) {
	case string:
		parsed, err := time.ParseDuration(d)
		if err != nil {
			return 0
		}
		return parsed
	case int:
		return time.Duration(d) * time.Second
	case int64:
		return time.Duration(d) * time.Second
	case float64:
		return time.Duration(d) * time.Second
	default:
		return 0
	}
}

func (c *mapConfig) GetSection(key string) Config {
	v, ok := c.data[key]
	if !ok {
		return nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	return &mapConfig{data: m}
}

func (c *mapConfig) GetStringMap(key string) map[string]string {
	v, ok := c.data[key]
	if !ok {
		return nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	result := make(map[string]string, len(m))
	for k, val := range m {
		result[k] = fmt.Sprint(val)
	}
	return result
}

func (c *mapConfig) GetStringSlice(key string) []string {
	v, ok := c.data[key]
	if !ok {
		return nil
	}
	slice, ok := v.([]any)
	if !ok {
		return nil
	}
	result := make([]string, len(slice))
	for i, item := range slice {
		result[i] = fmt.Sprint(item)
	}
	return result
}

func (c *mapConfig) GetRaw(key string) any {
	return c.data[key]
}

func (c *mapConfig) Has(key string) bool {
	_, ok := c.data[key]
	return ok
}

func AsMap(cfg Config) map[string]any {
	mc, ok := cfg.(*mapConfig)
	if !ok {
		return nil
	}
	return maps.Clone(mc.data)
}
