package storage

import (
	"archive/zip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"time"
)

// Data rights (saas-release-readiness 4.2): an owner exports or deletes a company with all its data, a person
// exports or deletes their account. Both work on the schema as it is, not on a list kept by hand: every table with
// a company_id column holds company data (postgres-tenancy D2), so a table added later is exported and deleted
// without touching this file. Blobs (artifacts, documents, attachments, run logs) go with their company.
//
// An export leaves out what only Core can use and what would let someone act as the company: encrypted material
// (every bytea column: the wrapped company key, secret values, the Telegram bot token, cached responses) and the
// hashes of bearer secrets and pairing codes (exportOmitted). Secret names, versions and authors stay.

// exportOmitted are the text columns an export leaves out wherever they appear.
var exportOmitted = map[string]bool{"hash": true, "token_hash": true, "id_hash": true, "code": true}

// exportColumn reports whether an export keeps column (of data type) of a company table.
func exportColumn(column, dataType string) bool {
	return dataType != "bytea" && !exportOmitted[column]
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// companyTables are the tables holding company rows (a company_id column), the companies table aside.
func companyTables(ctx context.Context, q queryer) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT table_name FROM information_schema.columns
 WHERE table_schema=current_schema() AND column_name='company_id' AND table_name<>'companies'
 AND table_name IN (SELECT table_name FROM information_schema.tables WHERE table_schema=current_schema() AND table_type='BASE TABLE')
 ORDER BY table_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err = rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// deletionOrder orders tables so that a table comes before every table it references (children first); tables in
// a reference cycle come last, in name order (the deletion retries them).
func deletionOrder(ctx context.Context, q queryer, tables []string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT ch.relname, pa.relname FROM pg_constraint c
 JOIN pg_class ch ON ch.oid=c.conrelid JOIN pg_class pa ON pa.oid=c.confrelid JOIN pg_namespace n ON n.oid=c.connamespace
 WHERE c.contype='f' AND n.nspname=current_schema()`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	in := map[string]bool{}
	for _, t := range tables {
		in[t] = true
	}
	// referencedBy[p] are the tables (other than p) with a foreign key to p.
	referencedBy := map[string]map[string]bool{}
	for rows.Next() {
		var child, parent string
		if err = rows.Scan(&child, &parent); err != nil {
			return nil, err
		}
		if child == parent || !in[child] || !in[parent] {
			continue
		}
		if referencedBy[parent] == nil {
			referencedBy[parent] = map[string]bool{}
		}
		referencedBy[parent][child] = true
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	var out []string
	done := map[string]bool{}
	for len(out) < len(tables) {
		progress := false
		for _, t := range tables {
			if done[t] {
				continue
			}
			ready := true
			for child := range referencedBy[t] {
				if !done[child] {
					ready = false
					break
				}
			}
			if ready {
				out, done[t], progress = append(out, t), true, true
			}
		}
		if !progress {
			for _, t := range tables {
				if !done[t] {
					out, done[t] = append(out, t), true
				}
			}
		}
	}
	return out, nil
}

// deleteCompanyRows deletes every row of company in tx: its rows in every company table (children first; a
// table a foreign key still holds is retried after the others), its skills, the companies row. Users that last
// worked in it start from their other companies.
func (s *Store) deleteCompanyRows(ctx context.Context, tx *sql.Tx, company string) error {
	if err := lockCompanyTx(ctx, tx, company); err != nil {
		return err
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM companies WHERE id=$1)", company).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	tables, err := companyTables(ctx, tx)
	if err != nil {
		return err
	}
	if tables, err = deletionOrder(ctx, tx, tables); err != nil {
		return err
	}
	for len(tables) > 0 {
		var held []string
		for _, t := range tables {
			if _, err = tx.ExecContext(ctx, "SAVEPOINT delete_company"); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, "DELETE FROM "+quoteIdent(t)+" WHERE company_id=$1", company); err != nil {
				if pgCode(err) != "23503" { // foreign_key_violation: another table still refers to these rows
					return fmt.Errorf("delete %s: %w", t, err)
				}
				if _, err = tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT delete_company"); err != nil {
					return err
				}
				held = append(held, t)
				continue
			}
			if _, err = tx.ExecContext(ctx, "RELEASE SAVEPOINT delete_company"); err != nil {
				return err
			}
		}
		if len(held) == len(tables) {
			return fmt.Errorf("delete company %s: rows of %s are still referenced", company, strings.Join(held, ", "))
		}
		tables = held
	}
	for _, q := range []string{
		"DELETE FROM skill_sources WHERE scope=$1", // its skill_index rows go with it (ON DELETE CASCADE)
		"UPDATE users SET last_company_id='' WHERE last_company_id=$1",
		"DELETE FROM companies WHERE id=$1",
	} {
		if _, err = tx.ExecContext(ctx, q, company); err != nil {
			return err
		}
	}
	return nil
}

func quoteIdent(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

// DeleteCompany deletes company with all its data: every row Core keeps for it and its blobs. The default
// company, which every database has, is not deleted (ErrInput).
func (s *Store) DeleteCompany(ctx context.Context, company string) error {
	if company == "" || company == DefaultCompany {
		return inputError("the default company is not deleted")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = s.deleteCompanyRows(ctx, tx, company); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.deleteCompanyBlobs(ctx, company)
	return nil
}

// deleteCompanyBlobs removes a deleted company's blobs. Its rows are gone by then, so a failure is logged for the
// operator (docs/incident-runbook.md) rather than reported as a failed deletion.
func (s *Store) deleteCompanyBlobs(ctx context.Context, company string) {
	if s.Blobs == nil {
		return
	}
	if err := s.Blobs.DeleteCompany(ctx, company); err != nil {
		slog.Error("deleted company's blobs left behind", "company", company, "error", err)
	}
}

// ErrSubscriptionActive refuses deleting an account whose paid subscription still renews.
var ErrSubscriptionActive = errors.New("the account has a subscription that still renews: cancel it on the Billing page first")

// AccountDeletion is what deleting an account removed.
type AccountDeletion struct {
	User      string   `json:"user"`
	Account   string   `json:"account"`
	Companies []string `json:"companies"`
}

// DeleteAccount deletes user's account with all its data: the companies of the account (as DeleteCompany), the
// user's memberships in other companies, sign-in identities and sessions, the account's subscription and billing
// events, the user and the account. A paid subscription that still renews is refused (ErrSubscriptionActive): the
// provider would keep charging. An account another user shares (only operator-made accounts) is refused
// (ErrConflict). A user of the default account (operator-made) loses only their own rows.
func (s *Store) DeleteAccount(ctx context.Context, user string) (AccountDeletion, error) {
	out := AccountDeletion{User: user, Companies: []string{}}
	u, err := s.User(ctx, user)
	if err != nil {
		return out, err
	}
	out.Account = u.Account
	own := u.Account != DefaultAccount
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if own {
		var others int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM users WHERE account_id=$1 AND id<>$2", u.Account, user).Scan(&others); err != nil {
			return out, err
		}
		if others > 0 {
			return out, conflictError("other people sign in with this account; the operator removes it")
		}
		var status, subscription string
		var cancelAt int64
		err = tx.QueryRowContext(ctx, "SELECT status, subscription_id, cancel_at FROM account_subscriptions WHERE account_id=$1 FOR UPDATE", u.Account).Scan(&status, &subscription, &cancelAt)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return out, err
		}
		if err == nil && subscription != "" && status != SubCanceled && cancelAt == 0 {
			return out, ErrSubscriptionActive
		}
		rows, err := tx.QueryContext(ctx, "SELECT id FROM companies WHERE account_id=$1 ORDER BY id", u.Account)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return out, err
			}
			out.Companies = append(out.Companies, id)
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return out, err
		}
		for _, company := range out.Companies {
			if company == DefaultCompany {
				return out, inputError("the default company is not deleted")
			}
			if err = s.deleteCompanyRows(ctx, tx, company); err != nil {
				return out, err
			}
		}
	}
	stmts := []string{
		"DELETE FROM memberships WHERE user_id=$1",
		"DELETE FROM identities WHERE user_id=$1",
		"DELETE FROM sessions WHERE user_id=$1",
		"DELETE FROM users WHERE id=$1",
	}
	args := []any{user}
	if own {
		stmts = append(stmts, "DELETE FROM account_subscriptions WHERE account_id=$1", "DELETE FROM billing_events WHERE account_id=$1", "DELETE FROM accounts WHERE id=$1")
	}
	for _, q := range stmts {
		a := args
		if strings.Contains(q, "account_id") || strings.HasPrefix(q, "DELETE FROM accounts") {
			a = []any{u.Account}
		}
		if _, err = tx.ExecContext(ctx, q, a...); err != nil {
			return out, constraint(err)
		}
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	for _, company := range out.Companies {
		s.deleteCompanyBlobs(ctx, company)
	}
	return out, nil
}

// exportTx is a read-only snapshot: an export sees the data of one moment.
func (s *Store) exportTx(ctx context.Context) (*sql.Tx, error) {
	return s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
}

// ExportManifest describes an export: what it holds and what it leaves out.
type ExportManifest struct {
	Company    string           `json:"company"`
	Name       string           `json:"name"`
	ExportedAt int64            `json:"exported_at"`
	Tables     map[string]int64 `json:"tables"`
	Blobs      int              `json:"blobs"`
	Omitted    string           `json:"omitted"`
}

const exportOmittedNote = "encrypted material (secret values, the company key, bot tokens) and the hashes of tokens and codes; secret names stay"

// ExportCompany writes company's data into zw under prefix: manifest.json, tables/<table>.json (an array of rows
// as JSON objects) for every company table, skills.json and blobs/<sha256> (the blobs' contents).
func (s *Store) ExportCompany(ctx context.Context, company string, zw *zip.Writer, prefix string) error {
	tx, err := s.exportTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return s.exportCompany(ctx, tx, company, zw, prefix)
}

func (s *Store) exportCompany(ctx context.Context, tx *sql.Tx, company string, zw *zip.Writer, prefix string) error {
	m := ExportManifest{Company: company, ExportedAt: s.Now().Unix(), Tables: map[string]int64{}, Omitted: exportOmittedNote}
	if err := tx.QueryRowContext(ctx, "SELECT name FROM companies WHERE id=$1", company).Scan(&m.Name); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	tables, err := companyTables(ctx, tx)
	if err != nil {
		return err
	}
	if err = exportRows(ctx, tx, zw, prefix+"tables/companies.json", "SELECT row_to_json(t)::text FROM (SELECT * FROM companies WHERE id=$1) t", company); err != nil {
		return err
	}
	for _, t := range tables {
		cols, err := exportColumns(ctx, tx, t)
		if err != nil {
			return err
		}
		n, err := exportCount(ctx, tx, zw, prefix+"tables/"+t+".json",
			"SELECT row_to_json(t)::text FROM (SELECT "+strings.Join(cols, ",")+" FROM "+quoteIdent(t)+" WHERE company_id=$1) t", company)
		if err != nil {
			return fmt.Errorf("export %s: %w", t, err)
		}
		m.Tables[t] = n
	}
	if err = exportRows(ctx, tx, zw, prefix+"skills.json",
		"SELECT row_to_json(t)::text FROM (SELECT s.*, (SELECT coalesce(json_agg(i), '[]'::json) FROM skill_index i WHERE i.scope=s.scope AND i.source_id=s.id) AS skills FROM skill_sources s WHERE s.scope=$1) t", company); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, "SELECT sha256 FROM blobs WHERE company_id=$1 ORDER BY sha256", company)
	if err != nil {
		return err
	}
	var digests []string
	for rows.Next() {
		var sha string
		if err = rows.Scan(&sha); err != nil {
			rows.Close()
			return err
		}
		digests = append(digests, sha)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, sha := range digests {
		if s.Blobs == nil {
			break
		}
		r, err := s.Blobs.Get(ctx, company, sha)
		if err != nil {
			// A blob retention removed between the snapshot and now is no reason to fail the export.
			slog.Warn("export blob", "company", company, "sha256", sha, "error", err)
			continue
		}
		f, err := zw.CreateHeader(&zip.FileHeader{Name: prefix + "blobs/" + sha, Method: zip.Deflate, Modified: time.Unix(m.ExportedAt, 0)})
		if err == nil {
			_, err = io.Copy(f, r)
		}
		r.Close()
		if err != nil {
			return err
		}
		m.Blobs++
	}
	return writeJSON(zw, prefix+"manifest.json", m)
}

// exportColumns are the columns of table an export keeps, quoted.
func exportColumns(ctx context.Context, tx *sql.Tx, table string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, "SELECT column_name, data_type FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=$1 ORDER BY ordinal_position", table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var col, typ string
		if err = rows.Scan(&col, &typ); err != nil {
			return nil, err
		}
		if exportColumn(col, typ) {
			out = append(out, quoteIdent(col))
		}
	}
	return out, rows.Err()
}

func exportRows(ctx context.Context, tx *sql.Tx, zw *zip.Writer, name, query string, args ...any) error {
	_, err := exportCount(ctx, tx, zw, name, query, args...)
	return err
}

// exportCount writes the JSON rows query returns as one JSON array into the file name of zw.
func exportCount(ctx context.Context, tx *sql.Tx, zw *zip.Writer, name, query string, args ...any) (int64, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	f, err := zw.Create(name)
	if err != nil {
		return 0, err
	}
	var n int64
	if _, err = io.WriteString(f, "["); err != nil {
		return 0, err
	}
	for rows.Next() {
		var row string
		if err = rows.Scan(&row); err != nil {
			return n, err
		}
		sep := ",\n"
		if n == 0 {
			sep = "\n"
		}
		if _, err = io.WriteString(f, sep+row); err != nil {
			return n, err
		}
		n++
	}
	if err = rows.Err(); err != nil {
		return n, err
	}
	_, err = io.WriteString(f, "\n]\n")
	return n, err
}

func writeJSON(zw *zip.Writer, name string, v any) error {
	f, err := zw.Create(name)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// AccountExport is account.json of an account export.
type AccountExport struct {
	ExportedAt   int64            `json:"exported_at"`
	User         User             `json:"user"`
	Identities   []map[string]any `json:"identities"`
	Memberships  []Membership     `json:"memberships"`
	Subscription *Subscription    `json:"subscription,omitempty"`
	// Companies are the companies of the account, exported under companies/<id>/.
	Companies []string `json:"companies"`
	Note      string   `json:"note"`
}

// ExportAccount writes user's data into zw: account.json (the user, sign-in identities, memberships, the
// account's subscription) and every company of the user's account under companies/<id>/ (as ExportCompany).
// Companies of other accounts the user is a member of belong to those accounts and are not exported here.
func (s *Store) ExportAccount(ctx context.Context, user string, zw *zip.Writer) error {
	tx, err := s.exportTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	out := AccountExport{ExportedAt: s.Now().Unix(), Identities: []map[string]any{}, Memberships: []Membership{}, Companies: []string{},
		Note: "Companies of other accounts you belong to are exported by their owners. " + "Left out: " + exportOmittedNote + "."}
	if out.User, err = userBy(ctx, &Tx{Tx: tx}, user); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, "SELECT row_to_json(t)::text FROM (SELECT provider, subject, email, created_at FROM identities WHERE user_id=$1 ORDER BY created_at) t", user)
	if err != nil {
		return err
	}
	for rows.Next() {
		var raw string
		var v map[string]any
		if err = rows.Scan(&raw); err == nil {
			err = json.Unmarshal([]byte(raw), &v)
		}
		if err != nil {
			rows.Close()
			return err
		}
		out.Identities = append(out.Identities, v)
	}
	rows.Close()
	if rows, err = tx.QueryContext(ctx, "SELECT m.company_id, c.name, m.role, c.account_id FROM memberships m JOIN companies c ON c.id=m.company_id WHERE m.user_id=$1 ORDER BY m.company_id", user); err != nil {
		return err
	}
	for rows.Next() {
		var m Membership
		var account string
		if err = rows.Scan(&m.Company, &m.Name, &m.Role, &account); err != nil {
			rows.Close()
			return err
		}
		out.Memberships = append(out.Memberships, m)
	}
	rows.Close()
	if out.User.Account != DefaultAccount {
		var sub Subscription
		err = tx.QueryRowContext(ctx, "SELECT account_id, plan, status, trial_ends_at, period_ends_at, cancel_at, provider, customer_id, subscription_id, updated_at FROM account_subscriptions WHERE account_id=$1", out.User.Account).
			Scan(&sub.Account, &sub.Plan, &sub.Status, &sub.TrialEndsAt, &sub.PeriodEndsAt, &sub.CancelAt, &sub.Provider, &sub.CustomerID, &sub.SubscriptionID, &sub.UpdatedAt)
		if err == nil {
			out.Subscription = &sub
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if rows, err = tx.QueryContext(ctx, "SELECT id FROM companies WHERE account_id=$1 ORDER BY id", out.User.Account); err != nil {
			return err
		}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			out.Companies = append(out.Companies, id)
		}
		rows.Close()
	}
	for _, company := range out.Companies {
		if err = s.exportCompany(ctx, tx, company, zw, "companies/"+company+"/"); err != nil {
			return err
		}
	}
	slices.Sort(out.Companies)
	return writeJSON(zw, "account.json", out)
}
