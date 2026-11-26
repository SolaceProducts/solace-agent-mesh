// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package doctor

type Context string

const (
	ContextLocal Context = "local"
	ContextWheel Context = "wheel"
	ContextHelm  Context = "helm"
)

func (c Context) IsValid() bool {
	return c == ContextLocal || c == ContextWheel || c == ContextHelm
}

func (c Context) IsStrict() bool {
	return c == ContextWheel || c == ContextHelm
}

func DetectContext(env Env) (Context, bool) {
	c := Context(env.DoctorContext)
	if c.IsValid() {
		return c, true
	}
	return "", false
}
