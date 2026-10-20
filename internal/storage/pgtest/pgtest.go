// Package pgtest gives tests disposable Postgres databases on the server named by FACTORY_TEST_DATABASE_URL.
// Each test gets its own database, cloned from a template database that is built once per schema (across
// test processes) and kept on the server, so a test pays for a CREATE DATABASE, not for the migrations.
package pgtest

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// Env names the URL of the test server (any database on it, as a user that may create databases); unset, the
// tests that need Postgres are skipped.
const Env = "FACTORY_TEST_DATABASE_URL"

var (
	adminOnce sync.Once
	admin     *sql.DB
	adminErr  error
	built     sync.Map // template name → struct{}
)

func server(t testing.TB) (*sql.DB, string) {
	t.Helper()
	base := os.Getenv(Env)
	if base == "" {
		t.Skip(Env + " not set: Postgres tests skipped (see docs/development.md)")
	}
	adminOnce.Do(func() {
		admin, adminErr = sql.Open("pgx", base)
		if adminErr == nil {
			admin.SetMaxOpenConns(4)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			adminErr = admin.PingContext(ctx)
		}
	})
	if adminErr != nil {
		t.Fatalf("%s: %v", Env, adminErr)
	}
	return admin, base
}

// URLFor is the server URL with database name.
func URLFor(base, name string) string {
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	u.Path = "/" + name
	return u.String()
}

// Empty creates an empty database for t and returns its URL; it is dropped when t ends.
func Empty(t testing.TB) string {
	t.Helper()
	return create(t, "template0")
}

// FromTemplate creates a database for t cloned from the template database ft_<key> and returns its URL; it is
// dropped when t ends. The template is built once with prepare (given its URL; prepare must close its
// connections), under an advisory lock shared by every test process.
func FromTemplate(t testing.TB, key string, prepare func(url string) error) string {
	t.Helper()
	db, base := server(t)
	template := "ft_" + key
	if _, ok := built.Load(template); !ok {
		if err := buildTemplate(db, base, template, prepare); err != nil {
			t.Fatalf("test template %s: %v", template, err)
		}
		built.Store(template, struct{}{})
	}
	return create(t, template)
}

func buildTemplate(db *sql.DB, base, template string, prepare func(string) error) error {
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "SELECT pg_advisory_lock(hashtext('appfactory test templates'))"); err != nil {
		return err
	}
	defer conn.ExecContext(ctx, "SELECT pg_advisory_unlock(hashtext('appfactory test templates'))")
	var n int
	if err = conn.QueryRowContext(ctx, "SELECT count(*) FROM pg_database WHERE datname=$1", template).Scan(&n); err != nil || n > 0 {
		return err
	}
	building := template + "_build"
	if _, err = conn.ExecContext(ctx, `DROP DATABASE IF EXISTS "`+building+`" WITH (FORCE)`); err != nil {
		return err
	}
	if _, err = conn.ExecContext(ctx, `CREATE DATABASE "`+building+`" TEMPLATE template0`); err != nil {
		return err
	}
	if err = prepare(URLFor(base, building)); err != nil {
		return err
	}
	// Renaming needs the database without sessions: a closed pool's backends may still be ending.
	return retryInUse(func() error {
		_, err := conn.ExecContext(ctx, "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1 AND pid<>pg_backend_pid()", building)
		if err == nil {
			_, err = conn.ExecContext(ctx, `ALTER DATABASE "`+building+`" RENAME TO "`+template+`"`)
		}
		return err
	})
}

func create(t testing.TB, template string) string {
	t.Helper()
	db, base := server(t)
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	name := "ft_t_" + hex.EncodeToString(b)
	if err := retryInUse(func() error {
		_, err := db.Exec(`CREATE DATABASE "` + name + `" TEMPLATE "` + template + `" STRATEGY WAL_LOG`)
		return err
	}); err != nil {
		t.Fatalf("create test database: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec(`DROP DATABASE IF EXISTS "` + name + `" WITH (FORCE)`); err != nil {
			t.Errorf("drop test database %s: %v", name, err)
		}
	})
	return URLFor(base, name)
}

// retryInUse retries fn while Postgres reports the source or target database in use (55006).
func retryInUse(fn func() error) error {
	var err error
	for range 100 {
		var e *pgconn.PgError
		if err = fn(); !errors.As(err, &e) || e.Code != "55006" {
			return err
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("database still in use: %w", err)
}
