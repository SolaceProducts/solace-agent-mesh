// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sqltool

const (
	mssqlListTablesQuery = "SELECT table_name FROM information_schema.tables WHERE table_schema = @p1 AND table_type = 'BASE TABLE' ORDER BY table_name"

	mssqlGetColumnsQuery = "SELECT column_name, data_type, is_nullable FROM information_schema.columns WHERE table_schema = @p1 AND table_name = @p2 ORDER BY ordinal_position"

	mssqlGetPrimaryKeysQuery = `SELECT c.COLUMN_NAME FROM information_schema.table_constraints tc
			JOIN information_schema.constraint_column_usage c ON tc.constraint_name = c.constraint_name
			WHERE tc.table_schema = @p1 AND tc.table_name = @p2 AND tc.constraint_type = 'PRIMARY KEY'
			ORDER BY c.COLUMN_NAME`

	mssqlGetForeignKeysQuery = `SELECT ccu.column_name, kcu2.table_name, kcu2.column_name
			FROM information_schema.referential_constraints rc
			JOIN information_schema.constraint_column_usage ccu ON rc.constraint_name = ccu.constraint_name
			JOIN information_schema.key_column_usage kcu2 ON rc.unique_constraint_name = kcu2.constraint_name
			WHERE ccu.table_schema = @p1 AND ccu.table_name = @p2`

	mssqlGetIndexedColumnsQuery = `SELECT c.name FROM sys.indexes i
			JOIN sys.index_columns ic ON i.object_id = ic.object_id AND i.index_id = ic.index_id
			JOIN sys.columns c ON ic.object_id = c.object_id AND ic.column_id = c.column_id
			WHERE i.object_id = OBJECT_ID(@p1) AND i.is_primary_key = 0`

	mssqlGetRowCountQuery = "SELECT SUM(p.rows) FROM sys.partitions p WHERE p.object_id = OBJECT_ID(@p1) AND p.index_id IN (0, 1)"
)

const (
	oracleListTablesQuery = "SELECT table_name FROM user_tables ORDER BY table_name"

	oracleGetColumnsQuery = "SELECT column_name, data_type, nullable FROM user_tab_columns WHERE table_name = :1 ORDER BY column_id"

	oracleGetPrimaryKeysQuery = `SELECT cols.column_name FROM user_constraints c
			JOIN user_cons_columns cols ON c.constraint_name = cols.constraint_name
			WHERE c.table_name = :1 AND c.constraint_type = 'P'
			ORDER BY cols.position`

	oracleGetForeignKeysQuery = `SELECT a.column_name, c_pk.table_name, b.column_name
			FROM user_cons_columns a
			JOIN user_constraints c ON a.constraint_name = c.constraint_name
			JOIN user_constraints c_pk ON c.r_constraint_name = c_pk.constraint_name
			JOIN user_cons_columns b ON c_pk.constraint_name = b.constraint_name AND a.position = b.position
			WHERE c.constraint_type = 'R' AND a.table_name = :1`

	oracleGetIndexedColumnsQuery = `SELECT column_name FROM user_ind_columns WHERE table_name = :1
			AND index_name NOT IN (SELECT constraint_name FROM user_constraints WHERE constraint_type = 'P' AND table_name = :1)`

	oracleGetRowCountQuery = "SELECT num_rows FROM user_tables WHERE table_name = :1"
)
