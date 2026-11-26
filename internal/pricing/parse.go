// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pricing

import (
	"errors"
	"fmt"
	"strings"
)

type Fields struct {
	Input      *string
	Output     *string
	CacheRead  *string
	CacheWrite *string
}

type FieldError struct {
	Bucket  Bucket
	Message string
}

func Parse(f Fields) (*ModelPrice, []FieldError) {
	input, hasInput := given(f.Input)
	output, hasOutput := given(f.Output)
	cacheRead, hasCacheRead := given(f.CacheRead)
	cacheWrite, hasCacheWrite := given(f.CacheWrite)

	var errs []FieldError

	if hasInput != hasOutput {
		missing, present := BucketOutput, BucketInput
		if hasOutput {
			missing, present = BucketInput, BucketOutput
		}
		errs = append(errs, FieldError{
			Bucket:  missing,
			Message: fmt.Sprintf("required once %s is priced", present),
		})
	}

	if hasCacheRead != hasCacheWrite {
		missing, present := BucketCacheWrite, BucketCacheRead
		if hasCacheWrite {
			missing, present = BucketCacheRead, BucketCacheWrite
		}
		errs = append(errs, FieldError{
			Bucket:  missing,
			Message: fmt.Sprintf("required once %s is priced", present),
		})
	}

	if (hasCacheRead || hasCacheWrite) && (!hasInput || !hasOutput) {
		errs = append(errs, FieldError{
			Message: fmt.Sprintf("%s and %s can only be priced once %s and %s are",
				BucketCacheRead, BucketCacheWrite, BucketInput, BucketOutput),
		})
	}

	price := ModelPrice{}
	parse := func(bucket Bucket, text string, present bool) (Amount, bool) {
		if !present {
			return 0, false
		}
		amount, err := ParseAmount(text)
		if err != nil {
			errs = append(errs, FieldError{Bucket: bucket, Message: amountMessage(err)})
			return 0, false
		}
		return amount, true
	}

	price.Input, _ = parse(BucketInput, input, hasInput)
	price.Output, _ = parse(BucketOutput, output, hasOutput)
	if amount, ok := parse(BucketCacheRead, cacheRead, hasCacheRead); ok {
		price.CacheRead = &amount
	}
	if amount, ok := parse(BucketCacheWrite, cacheWrite, hasCacheWrite); ok {
		price.CacheWrite = &amount
	}

	if len(errs) > 0 {
		return nil, errs
	}
	if !hasInput {
		return nil, nil
	}
	return &price, nil
}

func Details(errs []FieldError, prefix string) map[string][]string {
	if len(errs) == 0 {
		return nil
	}
	details := make(map[string][]string, len(errs))
	for _, e := range errs {
		field := strings.TrimSuffix(prefix, ".")
		if e.Bucket != "" {
			field = prefix + string(e.Bucket)
		}
		details[field] = append(details[field], e.Message)
	}
	return details
}

func amountMessage(err error) string {
	switch {
	case errors.Is(err, ErrNegativeAmount):
		return "must not be negative"
	case errors.Is(err, ErrAmountTooPrecise):
		return fmt.Sprintf("must have at most %d decimal places", DecimalPlaces)
	case errors.Is(err, ErrAmountTooLarge):
		return fmt.Sprintf("must be at most %s per %s tokens", MaxAmount, formatQuantity(TokenQuantity))
	default:
		return fmt.Sprintf("must be an amount in dollars per %s tokens, for example 3.00", formatQuantity(TokenQuantity))
	}
}

func formatQuantity(n int) string {
	if n == 1_000_000 {
		return "1M"
	}
	return fmt.Sprintf("%d", n)
}

func given(v *string) (string, bool) {
	if v == nil {
		return "", false
	}
	trimmed := strings.TrimSpace(*v)
	return trimmed, trimmed != ""
}
