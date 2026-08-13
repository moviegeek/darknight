package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// queryTimeout caps how long a single console query may run. The store opens
// SQLite with SetMaxOpenConns(1), so a long query would block every other DB
// access in the app; the timeout bounds that.
const queryTimeout = 30 * time.Second

// tableInfoColumn mirrors one row of SQLite's PRAGMA table_info.
type tableInfoColumn struct {
	CID     int    `json:"cid"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	NotNull int    `json:"notnull"`
	PK      int    `json:"pk"`
}

// tableInfo is one entry in the GET /dev/tables response: a table name plus
// its columns.
type tableInfo struct {
	Name    string            `json:"name"`
	Columns []tableInfoColumn `json:"columns"`
}

// listTables returns every user table in the database with its column layout.
// sqlite_% internal tables are excluded.
func (a *API) listTables(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()

	rows, err := a.Store.DB.QueryContext(ctx,
		`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		names = append(names, n)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	out := make([]tableInfo, 0, len(names))
	for _, n := range names {
		cols, err := tableColumns(ctx, a.Store.DB, n)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, tableInfo{Name: n, Columns: cols})
	}
	writeJSON(w, http.StatusOK, out)
}

// tableColumns runs PRAGMA table_info for one table.
func tableColumns(ctx context.Context, db *sql.DB, table string) ([]tableInfoColumn, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info("`+table+`")`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []tableInfoColumn
	for rows.Next() {
		var c tableInfoColumn
		var dflt sql.NullString
		if err := rows.Scan(&c.CID, &c.Name, &c.Type, &c.NotNull, &dflt, &c.PK); err != nil {
			return nil, err
		}
		cols = append(cols, c)
	}
	return cols, rows.Err()
}

// execSQLReq is the body of POST /dev/sql.
type execSQLReq struct {
	SQL   string `json:"sql"`
	Write bool   `json:"write"`
}

// sqlResult is the response for POST /dev/sql. For query statements, Columns
// and Rows are populated; for exec statements, RowsAffected / LastInsertID are
// populated. DurationMs is always set.
type sqlResult struct {
	Columns      []string   `json:"columns"`
	Rows         [][]any    `json:"rows"`
	RowsAffected int64      `json:"rows_affected"`
	LastInsertID int64      `json:"last_insert_id,omitempty"`
	DurationMs   int64      `json:"duration_ms"`
}

// execSQL runs a single SQL statement. When write is false, only read-only
// statements (SELECT/WITH/PRAGMA/EXPLAIN) are permitted; anything else is
// rejected with 403. Only the first statement in the input is executed so a
// multi-statement string cannot smuggle in a second write.
func (a *API) execSQL(w http.ResponseWriter, r *http.Request) {
	var req execSQLReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if strings.TrimSpace(req.SQL) == "" {
		writeError(w, http.StatusBadRequest, "sql is required")
		return
	}

	keyword := firstKeyword(req.SQL)
	if !req.Write && !isReadOnlyKeyword(keyword) {
		writeError(w, http.StatusForbidden,
			"write mode is off: only SELECT/WITH/PRAGMA/EXPLAIN are allowed")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()

	start := time.Now()
	if isQueryKeyword(keyword) {
		runQuery(ctx, a.Store.DB, req.SQL, w, start)
		return
	}
	runExec(ctx, a.Store.DB, req.SQL, w, start)
}

// runQuery executes a statement that returns rows, streaming them into a
// generic [][]any result.
func runQuery(ctx context.Context, db *sql.DB, query string, w http.ResponseWriter, start time.Time) {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	defer rows.Close()

	types, err := rows.ColumnTypes()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cols := make([]string, len(types))
	for i, t := range types {
		cols[i] = t.Name()
	}

	var out [][]any
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		// Normalize sql.Null* values to their underlying value (or null),
		// otherwise they'd marshal as objects.
		for i, v := range vals {
			vals[i] = normalizeScanValue(v)
		}
		out = append(out, vals)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if out == nil {
		out = [][]any{}
	}
	writeJSON(w, http.StatusOK, sqlResult{
		Columns:    cols,
		Rows:       out,
		DurationMs: time.Since(start).Milliseconds(),
	})
}

// runExec executes a statement that does not return rows.
func runExec(ctx context.Context, db *sql.DB, query string, w http.ResponseWriter, start time.Time) {
	res, err := db.ExecContext(ctx, query)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	affected, _ := res.RowsAffected()
	lastID, _ := res.LastInsertId()
	writeJSON(w, http.StatusOK, sqlResult{
		RowsAffected: affected,
		LastInsertID: lastID,
		DurationMs:   time.Since(start).Milliseconds(),
	})
}

// firstKeyword returns the uppercased first keyword of the SQL text, skipping
// leading whitespace, line/block comments, and "--" comments. This decides
// read-only vs write classification and query-vs-exec dispatch.
func firstKeyword(s string) string {
	i := 0
	for i < len(s) {
		// skip whitespace
		if s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r' {
			i++
			continue
		}
		// skip line comments "-- ... \n"
		if i+1 < len(s) && s[i] == '-' && s[i+1] == '-' {
			for i < len(s) && s[i] != '\n' {
				i++
			}
			continue
		}
		// skip block comments "/* ... */"
		if i+1 < len(s) && s[i] == '/' && s[i+1] == '*' {
			i += 2
			for i+1 < len(s) && !(s[i] == '*' && s[i+1] == '/') {
				i++
			}
			i += 2
			continue
		}
		break
	}
	// read the keyword: alphanumerics and underscores
	start := i
	for i < len(s) {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' {
			i++
		} else {
			break
		}
	}
	return strings.ToUpper(s[start:i])
}

// isReadOnlyKeyword reports whether a statement starting with keyword is safe
// in read-only mode.
func isReadOnlyKeyword(keyword string) bool {
	switch keyword {
	case "SELECT", "WITH", "PRAGMA", "EXPLAIN":
		return true
	}
	return false
}

// isQueryKeyword reports whether a statement should be run via Query (returns
// rows) rather than Exec.
func isQueryKeyword(keyword string) bool {
	switch keyword {
	case "SELECT", "WITH", "PRAGMA", "EXPLAIN":
		return true
	}
	return false
}

// normalizeScanValue unwraps common database/sql Null types so they marshal as
// plain JSON values (null for NULL, the value otherwise). Non-Null values pass
// through unchanged.
func normalizeScanValue(v any) any {
	switch n := v.(type) {
	case nil:
		return nil
	case sql.NullString:
		if n.Valid {
			return n.String
		}
		return nil
	case sql.NullInt64:
		if n.Valid {
			return n.Int64
		}
		return nil
	case sql.NullFloat64:
		if n.Valid {
			return n.Float64
		}
		return nil
	case sql.NullBool:
		if n.Valid {
			return n.Bool
		}
		return nil
	case sql.NullTime:
		if n.Valid {
			return n.Time
		}
		return nil
	case []byte:
		// modernc/sqlite returns TEXT as []byte; encode as string.
		return string(n)
	default:
		return v
	}
}
