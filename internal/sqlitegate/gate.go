// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sqlitegate

import (
	"encoding/binary"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"modernc.org/libc"
	sqlite3 "modernc.org/sqlite/lib"
)

func CheckSingleReadOnlySelect(definitions []string, query string) error {
	scratch, err := openScratch()
	if err != nil {
		return err
	}
	defer scratch.close()

	tableNames := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		name, err := scratch.define(definition)
		if err != nil {
			return err
		}
		tableNames = append(tableNames, name)
	}

	reads := newReadPolicy(tableNames)
	if err := scratch.requireSingleReadOnlySelect(query, reads); err != nil {
		return err
	}
	for _, name := range reads.namesToResolve {
		if scratch.hasSchemaObject(name) {
			return &Refusal{Reason: ReasonDeniedAction, Action: sqlite3.SQLITE_READ}
		}
	}
	return nil
}

const pointerBytes = 4 << (^uintptr(0) >> 63)

type scratch struct {
	tls     *libc.TLS
	db      uintptr
	stmtOut uintptr
	tailOut uintptr
}

func openScratch() (*scratch, error) {
	s := &scratch{tls: libc.NewTLS()}
	if err := s.open(); err != nil {
		s.close()
		return nil, err
	}
	return s, nil
}

func (s *scratch) open() error {
	s.stmtOut = libc.Xmalloc(s.tls, pointerBytes)
	s.tailOut = libc.Xmalloc(s.tls, pointerBytes)
	dbOut := libc.Xmalloc(s.tls, pointerBytes)
	defer libc.Xfree(s.tls, dbOut)
	if s.stmtOut == 0 || s.tailOut == 0 || dbOut == 0 {
		return &InternalError{Stage: StageOpen, ResultCode: sqlite3.SQLITE_NOMEM}
	}
	name, err := libc.CString(":memory:")
	if err != nil {
		return &InternalError{Stage: StageOpen, ResultCode: sqlite3.SQLITE_NOMEM}
	}
	defer libc.Xfree(s.tls, name)

	rc := sqlite3.Xsqlite3_open_v2(s.tls, name, dbOut,
		sqlite3.SQLITE_OPEN_READWRITE|sqlite3.SQLITE_OPEN_CREATE|sqlite3.SQLITE_OPEN_MEMORY, 0)
	s.db = pointerAt(dbOut)
	if rc != sqlite3.SQLITE_OK {
		return &InternalError{Stage: StageOpen, ResultCode: rc}
	}
	sqlite3.Xsqlite3_extended_result_codes(s.tls, s.db, 1)
	return nil
}

func (s *scratch) close() {
	if s.db != 0 {
		sqlite3.Xsqlite3_close_v2(s.tls, s.db)
	}
	libc.Xfree(s.tls, s.stmtOut)
	libc.Xfree(s.tls, s.tailOut)
	s.tls.Close()
}

func (s *scratch) define(definition string) (tableName string, err error) {
	text, err := libc.CString(definition)
	if err != nil {
		return "", &InternalError{Stage: StageDefine, ResultCode: sqlite3.SQLITE_NOMEM}
	}
	defer libc.Xfree(s.tls, text)

	created := &tableDefinition{}
	defining := &authorization{allows: created.allows}
	defer s.authorizeWith(defining)()

	stmt, tail, rc := s.prepare(text)
	if refusedWithoutARule(rc, defining) {
		return "", &InternalError{Stage: StageAuthorize, ResultCode: rc}
	}
	if rc != sqlite3.SQLITE_OK {
		return "", &InternalError{Stage: StageDefine, ResultCode: rc}
	}
	if stmt == 0 {
		return "", &InternalError{Stage: StageDefine}
	}
	if onePlainCreateTable := atEndOfText(tail) && len(created.names) == 1; !onePlainCreateTable {
		sqlite3.Xsqlite3_finalize(s.tls, stmt)
		return "", &InternalError{Stage: StageDefine}
	}
	stepRC := sqlite3.Xsqlite3_step(s.tls, stmt)
	sqlite3.Xsqlite3_finalize(s.tls, stmt)
	if stepRC != sqlite3.SQLITE_DONE {
		return "", &InternalError{Stage: StageDefine, ResultCode: stepRC}
	}
	return created.names[0], nil
}

func refusedWithoutARule(rc int32, a *authorization) bool {
	return rc&0xff == sqlite3.SQLITE_AUTH && !a.denied
}

func (s *scratch) prepare(sql uintptr) (stmt, tail uintptr, rc int32) {
	rc = sqlite3.Xsqlite3_prepare_v2(s.tls, s.db, sql, -1, s.stmtOut, s.tailOut)
	return pointerAt(s.stmtOut), pointerAt(s.tailOut), rc
}

