// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/eventmesh/solaceheaders"
	"github.com/SolaceProducts/solace-agent-mesh/internal/sacexpr"
)

const staticPrefix = "static:"

type HeaderSetting struct {
	Spec       solaceheaders.Spec
	Literal    any
	Expression string
}

type UserPropertySetting struct {
	Name       string
	Literal    string
	Expression string
}

func (h *OutputHandlerConfig) guaranteed() bool {
	return h.DeliveryMode == solaceheaders.DeliveryGuaranteed
}

func parseOutputDelivery(m map[string]any, h *OutputHandlerConfig) error {
	if raw, present := m["delivery_mode"]; present && raw != nil {
		if _, isString := raw.(string); !isString {
			return fmt.Errorf("delivery_mode: must be %q or %q (got a %T)", solaceheaders.DeliveryDirect, solaceheaders.DeliveryGuaranteed, raw)
		}
	}
	h.DeliveryMode = getString(m, "delivery_mode")
	if err := solaceheaders.ValidateDeliveryMode(h.DeliveryMode); err != nil {
		return fmt.Errorf("delivery_mode: %w", err)
	}
	headers, err := parseHeaderSettings(m["headers"], h.guaranteed())
	if err != nil {
		return err
	}
	h.Headers = headers
	props, err := parseUserPropertySettings(m["user_properties"])
	if err != nil {
		return err
	}
	h.UserProperties = props
	return nil
}

func parseHeaderSettings(raw any, guaranteed bool) ([]HeaderSetting, error) {
	if raw == nil {
		return nil, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("headers: expected a map of header name to value")
	}
	settings := make([]HeaderSetting, 0, len(m))
	for _, name := range sortedKeys(m) {
		spec, err := solaceheaders.Lookup(name)
		if err != nil {
			return nil, fmt.Errorf("headers: %w", err)
		}
		if spec.GuaranteedOnly && !guaranteed {
			return nil, fmt.Errorf("headers.%s requires delivery_mode %q", name, solaceheaders.DeliveryGuaranteed)
		}
		setting, err := parseHeaderValue(spec, m[name])
		if err != nil {
			return nil, fmt.Errorf("headers.%s: %w", name, err)
		}
		settings = append(settings, setting)
	}
	return settings, nil
}

func parseHeaderValue(spec solaceheaders.Spec, v any) (HeaderSetting, error) {
	if expr, isString := v.(string); isString && !strings.HasPrefix(expr, staticPrefix) {
		if err := checkExpressionSource(expr); err != nil {
			return HeaderSetting{}, err
		}
		return HeaderSetting{Spec: spec, Expression: expr}, nil
	}
	if s, isString := v.(string); isString {
		v = strings.TrimPrefix(s, staticPrefix)
	}
	literal, err := spec.CoerceLiteral(v)
	if err != nil {
		return HeaderSetting{}, err
	}
	return HeaderSetting{Spec: spec, Literal: literal}, nil
}

func parseUserPropertySettings(raw any) ([]UserPropertySetting, error) {
	if raw == nil {
		return nil, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("user_properties: expected a map of property name to value")
	}
	settings := make([]UserPropertySetting, 0, len(m))
	for _, name := range sortedKeys(m) {
		if err := solaceheaders.ValidateUserPropertyKey(name); err != nil {
			return nil, fmt.Errorf("user_properties: %w", err)
		}
		setting, err := parseUserPropertyValue(name, m[name])
		if err != nil {
			return nil, fmt.Errorf("user_properties.%s: %w", name, err)
		}
		settings = append(settings, setting)
	}
	return settings, nil
}

func parseUserPropertyValue(name string, v any) (UserPropertySetting, error) {
	if s, isString := v.(string); isString {
		if literal, isStatic := strings.CutPrefix(s, staticPrefix); isStatic {
			return UserPropertySetting{Name: name, Literal: literal}, nil
		}
		if err := checkExpressionSource(s); err != nil {
			return UserPropertySetting{}, err
		}
		return UserPropertySetting{Name: name, Expression: s}, nil
	}
	literal, ok := solaceheaders.FormatValue(v)
	if !ok {
		return UserPropertySetting{}, fmt.Errorf("expected a string, number or boolean (got a %T)", v)
	}
	return UserPropertySetting{Name: name, Literal: literal}, nil
}

