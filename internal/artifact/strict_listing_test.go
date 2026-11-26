// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package artifact

import (
	"context"
	"errors"
	"slices"
	"testing"
)

type lenientLister struct {
	Service
	keys []string
}

func (l lenientLister) ListKeys(context.Context, KeyScope) ([]string, error) { return l.keys, nil }

type strictLister struct {
	lenientLister
	strictKeys []string
	err        error
}

func (l strictLister) ListKeysStrict(context.Context, KeyScope) ([]string, error) {
	return l.strictKeys, l.err
}

type decorator struct {
	Service
	inner Service
}

func (d decorator) Inner() Service { return d.inner }

func wrappedInDecorators(depth int, base Service) Service {
	svc := base
	for range depth {
		svc = decorator{Service: lenientLister{keys: []string{"lenient.txt"}}, inner: svc}
	}
	return svc
}

type selfWrapping struct {
	lenientLister
}

func (s selfWrapping) Inner() Service { return s }

func TestListKeysStrict(t *testing.T) {
	errUnreadable := errors.New("unreadable")
	strict := strictLister{lenientLister: lenientLister{keys: []string{"lenient.txt"}}, strictKeys: []string{"strict.txt"}}
	failing := strictLister{lenientLister: lenientLister{keys: []string{"lenient.txt"}}, err: errUnreadable}
	tests := map[string]struct {
		svc      Service
		wantKeys []string
		wantErr  error
	}{
		"a store without a strict listing lists as it always did":     {lenientLister{keys: []string{"lenient.txt"}}, []string{"lenient.txt"}, nil},
		"a store with a strict listing uses it":                       {strict, []string{"strict.txt"}, nil},
		"a strict listing's error is returned":                        {failing, nil, errUnreadable},
		"a decorated strict store is found through its wrappers":      {decorator{inner: decorator{inner: strict}}, []string{"strict.txt"}, nil},
		"a strict store under fifteen decorators is still found":      {wrappedInDecorators(15, strict), []string{"strict.txt"}, nil},
		"a strict store under sixteen decorators is not searched for": {wrappedInDecorators(16, strict), []string{"lenient.txt"}, nil},
		"a decorator that wraps itself ends in the plain listing":     {selfWrapping{lenientLister{keys: []string{"lenient.txt"}}}, []string{"lenient.txt"}, nil},
		"a decorator over a plain store lists through itself":         {decorator{Service: lenientLister{keys: []string{"lenient.txt"}}, inner: lenientLister{keys: []string{"other.txt"}}}, []string{"lenient.txt"}, nil},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			keys, err := ListKeysStrict(t.Context(), tc.svc, KeyScope{})

			if !errors.Is(err, tc.wantErr) || !slices.Equal(keys, tc.wantKeys) {
				t.Fatalf("ListKeysStrict = %v, %v, want %v, %v", keys, err, tc.wantKeys, tc.wantErr)
			}
		})
	}
}
