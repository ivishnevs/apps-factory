package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
)

// DB is Core's Postgres database (pgx through database/sql). Queries keep `?` placeholders; DB and Tx number
// them ($1, $2, ...) before Postgres sees them, and pass Go booleans as the 0/1 integers the flag columns hold.
type DB struct{ *sql.DB }

func (db *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return db.DB.ExecContext(ctx, rebind(query), params(args)...)
}
func (db *DB) Exec(query string, args ...any) (sql.Result, error) {
	return db.ExecContext(context.Background(), query, args...)
}
func (db *DB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return db.DB.QueryContext(ctx, rebind(query), params(args)...)
}
func (db *DB) Query(query string, args ...any) (*sql.Rows, error) {
	return db.QueryContext(context.Background(), query, args...)
}
func (db *DB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return db.DB.QueryRowContext(ctx, rebind(query), params(args)...)
}
func (db *DB) QueryRow(query string, args ...any) *sql.Row {
	return db.QueryRowContext(context.Background(), query, args...)
}

// Tx is a transaction bound to one company. Helpers write tx.company into every row they insert and scope
// every lookup by a caller-supplied ID with it. Its queries use `?` placeholders like DB's.
type Tx struct {
	*sql.Tx
	company string
	store   *Store
}

func (tx *Tx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return tx.Tx.ExecContext(ctx, rebind(query), params(args)...)
}
func (tx *Tx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return tx.Tx.QueryContext(ctx, rebind(query), params(args)...)
}
func (tx *Tx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return tx.Tx.QueryRowContext(ctx, rebind(query), params(args)...)
}

// DatabaseVersion names the database server ("postgres 17.6"); it fails when the database is unreachable.
func (s *Store) DatabaseVersion(ctx context.Context) (string, error) {
	var v string
	err := s.DB.QueryRowContext(ctx, "SHOW server_version").Scan(&v)
	return "postgres " + v, err
}

// Advisory lock classes (the first key of pg_advisory_lock(int, int)).
const (
	lockMigrate = 0x46414354 // "FACT": schema migrations, one Core at a time
	lockCompany = 0x46414355 // per company: transactions that read state and decide on it (D4)
)

// lockCompanyTx serializes the transaction with every other deciding transaction of the same company until
// it ends; other companies never wait for it.
func lockCompanyTx(ctx context.Context, tx *sql.Tx, company string) error {
	_, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1, hashtext($2))", lockCompany, company)
	return err
}

var rebound sync.Map // query → numbered query

// rebind numbers the `?` placeholders of query as $1, $2, ... outside string literals, quoted identifiers and
// comments.
func rebind(query string) string {
	if v, ok := rebound.Load(query); ok {
		return v.(string)
	}
	if !strings.Contains(query, "?") {
		return query
	}
	var b strings.Builder
	b.Grow(len(query) + 16)
	n := 0
	for i := 0; i < len(query); i++ {
		c := query[i]
		switch {
		case c == '\'' || c == '"':
			j := i + 1
			for j < len(query) {
				if query[j] == c {
					if j+1 < len(query) && query[j+1] == c { // doubled quote
						j += 2
						continue
					}
					break
				}
				j++
			}
			b.WriteString(query[i:min(j+1, len(query))])
			i = j
		case c == '-' && i+1 < len(query) && query[i+1] == '-':
			j := strings.IndexByte(query[i:], '\n')
			if j < 0 {
				j = len(query) - i
			}
			b.WriteString(query[i : i+j])
			i += j - 1
		case c == '?':
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
		default:
			b.WriteByte(c)
		}
	}
	out := b.String()
	rebound.Store(query, out)
	return out
}

// params passes booleans as the 0/1 integers of the flag columns.
func params(args []any) []any {
	var out []any // a copy: the caller's slice is never written
	for i, a := range args {
		if v, ok := a.(bool); ok {
			if out == nil {
				out = append([]any(nil), args...)
			}
			out[i] = boolInt(v)
		}
	}
	if out == nil {
		return args
	}
	return out
}

func boolInt(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

// constraint maps an integrity violation (SQLSTATE class 23: unique, foreign key, check, not null) to
// ErrConflict, the 409 of the API.
func constraint(err error) error {
	if isConstraint(err) {
		return fmt.Errorf("%w: %v", ErrConflict, err)
	}
	return err
}

func isConstraint(err error) bool {
	var e *pgconn.PgError
	return errors.As(err, &e) && strings.HasPrefix(e.Code, "23")
}

// pgCode is the SQLSTATE of a Postgres error, empty for any other error.
func pgCode(err error) string {
	var e *pgconn.PgError
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}
