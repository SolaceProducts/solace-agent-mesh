// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package budget

type Level string

const (
	LevelNone  Level = "none"
	LevelWarn  Level = "warn"
	LevelLimit Level = "limit"
)

func (l Level) Above(other Level) bool {
	return l.rank() > other.rank()
}

func (l Level) rank() int {
	switch l {
	case LevelLimit:
		return 2
	case LevelWarn:
		return 1
	default:
		return 0
	}
}

func LevelOf(spent int64, amount *int64, topUp int64, warnPercentage *int) Level {
	limit := EffectiveCap(amount, topUp)
	switch {
	case limit == nil:
		return LevelNone
	case spent >= *limit:
		return LevelLimit
	case warnPercentage != nil && spent >= WarnThreshold(*limit, *warnPercentage):
		return LevelWarn
	default:
		return LevelNone
	}
}
