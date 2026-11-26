// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package dbutil

import "testing"

func TestTranslatePostgres(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`SELECT id::text FROM t`, `SELECT CAST(id AS TEXT) FROM t`},
		{`SELECT e.id::text AS id`, `SELECT CAST(e.id AS TEXT) AS id`},
		{`WHERE id = ?::uuid`, `WHERE id = CAST(? AS TEXT)`},
		{`SELECT count(*)::float / 2`, `SELECT CAST(count(*) AS REAL) / 2`},
		{`SELECT (a + b)::bigint`, `SELECT CAST((a + b) AS INTEGER)`},
		{`SELECT coalesce(sum(x), 0)::numeric(20,10)`, `SELECT CAST(coalesce(sum(x), 0) AS REAL)`},
		{`SELECT '{}'::jsonb`, `SELECT '{}'`},
		{`SELECT 'a::text' FROM t`, `SELECT 'a::text' FROM t`},
		{`SELECT NULL::text AS h`, `SELECT CAST(NULL AS TEXT) AS h`},
		{`WHERE a IS DISTINCT FROM b AND c is not distinct from d`, `WHERE a IS NOT b AND c IS d`},
		{`SELECT char_length(x), GREATEST(a, b), least(a, b)`, `SELECT length(x), max(a, b), min(a, b)`},
		{`SELECT x FROM t WHERE a = 1 FOR UPDATE SKIP LOCKED`, `SELECT x FROM t WHERE a = 1`},
		{`SELECT x FROM t FOR UPDATE`, `SELECT x FROM t`},
		{`SELECT ?::text[]`, `SELECT ?::text[]`},
		{`SELECT to_regclass(?)::text`, `SELECT CAST(to_regclass(?) AS TEXT)`},
		{`SELECT my_greatest(a)`, `SELECT my_greatest(a)`},
	} {
		if got := translatePostgres(tc.in); got != tc.want {
			t.Errorf("translatePostgres(%q)\n got %q\nwant %q", tc.in, got, tc.want)
		}
	}
}
