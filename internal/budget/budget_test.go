// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package budget

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"testing"
)

var (
	maxCents     = int64(math.MaxInt64) / scalePerCent
	maxAmountStr = fmt.Sprintf("%d.%02d", maxCents/100, maxCents%100)
)

func TestScalePerCentMatchesScaleExponent(t *testing.T) {
	want := int64(1)
	for range ScaleExponent - InputDecimalPlaces {
		want *= 10
	}
	if scalePerCent != want {
		t.Fatalf("scalePerCent = %d, want 10^(ScaleExponent-InputDecimalPlaces) = %d", int64(scalePerCent), want)
	}
}

func TestParseAmount_ExactWhereAFloatIsNot(t *testing.T) {
	for _, c := range []struct {
		text  string
		cents int64
	}{
		{"19.99", 1999}, {"0.57", 57}, {"2.51", 251}, {"1234567.89", 123456789},
	} {
		want := c.cents * scalePerCent

		f, err := strconv.ParseFloat(c.text, 64)
		if err != nil {
			t.Fatalf("ParseFloat(%q): %v", c.text, err)
		}
		if viaFloat := int64(f * math.Pow10(ScaleExponent)); viaFloat == want {
			t.Errorf("premise: %q survives a float64 at scale %d; pick a value that does not", c.text, ScaleExponent)
		}

		got, err := ParseAmount(c.text)
		if err != nil {
			t.Errorf("ParseAmount(%q): %v", c.text, err)
			continue
		}
		if got != want {
			t.Errorf("ParseAmount(%q) = %d, want %d", c.text, got, want)
		}
	}
}

func TestParseAmount_RoundTripsThroughFormat(t *testing.T) {
	for _, text := range []string{"0.00", "0.07", "0.29", "1.10", "4.35", "8.20", "19.99", "1234567.89", maxAmountStr} {
		got, err := ParseAmount(text)
		if err != nil {
			t.Errorf("ParseAmount(%q): %v", text, err)
			continue
		}
		if back := FormatAmount(got); back != text {
			t.Errorf("FormatAmount(ParseAmount(%q)) = %q", text, back)
		}
	}
}

func TestParseAmount_AcceptedForms(t *testing.T) {
	for _, c := range []struct {
		text  string
		cents int64
	}{
		{"5", 500}, {".5", 50}, {"5.", 500}, {"+5", 500}, {"+.5", 50},
		{"  5  ", 500}, {"\t5.25\n", 525}, {"0", 0}, {"0.00", 0}, {"007.10", 710},
	} {
		got, err := ParseAmount(c.text)
		if err != nil {
			t.Errorf("ParseAmount(%q): %v", c.text, err)
			continue
		}
		if want := c.cents * scalePerCent; got != want {
			t.Errorf("ParseAmount(%q) = %d, want %d", c.text, got, want)
		}
	}
}

func TestParseAmount_RejectedForms(t *testing.T) {
	for _, c := range []struct {
		text string
		want error
	}{
		{"", ErrMalformedAmount},
		{"   ", ErrMalformedAmount},
		{".", ErrMalformedAmount},
		{"+", ErrMalformedAmount},
		{"+.", ErrMalformedAmount},
		{"++5", ErrMalformedAmount},
		{"abc", ErrMalformedAmount},
		{"1.2.3", ErrMalformedAmount},
		{"1e3", ErrMalformedAmount},
		{"1,000", ErrMalformedAmount},
		{"1_000", ErrMalformedAmount},
		{"5 5", ErrMalformedAmount},
		{"٥", ErrMalformedAmount},
		{"-5", ErrNegativeAmount},
		{"-0.50", ErrNegativeAmount},
		{" -1", ErrNegativeAmount},
		{"1.234", ErrAmountTooPrecise},
		{"0.001", ErrAmountTooPrecise},
		{"99999999999999999999", ErrAmountTooLarge},
	} {
		got, err := ParseAmount(c.text)
		if !errors.Is(err, c.want) {
			t.Errorf("ParseAmount(%q) = %d, %v; want %v", c.text, got, err, c.want)
		}
	}
}

func TestParseAmount_StorageBound(t *testing.T) {
	got, err := ParseAmount(maxAmountStr)
	if err != nil {
		t.Fatalf("ParseAmount(%q), the largest representable amount: %v", maxAmountStr, err)
	}
	if want := maxCents * scalePerCent; got != want {
		t.Errorf("ParseAmount(%q) = %d, want %d", maxAmountStr, got, want)
	}

	next := maxCents + 1
	nextStr := fmt.Sprintf("%d.%02d", next/100, next%100)
	if _, err := ParseAmount(nextStr); !errors.Is(err, ErrAmountTooLarge) {
		t.Errorf("ParseAmount(%q), one cent over: %v, want %v", nextStr, err, ErrAmountTooLarge)
	}

	wholeMax := fmt.Sprintf("%d.99", maxCents/100)
	if maxCents%100 == 99 {
		t.Fatalf("test precondition: max amount %s already ends in .99", maxAmountStr)
	}
	if _, err := ParseAmount(wholeMax); !errors.Is(err, ErrAmountTooLarge) {
		t.Errorf("ParseAmount(%q): %v, want %v", wholeMax, err, ErrAmountTooLarge)
	}
}

func TestFormatAmount(t *testing.T) {
	for _, c := range []struct {
		stored int64
		want   string
	}{
		{0, "0.00"},
		{5 * scalePerCent, "0.05"},
		{1999 * scalePerCent, "19.99"},
		{100_000 * scalePerCent, "1000.00"},
		{1999*scalePerCent + scalePerCent - 1, "19.99"},
		{scalePerCent - 1, "0.00"},
		{maxCents * scalePerCent, maxAmountStr},
		{math.MaxInt64, maxAmountStr},
	} {
		if got := FormatAmount(c.stored); got != c.want {
			t.Errorf("FormatAmount(%d) = %q, want %q", c.stored, got, c.want)
		}
	}
}

func TestWarnThresholdDoesNotWrapAtLargestAmount(t *testing.T) {
	exact := func(amount int64, pct int) *big.Int {
		v := new(big.Int).Mul(big.NewInt(amount), big.NewInt(int64(pct)))
		return v.Quo(v, big.NewInt(100))
	}
	for _, amount := range []int64{
		maxCents * scalePerCent,
		1999 * scalePerCent,
		scalePerCent,
		0,
	} {
		for _, pct := range []int{1, 50, 80, 99, 100} {
			got := WarnThreshold(amount, pct)
			if want := exact(amount, pct); got < 0 || big.NewInt(got).Cmp(want) != 0 {
				t.Errorf("WarnThreshold(%d, %d) = %d, want %s", amount, pct, got, want)
			}
		}
	}

	for _, pct := range []int{1, 50, 100} {
		got := WarnThreshold(math.MaxInt64, pct)
		loss := new(big.Int).Sub(exact(math.MaxInt64, pct), big.NewInt(got))
		if got < 0 || loss.Sign() < 0 || loss.Cmp(big.NewInt(99)) > 0 {
			t.Errorf("WarnThreshold(MaxInt64, %d) = %d; loss %s, want 0–99 and no wrap", pct, got, loss)
		}
	}
}