func checkExpressionSource(expr string) error {
	_, err := sacexpr.NewContext(nil, "", nil).GetData(rewriteTaskResponseStr(expr))
	if errors.Is(err, sacexpr.ErrUnknownSource) {
		return fmt.Errorf("%w; write a literal string as static:<value>", err)
	}
	return nil
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func outboundMessage(exprCtx *sacexpr.Context, handler *OutputHandlerConfig, payload []byte, logger *slog.Logger) *broker.Message {
	msg := &broker.Message{Payload: payload}
	for _, s := range handler.Headers {
		value, err := headerValue(exprCtx, s)
		if err != nil {
			logger.Warn("output header left unset: its value could not be used",
				"handlerName", handler.Name, "headerName", s.Spec.Name, "error", err)
			continue
		}
		if value != nil {
			setHeader(msg, s.Spec.Name, value)
		}
	}
	for _, p := range handler.UserProperties {
		value, err := userPropertyValue(exprCtx, p)
		if err != nil {
			logger.Warn("output user property left unset: its value could not be used",
				"handlerName", handler.Name, "userPropertyName", p.Name, "error", err)
			continue
		}
		if value == "" {
			continue
		}
		if msg.UserProperties == nil {
			msg.UserProperties = make(map[string]string, len(handler.UserProperties))
		}
		msg.UserProperties[p.Name] = value
	}
	return msg
}

var errValueNotEvaluated = errors.New("expression could not be evaluated")

func headerValue(exprCtx *sacexpr.Context, s HeaderSetting) (any, error) {
	if s.Expression == "" {
		return s.Literal, nil
	}
	raw, err := exprCtx.GetData(rewriteTaskResponseStr(s.Expression))
	if err != nil {
		return nil, errValueNotEvaluated
	}
	if isUnset(raw) {
		return nil, nil
	}
	return s.Spec.Coerce(raw)
}

func userPropertyValue(exprCtx *sacexpr.Context, p UserPropertySetting) (string, error) {
	if p.Expression == "" {
		return p.Literal, nil
	}
	raw, err := exprCtx.GetData(rewriteTaskResponseStr(p.Expression))
	if err != nil {
		return "", errValueNotEvaluated
	}
	if isUnset(raw) {
		return "", nil
	}
	value, ok := solaceheaders.FormatValue(raw)
	if !ok {
		return "", fmt.Errorf("resolved to a %T; a user property must be a single value", raw)
	}
	return value, nil
}

func isUnset(v any) bool {
	if v == nil {
		return true
	}
	s, isString := v.(string)
	return isString && strings.TrimSpace(s) == ""
}

func setHeader(msg *broker.Message, name string, value any) {
	switch name {
	case solaceheaders.TimeToLive:
		msg.TTL = time.Duration(value.(int64)) * time.Millisecond
		return
	case solaceheaders.CorrelationID:
		msg.CorrelationID = value.(string)
		return
	}
	if msg.Headers == nil {
		msg.Headers = &broker.PublishHeaders{}
	}
	h := msg.Headers
	switch name {
	case solaceheaders.DMQEligible:
		b := value.(bool)
		h.DMQEligible = &b
	case solaceheaders.Expiration:
		h.Expiration = time.UnixMilli(value.(int64))
	case solaceheaders.Priority:
		p := value.(int)
		h.Priority = &p
	case solaceheaders.SequenceNumber:
		n := value.(uint64)
		h.SequenceNumber = &n
	case solaceheaders.ApplicationMessageID:
		h.ApplicationMessageID = value.(string)
	case solaceheaders.ApplicationMessageType:
		h.ApplicationMessageType = value.(string)
	case solaceheaders.SenderID:
		h.SenderID = value.(string)
	case solaceheaders.HTTPContentEncoding:
		h.HTTPContentEncoding = value.(string)
	}
}
