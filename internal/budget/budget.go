// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package budget

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/pricing"
)

const (
	ScaleExponent      = pricing.CostDecimalPlaces
	InputDecimalPlaces = 2
	scalePerCent       = 100_000_000
)

var (
	ErrMalformedAmount  = errors.New("amount is not a decimal number")
	ErrNegativeAmount   = errors.New("amount must not be negative")
	ErrAmountTooPrecise = errors.New("amount has more decimal places than a budget supports")
	ErrAmountTooLarge   = errors.New("amount is larger than a budget may be")
)

func ParseAmount(text string) (int64, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return 0, amountError(ErrMalformedAmount, text)
	}
	if strings.HasPrefix(trimmed, "-") {
		return 0, amountError(ErrNegativeAmount, text)
	}
	trimmed = strings.TrimPrefix(trimmed, "+")

	whole, frac, _ := strings.Cut(trimmed, ".")
	if whole == "" && frac == "" {
		return 0, amountError(ErrMalformedAmount, text)
	}
	if !isDigits(whole) || !isDigits(frac) {
		return 0, amountError(ErrMalformedAmount, text)
	}
	if len(frac) > InputDecimalPlaces {
		return 0, fmt.Errorf("%w: %q has %d decimal places, limit is %d",
			ErrAmountTooPrecise, text, len(frac), InputDecimalPlaces)
	}

	digits := whole + frac + strings.Repeat("0", ScaleExponent-len(frac))

	var value int64
	for _, r := range digits {
		digit := int64(r - '0')
		if value > (math.MaxInt64-digit)/10 {
			return 0, amountError(ErrAmountTooLarge, text)
		}
		value = value*10 + digit
	}
	return value, nil
}

func FormatAmount(v int64) string {
	sign := ""
	cents := v / scalePerCent
	if cents < 0 {
		sign, cents = "-", -cents
	}
	return fmt.Sprintf("%s%d.%02d", sign, cents/100, cents%100)
}

func WarnThreshold(amount int64, pct int) int64 {
	return amount / 100 * int64(pct)
}

func amountError(sentinel error, text string) error {
	return fmt.Errorf("%w: %q", sentinel, text)
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
