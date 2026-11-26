// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pricing

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

const (
	DecimalPlaces = 4

	TokenQuantity = 1_000_000

	MaxAmount Amount = 1_000_000 * 10_000

	MaxTokensBeforeCostOverflows = math.MaxInt64 / int64(MaxAmount)

	CostDecimalPlaces = 10
)

func FormatCost(v int64) string {
	sign := ""
	if v < 0 {
		sign, v = "-", -v
	}
	unit := int64(1)
	for range CostDecimalPlaces {
		unit *= 10
	}
	return fmt.Sprintf("%s%d.%0*d", sign, v/unit, CostDecimalPlaces, v%unit)
}

type Amount int64

var (
	ErrMalformedAmount = errors.New("amount is not a decimal number")

	ErrNegativeAmount = errors.New("amount must not be negative")

	ErrAmountTooPrecise = errors.New("amount has more decimal places than a price supports")

	ErrAmountTooLarge = errors.New("amount is larger than a price may be")

	ErrStoredPriceInvalid = errors.New("stored price is not one Store writes")
)

func amountErr(err error, text string) error {
	return fmt.Errorf("%w: %q", err, text)
}

func ParseAmount(text string) (Amount, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return 0, amountErr(ErrMalformedAmount, text)
	}
	if strings.HasPrefix(trimmed, "-") {
		return 0, amountErr(ErrNegativeAmount, text)
	}
	trimmed = strings.TrimPrefix(trimmed, "+")

	whole, frac, hasPoint := strings.Cut(trimmed, ".")
	if whole == "" && frac == "" {
		return 0, amountErr(ErrMalformedAmount, text)
	}
	if hasPoint && strings.Contains(frac, ".") {
		return 0, amountErr(ErrMalformedAmount, text)
	}
	if !isDigits(whole) || !isDigits(frac) {
		return 0, amountErr(ErrMalformedAmount, text)
	}
	if len(frac) > DecimalPlaces {
		return 0, fmt.Errorf("%w: %q has %d decimal places, limit is %d",
			ErrAmountTooPrecise, text, len(frac), DecimalPlaces)
	}

	digits := whole + frac + strings.Repeat("0", DecimalPlaces-len(frac))

	var value int64
	for _, r := range digits {
		digit := int64(r - '0')
		if value > (math.MaxInt64-digit)/10 {
			return 0, amountErr(ErrAmountTooLarge, text)
		}
		value = value*10 + digit
		if value > int64(MaxAmount) {
			return 0, amountErr(ErrAmountTooLarge, text)
		}
	}
	return Amount(value), nil
}

func (a Amount) String() string {
	digits := fmt.Sprintf("%0*d", DecimalPlaces+1, int64(a))
	split := len(digits) - DecimalPlaces
	whole, frac := digits[:split], digits[split:]

	frac = strings.TrimRight(frac, "0")
	if frac == "" {
		return whole
	}
	return whole + "." + frac
}

type Bucket string

const (
	BucketInput      Bucket = "input"
	BucketOutput     Bucket = "output"
	BucketCacheRead  Bucket = "cacheRead"
	BucketCacheWrite Bucket = "cacheWrite"
)

func Buckets() []Bucket {
	return []Bucket{BucketInput, BucketOutput, BucketCacheRead, BucketCacheWrite}
}

type ModelPrice struct {
	Input      Amount
	Output     Amount
	CacheRead  *Amount
	CacheWrite *Amount
}

func (p ModelPrice) Resolve(b Bucket) Amount {
	switch b {
	case BucketOutput:
		return p.Output
	case BucketCacheRead:
		if p.BreaksOutCache() {
			return *p.CacheRead
		}
	case BucketCacheWrite:
		if p.BreaksOutCache() {
			return *p.CacheWrite
		}
	}
	return p.Input
}

func (p ModelPrice) Cost(b Bucket, tokens int64) int64 {
	rate := p.Resolve(b)
	if tokens < 0 || tokens > MaxTokensBeforeCostOverflows || rate < 0 || rate > MaxAmount {
		panic(fmt.Sprintf("pricing: cannot cost %d tokens at rate %d without overflow", tokens, rate))
	}
	return int64(rate) * tokens
}

func (p ModelPrice) BreaksOutCache() bool {
	return p.CacheRead != nil && p.CacheWrite != nil
}

func withinRange(v int64) bool {
	return v >= 0 && v <= int64(MaxAmount)
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

type Stored struct {
	Input      *int64
	Output     *int64
	CacheRead  *int64
	CacheWrite *int64
}

func (s Stored) Price() (*ModelPrice, error) {
	if s == (Stored{}) {
		return nil, nil
	}
	if s.Input == nil || s.Output == nil {
		return nil, storedErr("input and output must be set together")
	}
	if !withinRange(*s.Input) || !withinRange(*s.Output) {
		return nil, storedErr("input or output is outside the accepted range")
	}
	price := ModelPrice{Input: Amount(*s.Input), Output: Amount(*s.Output)}
	if s.CacheRead == nil && s.CacheWrite == nil {
		return &price, nil
	}
	if s.CacheRead == nil || s.CacheWrite == nil {
		return nil, storedErr("cacheRead and cacheWrite must be set together")
	}
	if !withinRange(*s.CacheRead) || !withinRange(*s.CacheWrite) {
		return nil, storedErr("cacheRead or cacheWrite is outside the accepted range")
	}
	read, write := Amount(*s.CacheRead), Amount(*s.CacheWrite)
	price.CacheRead, price.CacheWrite = &read, &write
	return &price, nil
}

func storedErr(reason string) error {
	return fmt.Errorf("%w: %s", ErrStoredPriceInvalid, reason)
}

func Store(p *ModelPrice) Stored {
	if p == nil {
		return Stored{}
	}
	input, output := int64(p.Input), int64(p.Output)
	stored := Stored{Input: &input, Output: &output}
	if p.BreaksOutCache() {
		read, write := int64(*p.CacheRead), int64(*p.CacheWrite)
		stored.CacheRead, stored.CacheWrite = &read, &write
	}
	return stored
}
