// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sacexpr

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var ErrUnknownSource = errors.New("unknown expression source")

type Context struct {
	payload        any
	topic          string
	topicLevels    []string
	userProperties map[string]string
	userData       map[string]any
	iterationItem  any

	keywordArgs map[string]any
}

func NewContext(payload any, topic string, userProperties map[string]string) *Context {
	props := userProperties
	if props == nil {
		props = make(map[string]string)
	}

	var levels []string
	if topic != "" {
		levels = strings.Split(topic, "/")
	}

	return &Context{
		payload:        payload,
		topic:          topic,
		topicLevels:    levels,
		userProperties: props,
		userData:       make(map[string]any),
	}
}

func (c *Context) GetData(expression string) (any, error) {
	parsed := parseExpression(expression)

	switch parsed.source {
	case sourceInputPayload:
		return getPath(c.payload, parsed.path)

	case sourceInputTopic:
		if parsed.path == "" {
			return c.topic, nil
		}
		return getPath(c.topic, parsed.path)

	case sourceInputTopicLvls:
		if parsed.path == "" {
			return c.topicLevels, nil
		}
		idx, err := strconv.Atoi(parsed.path)
		if err != nil {
			return nil, fmt.Errorf("topic_levels index must be integer, got %q", parsed.path)
		}
		resolvedIdx := resolveIndex(idx, len(c.topicLevels))
		if resolvedIdx < 0 || resolvedIdx >= len(c.topicLevels) {
			return nil, nil
		}
		return c.topicLevels[resolvedIdx], nil

	case sourceInputUserProps:
		if parsed.path == "" {
			return c.userProperties, nil
		}
		val, ok := c.userProperties[parsed.path]
		if !ok {
			return nil, nil
		}
		return val, nil

	case sourceUserData:
		store, ok := c.userData[parsed.dataName]
		if !ok {
			return nil, nil
		}
		if parsed.path == "" {
			return store, nil
		}
		return getPath(store, parsed.path)

	case sourceStatic:
		return parsed.path, nil

	case sourceListItem:
		if c.iterationItem == nil {
			return nil, nil
		}
		if parsed.path == "" {
			return c.iterationItem, nil
		}
		return getPath(c.iterationItem, parsed.path)

	case sourceTemplate:
		return evaluateTemplate(c, parsed.path, 0)

	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownSource, expression)
	}
}

func (c *Context) SetData(expression string, value any) error {
	parsed := parseExpression(expression)

	switch parsed.source {
	case sourceInputPayload:
		if parsed.path == "" {
			c.payload = value
			return nil
		}
		newRoot, err := setPath(c.payload, parsed.path, value)
		if err != nil {
			return fmt.Errorf("set %q: %w", expression, err)
		}
		c.payload = newRoot
		return nil

	case sourceUserData:
		if parsed.path == "" {
			c.userData[parsed.dataName] = value
			return nil
		}
		store := c.userData[parsed.dataName]
		newRoot, err := setPath(store, parsed.path, value)
		if err != nil {
			return fmt.Errorf("set %q: %w", expression, err)
		}
		c.userData[parsed.dataName] = newRoot
		return nil

	default:
		return fmt.Errorf("cannot SetData on expression source %q", expression)
	}
}

func (c *Context) SetIterationItem(item any) {
	c.iterationItem = item
}

func (c *Context) SetKeywordArgs(args map[string]any) {
	c.keywordArgs = args
}

func (c *Context) ClearKeywordArgs() {
	c.keywordArgs = nil
}

func (c *Context) GetKeywordArg(name string) (any, bool) {
	if c.keywordArgs == nil {
		return nil, false
	}
	v, ok := c.keywordArgs[name]
	return v, ok
}

func (c *Context) Payload() any {
	return c.payload
}

func (c *Context) Clone() *Context {
	ud := make(map[string]any, len(c.userData))
	for k, v := range c.userData {
		if m, ok := v.(map[string]any); ok {
			cloned := make(map[string]any, len(m))
			for mk, mv := range m {
				cloned[mk] = mv
			}
			ud[k] = cloned
		} else {
			ud[k] = v
		}
	}
	return &Context{
		payload:        c.payload,
		topic:          c.topic,
		topicLevels:    c.topicLevels,
		userProperties: c.userProperties,
		userData:       ud,
		iterationItem:  c.iterationItem,
		keywordArgs:    c.keywordArgs,
	}
}
