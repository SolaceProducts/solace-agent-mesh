// Package closureroute is a fixture for the route-marker conformance walk. It
// is not compiled into the binary — the go tool ignores testdata — and exists
// only so the analyzer can be pointed at a registration shape the real tree
// does not contain.
//
// Every route in internal/entrypoint/httpsse wires a method value or a named
// function, so resolveChain's fall-through to nil, and the unreadable finding
// routes() raises from it, never execute in a normal run. A regression there
// would stay invisible until somebody wired the first closure handler, at which
// point the analyzer would report that route as conforming without having read
// it. This package is that first closure handler.
package closureroute

import "net/http"

const (
	publicReasonOwnerScoped = "handler enforces per-user ownership"
	publicReasonNoUserData  = "no caller data in the response"
)

type RouteRegistration struct {
	Method   string
	Path     string
	Scope    string
	IsPublic string
}

type adapter struct{}

func (a *adapter) wireRoute(mux *http.ServeMux, reg RouteRegistration, h http.HandlerFunc) {
	mux.HandleFunc(reg.Method+" "+reg.Path, h)
}

func (a *adapter) handleNamed(w http.ResponseWriter, r *http.Request) {}

func (a *adapter) register(mux *http.ServeMux) {
	// A closure the walk cannot resolve to a declared function. The marker
	// claims per-user ownership, so silently skipping it would report an
	// unverified claim as verified.
	a.wireRoute(mux, RouteRegistration{
		Method:   "GET",
		Path:     "/fixture/closure",
		IsPublic: publicReasonOwnerScoped,
	}, func(w http.ResponseWriter, r *http.Request) {})

	// The same shape carrying a marker that asserts the ABSENCE of caller
	// data. This is the direction that fails quietly: an empty chain reaches
	// no resolver, which is exactly what this marker wants to be true, so
	// without the unreadable finding it would read as a pass.
	a.wireRoute(mux, RouteRegistration{
		Method:   "GET",
		Path:     "/fixture/closure-nouserdata",
		IsPublic: publicReasonNoUserData,
	}, func(w http.ResponseWriter, r *http.Request) {})

	// The control: a named method value, which must resolve normally and stay
	// out of the unreadable set.
	a.wireRoute(mux, RouteRegistration{
		Method:   "GET",
		Path:     "/fixture/named",
		IsPublic: publicReasonOwnerScoped,
	}, a.handleNamed)

	// wireRoute taken as a value. The call below has a bare identifier for its
	// Fun, so the registration scan cannot match it and the route is invisible
	// — the shape that used to be skipped in silence.
	register := a.wireRoute
	register(mux, RouteRegistration{
		Method:   "GET",
		Path:     "/fixture/via-method-value",
		IsPublic: publicReasonOwnerScoped,
	}, a.handleNamed)
}