func (s *scratch) requireSingleReadOnlySelect(query string, reads *readPolicy) error {
	reading := &authorization{allows: reads.allows}
	defer s.authorizeWith(reading)()
	return s.requireSingleReadOnlySelectUnder(query, reading)
}

func (s *scratch) requireSingleReadOnlySelectUnder(query string, reading *authorization) error {
	text, err := libc.CString(query)
	if err != nil {
		return &InternalError{Stage: StageCompile, ResultCode: sqlite3.SQLITE_NOMEM}
	}
	defer libc.Xfree(s.tls, text)

	admitted := false
	for rest := text; !atEndOfText(rest); {
		stmt, tail, rc := s.prepare(rest)
		if reading.denied {
			return &Refusal{Reason: ReasonDeniedAction, Action: reading.deniedAction}
		}
		if refusedWithoutARule(rc, reading) {
			return &InternalError{Stage: StageAuthorize, ResultCode: rc}
		}
		if rc != sqlite3.SQLITE_OK {
			return s.compileError(rc)
		}
		if stmt != 0 {
			readOnlySelect := s.isReadOnlySelect(stmt)
			sqlite3.Xsqlite3_finalize(s.tls, stmt)
			if admitted {
				return &Refusal{Reason: ReasonMultipleStatements}
			}
			if !readOnlySelect {
				return &Refusal{Reason: ReasonNotReadOnlySelect}
			}
			admitted = true
		}
		if tail == rest {
			return &InternalError{Stage: StageCompile}
		}
		rest = tail
	}
	if !admitted {
		return &Refusal{Reason: ReasonNoStatement}
	}
	return nil
}

func (s *scratch) isReadOnlySelect(stmt uintptr) bool {
	return sqlite3.Xsqlite3_stmt_readonly(s.tls, stmt) != 0 &&
		sqlite3.Xsqlite3_column_count(s.tls, stmt) > 0 &&
		sqlite3.Xsqlite3_stmt_isexplain(s.tls, stmt) == 0
}

func (s *scratch) compileError(rc int32) error {
	summary := libc.GoString(sqlite3.Xsqlite3_errstr(s.tls, rc))
	detail := libc.GoString(sqlite3.Xsqlite3_errmsg(s.tls, s.db))
	if detail == summary {
		return &CompileError{message: fmt.Sprintf("%s (%d)", summary, rc)}
	}
	return &CompileError{message: fmt.Sprintf("%s: %s (%d)", summary, detail, rc)}
}

func (s *scratch) hasSchemaObject(name string) bool {
	text, err := libc.CString(`SELECT 1 FROM "` + strings.ReplaceAll(name, `"`, `""`) + `"`)
	if err != nil {
		return true
	}
	defer libc.Xfree(s.tls, text)

	stmt, _, rc := s.prepare(text)
	if stmt != 0 {
		sqlite3.Xsqlite3_finalize(s.tls, stmt)
	}
	unknown := rc == sqlite3.SQLITE_ERROR &&
		strings.HasPrefix(libc.GoString(sqlite3.Xsqlite3_errmsg(s.tls, s.db)), "no such table")
	return !unknown
}

func (s *scratch) authorizeWith(a *authorization) (withdraw func()) {
	id := lastAuthorizationID.Add(1)
	authorizations.Store(id, a)
	sqlite3.Xsqlite3_set_authorizer(s.tls, s.db, authorizerEntry, id)
	return func() {
		sqlite3.Xsqlite3_set_authorizer(s.tls, s.db, 0, 0)
		authorizations.Delete(id)
	}
}

type authorization struct {
	allows       func(action int32, first, second, database string) bool
	denied       bool
	deniedAction int32
}

var (
	authorizations      sync.Map
	lastAuthorizationID atomic.Uintptr
)

type authorizer = func(tls *libc.TLS, authorizationID uintptr, action int32, first, second, database, trigger uintptr) int32

var authorizerEntry = *(*uintptr)(unsafe.Pointer(&struct{ f authorizer }{authorize}))

func authorize(_ *libc.TLS, authorizationID uintptr, action int32, first, second, database, _ uintptr) int32 {
	registered, found := authorizations.Load(authorizationID)
	if !found {
		return sqlite3.SQLITE_DENY
	}
	a := registered.(*authorization)
	if a.allows(action, libc.GoString(first), libc.GoString(second), libc.GoString(database)) {
		return sqlite3.SQLITE_OK
	}
	if !a.denied {
		a.denied, a.deniedAction = true, action
	}
	return sqlite3.SQLITE_DENY
}

func pointerAt(address uintptr) uintptr {
	word := libc.GoBytes(address, pointerBytes)
	if pointerBytes == 8 {
		return uintptr(binary.NativeEndian.Uint64(word))
	}
	return uintptr(binary.NativeEndian.Uint32(word))
}

func atEndOfText(text uintptr) bool {
	return libc.GoBytes(text, 1)[0] == 0
}
