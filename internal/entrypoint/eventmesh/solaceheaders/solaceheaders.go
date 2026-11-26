// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package solaceheaders

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	DeliveryDirect     = "direct"
	DeliveryGuaranteed = "guaranteed"
)

const (
	DMQEligible            = "solace_dmqEligible"
	TimeToLive             = "solace_timeToLive"
	Expiration             = "solace_expiration"
	Priority               = "solace_priority"
	CorrelationID          = "solace_correlationId"
	ApplicationMessageID   = "solace_applicationMessageId"
	ApplicationMessageType = "solace_applicationMessageType"
	SenderID               = "solace_senderId"
	SequenceNumber         = "solace_sequenceNumber"
	HTTPContentEncoding    = "solace_httpContentEncoding"
)

const (
	isReply         = "solace_isReply"
	senderTimestamp = "solace_senderTimestamp"
)

const reservedUserPropertyPrefix = "solace.messaging."

const maxPriority = 255

const maxMillis = math.MaxInt64 / int64(time.Millisecond)

type kind int

const (
	kindBool kind = iota
	kindMillis
	kindPriority
	kindUint64
	kindString
)

type Spec struct {
	Name           string
	kind           kind
	GuaranteedOnly bool
}

var specs = []Spec{
	{Name: DMQEligible, kind: kindBool, GuaranteedOnly: true},
	{Name: TimeToLive, kind: kindMillis, GuaranteedOnly: true},
	{Name: Expiration, kind: kindMillis, GuaranteedOnly: true},
	{Name: Priority, kind: kindPriority},
	{Name: CorrelationID, kind: kindString},
	{Name: ApplicationMessageID, kind: kindString},
	{Name: ApplicationMessageType, kind: kindString},
	{Name: SenderID, kind: kindString},
	{Name: SequenceNumber, kind: kindUint64},
	{Name: HTTPContentEncoding, kind: kindString},
}

func Names() []string {
	names := make([]string, len(specs))
	for i, s := range specs {
		names[i] = s.Name
	}
	slices.Sort(names)
	return names
}

func Lookup(name string) (Spec, error) {
	for _, s := range specs {
		if s.Name == name {
			return s, nil
		}
	}
	if name == isReply || name == senderTimestamp {
		return Spec{}, fmt.Errorf("header %s is not supported: the Solace Go messaging API cannot set it on an individual message", name)
	}
	return Spec{}, fmt.Errorf("unknown header %q; supported headers: %s", name, strings.Join(Names(), ", "))
}

func ValidateDeliveryMode(mode string) error {
	switch mode {
	case "", DeliveryDirect, DeliveryGuaranteed:
		return nil
	}
	return fmt.Errorf("delivery mode %q is not one of: %s, %s", mode, DeliveryDirect, DeliveryGuaranteed)
}

func ValidateUserPropertyKey(key string) error {
	if key == "" {
		return fmt.Errorf("user property name must not be empty")
	}
	if strings.HasPrefix(key, reservedUserPropertyPrefix) {
		return fmt.Errorf("user property %q uses the reserved %q prefix; set Solace headers under headers instead", key, reservedUserPropertyPrefix)
	}
	return nil
}

func (s Spec) Coerce(v any) (any, error) {
	switch s.kind {
	case kindBool:
		return coerceBool(s.Name, v)
	case kindMillis:
		n, err := coerceInt(s.Name, v, 0, maxMillis)
		return n, err
	case kindPriority:
		n, err := coerceInt(s.Name, v, 0, maxPriority)
		return int(n), err
	case kindUint64:
		return coerceUint64(s.Name, v)
	default:
		return coerceString(s.Name, v)
	}
}

func coerceBool(name string, v any) (bool, error) {
	switch t := v.(type) {
	case bool:
		return t, nil
	case string:
		if b, err := strconv.ParseBool(strings.TrimSpace(t)); err == nil {
			return b, nil
		}
	}
	return false, fmt.Errorf("%s must be true or false (got a %T)", name, v)
}

func coerceInt(name string, v any, lo, hi int64) (int64, error) {
	n, ok := toInt64(v)
	if !ok {
		return 0, fmt.Errorf("%s must be a whole number (got a %T)", name, v)
	}
	if n < lo || n > hi {
		if hi == math.MaxInt64 {
			return 0, fmt.Errorf("%s must be >= %d", name, lo)
		}
		return 0, fmt.Errorf("%s must be between %d and %d", name, lo, hi)
	}
	return n, nil
}

func coerceUint64(name string, v any) (uint64, error) {
	if s, ok := v.(string); ok {
		if u, err := strconv.ParseUint(strings.TrimSpace(s), 10, 64); err == nil {
			return u, nil
		}
		return 0, fmt.Errorf("%s must be a whole number >= 0 (got a %T)", name, v)
	}
	n, err := coerceInt(name, v, 0, math.MaxInt64)
	return uint64(n), err
}

func toInt64(v any) (int64, bool) {
	switch t := v.(type) {
	case int:
		return int64(t), true
	case int32:
		return int64(t), true
	case int64:
		return t, true
	case uint64:
		if t > math.MaxInt64 {
			return 0, false
		}
		return int64(t), true
	case float64:
		if t != math.Trunc(t) || t < math.MinInt64 || t > math.MaxInt64 {
			return 0, false
		}
		return int64(t), true
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
		return n, err == nil
	}
	return 0, false
}

const (
	staticPrefix   = "static:"
	templatePrefix = "template:"
	templateOpen   = "{{"
)

func IsTemplate(authored any) bool {
	s, ok := authored.(string)
	return ok && strings.Contains(s, templateOpen)
}

func RuntimeValue(authored any) any {
	s, ok := authored.(string)
	if !ok {
		return authored
	}
	if IsTemplate(s) {
		return templatePrefix + s
	}
	return staticPrefix + s
}

func AuthoredValue(runtime any) any {
	s, ok := runtime.(string)
	if !ok {
		return runtime
	}
	if literal, found := strings.CutPrefix(s, staticPrefix); found {
		return literal
	}
	if template, found := strings.CutPrefix(s, templatePrefix); found {
		return template
	}
	return templateOpen + "text://" + s + "}}"
}

func (s Spec) ValidateAuthoredValue(authored any) error {
	if IsTemplate(authored) {
		return nil
	}
	_, err := s.CoerceLiteral(authored)
	return err
}

func (s Spec) CoerceLiteral(v any) (any, error) {
	coerced, err := s.Coerce(v)
	if err != nil {
		return nil, fmt.Errorf("%w; value: %v", err, v)
	}
	return coerced, nil
}

func FormatValue(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case bool, int, int32, int64, uint64:
		return fmt.Sprint(t), true
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), true
	}
	return "", false
}

func coerceString(name string, v any) (string, error) {
	if s, ok := FormatValue(v); ok {
		return s, nil
	}
	return "", fmt.Errorf("%s must be a string (got a %T)", name, v)
}
