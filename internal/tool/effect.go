// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tool

import (
	"fmt"
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
)

type Effect string

const (
	EffectReadOnly      Effect = remotetool.EffectReadOnly
	EffectSideEffecting Effect = remotetool.EffectSideEffecting
)

type EffectSource string

const (
	EffectSourceOperator EffectSource = "operator"
	EffectSourceDeclared EffectSource = "declared"
	EffectSourceDerived  EffectSource = "derived"
	EffectSourceHint     EffectSource = "hint"

	EffectSourceUntrustedHint EffectSource = "untrusted_hint"
	EffectSourceDefault       EffectSource = "default"
)

var effectSourcePrecedence = []EffectSource{
	EffectSourceOperator,
	EffectSourceDeclared,
	EffectSourceDerived,
	EffectSourceHint,
	EffectSourceUntrustedHint,
	EffectSourceDefault,
}

type EffectClass struct {
	Effect Effect
	Source EffectSource
}

var DefaultEffectClass = EffectClass{Effect: EffectSideEffecting, Source: EffectSourceDefault}

func DeclaredEffect(e Effect) EffectClass {
	return EffectClass{Effect: e, Source: EffectSourceDeclared}
}

func DerivedEffect(e Effect) EffectClass { return EffectClass{Effect: e, Source: EffectSourceDerived} }

func HintedEffect(e Effect) EffectClass { return EffectClass{Effect: e, Source: EffectSourceHint} }

func UntrustedHintEffect(e Effect) EffectClass {
	return EffectClass{Effect: e, Source: EffectSourceUntrustedHint}
}

func (c EffectClass) ReadOnly() bool {
	return c.valid() && c.Effect == EffectReadOnly
}

func (c EffectClass) valid() bool {
	return c.Effect.Valid() && slices.Contains(effectSourcePrecedence, c.Source)
}

func (e Effect) Valid() bool {
	return remotetool.ValidEffect(string(e))
}

func ParseEffect(s string) (Effect, error) {
	if e := Effect(s); e.Valid() {
		return e, nil
	}
	return "", fmt.Errorf("effect %q is not one of %q, %q", s, EffectReadOnly, EffectSideEffecting)
}

func ResolveEffect(candidates ...EffectClass) EffectClass {
	best := DefaultEffectClass
	bestRank := len(effectSourcePrecedence)
	for _, c := range candidates {
		if !c.valid() {
			continue
		}
		if rank := slices.Index(effectSourcePrecedence, c.Source); rank < bestRank {
			best, bestRank = c, rank
		}
	}
	return best
}

type EffectClassifier interface {
	EffectClass() EffectClass
}

type CallEffectClassifier interface {
	CallEffectClass(args map[string]any) EffectClass
}

func EffectClassOf(t Tool) EffectClass {
	if c, ok := t.(EffectClassifier); ok {
		return ResolveEffect(c.EffectClass())
	}
	return DefaultEffectClass
}

func CallEffectClassOf(t Tool, args map[string]any) EffectClass {
	if c, ok := t.(CallEffectClassifier); ok {
		if perCall := c.CallEffectClass(args); perCall.valid() {
			return perCall
		}
	}
	return EffectClassOf(t)
}

type ReadOnlyTool struct{}

func (ReadOnlyTool) EffectClass() EffectClass { return DeclaredEffect(EffectReadOnly) }

type SideEffectingTool struct{}

func (SideEffectingTool) EffectClass() EffectClass { return DeclaredEffect(EffectSideEffecting) }
