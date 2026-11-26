// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package config

import (
	"context"
	"time"
)

type Config interface {
	GetString(key string) string

	GetInt(key string) int

	GetBool(key string) bool

	GetDuration(key string) time.Duration

	GetSection(key string) Config

	GetStringMap(key string) map[string]string

	GetStringSlice(key string) []string

	GetRaw(key string) any

	Has(key string) bool
}

type Loader interface {
	Load(ctx context.Context, path string) (Config, error)

	LoadBytes(ctx context.Context, data []byte) (Config, error)
}
