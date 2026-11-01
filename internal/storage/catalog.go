package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"appfactory/internal/config"
)

// Stored catalogs (company-self-service D1, D2): a company's catalog is a versioned set of documents; the latest
// version is current. A version is saved only when the caller's validation (the catalog loader) accepts it, and
// only on top of the version the caller started from (Base), so two editors never overwrite each other.

// CatalogVersion is one saved version of a company's catalog.
type CatalogVersion struct {
	Version   int64  `json:"version"`
	Author    string `json:"author"`
	Note      string `json:"note,omitempty"`
	CreatedAt int64  `json:"created_at"`
	Documents int    `json:"documents"`
}

// Catalog is a version with its documents (path → content).
type Catalog struct {
	CatalogVersion
	Docs map[string]string `json:"documents_by_path"`
}

// CatalogSave saves Docs as the next version on top of Base (0: the company has none yet). Validate is the
// catalog loader over the documents; its error refuses the save as input.
type CatalogSave struct {
	Base     int64
	Docs     map[string]string
	Author   string
	Note     string
	Validate func(docs map[string]string) error
}

// CurrentCatalog is the company's current catalog; ErrNotFound when it has none.
func (s *Store) CurrentCatalog(ctx context.Context, company string) (Catalog, error) {
	return s.CatalogAt(ctx, company, 0)
}

// CatalogAt is version of the company's catalog, the current one for 0.
func (s *Store) CatalogAt(ctx context.Context, company string, version int64) (Catalog, error) {
	var out Catalog
	q := "SELECT version,author,note,created_at FROM catalog_versions WHERE company_id=? AND (?=0 OR version=?) ORDER BY version DESC LIMIT 1"
	err := s.DB.QueryRowContext(ctx, q, company, version, version).Scan(&out.Version, &out.Author, &out.Note, &out.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT path,content FROM catalog_documents WHERE company_id=? AND version=? ORDER BY path", company, out.Version)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	out.Docs = map[string]string{}
	for rows.Next() {
		var p, c string
		if err = rows.Scan(&p, &c); err != nil {
			return out, err
		}
		out.Docs[p] = c
	}
	out.Documents = len(out.Docs)
	return out, rows.Err()
}

// CatalogHistory lists the company's versions, newest first.
func (s *Store) CatalogHistory(ctx context.Context, company string) ([]CatalogVersion, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT v.version,v.author,v.note,v.created_at,(SELECT count(*) FROM catalog_documents d WHERE d.company_id=v.company_id AND d.version=v.version)
 FROM catalog_versions v WHERE v.company_id=? ORDER BY v.version DESC`, company)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CatalogVersion{}
	for rows.Next() {
		var v CatalogVersion
		if err = rows.Scan(&v.Version, &v.Author, &v.Note, &v.CreatedAt, &v.Documents); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// SaveCatalog stores in.Docs as the company's next version when the current version is in.Base and
// in.Validate accepts the documents: ErrConflict (another version was saved meanwhile) or ErrInput otherwise.
func (s *Store) SaveCatalog(ctx context.Context, company string, in CatalogSave) (CatalogVersion, error) {
	var out CatalogVersion
	if in.Author == "" {
		return out, inputError("author required")
	}
	if len(in.Note) > 1024 {
		return out, inputError("note exceeds 1024 characters")
	}
	if err := config.ValidCatalogDocuments(company, in.Docs); err != nil {
		return out, inputError(err.Error())
	}
	if in.Validate != nil {
		if err := in.Validate(in.Docs); err != nil {
			return out, inputError(err.Error())
		}
	}
	tx, err := s.begin(ctx, company)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var current int64
	if err = tx.QueryRowContext(ctx, "SELECT coalesce(max(version),0) FROM catalog_versions WHERE company_id=?", company).Scan(&current); err != nil {
		return out, err
	}
	if current != in.Base {
		return out, fmt.Errorf("%w: the catalog is at version %d, not %d: reload it and apply your change again", ErrConflict, current, in.Base)
	}
	out = CatalogVersion{Version: current + 1, Author: in.Author, Note: in.Note, CreatedAt: tx.now().Unix(), Documents: len(in.Docs)}
	if _, err = tx.ExecContext(ctx, "INSERT INTO catalog_versions(company_id,version,author,note,created_at) VALUES(?,?,?,?,?)", company, out.Version, out.Author, out.Note, out.CreatedAt); err != nil {
		return out, err
	}
	for p, c := range in.Docs {
		if _, err = tx.ExecContext(ctx, "INSERT INTO catalog_documents(company_id,version,path,content) VALUES(?,?,?,?)", company, out.Version, p, c); err != nil {
			return out, err
		}
	}
	if err = entityEventNote(ctx, tx, "catalog.saved", "", company, out, fmt.Sprintf("catalog version %d saved by %s (%d documents)", out.Version, out.Author, out.Documents)); err != nil {
		return out, err
	}
	return out, tx.Commit()
}

// CatalogCompanies are the companies with a stored catalog.
func (s *Store) CatalogCompanies(ctx context.Context) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT DISTINCT company_id FROM catalog_versions ORDER BY company_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var co string
		if err = rows.Scan(&co); err != nil {
			return nil, err
		}
		out = append(out, co)
	}
	return out, rows.Err()
}
