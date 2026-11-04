package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"appfactory/internal/config"
	"appfactory/internal/storage"
)

// Company provisioning (company-self-service 4.1, 4.2): the operator (FACTORY_TOKEN) creates a company from a
// starter template; the company gets its catalog and a first owner token, with which its owner administers
// that company only.

type operatorKey struct{}

// operator reports whether the request carries the operator's shared token.
func operator(r *http.Request) bool {
	v, _ := r.Context().Value(operatorKey{}).(bool)
	return v
}

// operatorOnly are the routes a company owner token may not use.
func operatorOnly(path string) bool {
	return path == "/companies" || strings.HasPrefix(path, "/companies/")
}

// ownerGateway serves a company owner token: the owner routes of its company, except the operator's.
func (c *Core) ownerGateway(w http.ResponseWriter, r *http.Request, m *http.ServeMux, token string) {
	t, err := c.Store.AuthenticateOwnerToken(r.Context(), token)
	if errors.Is(err, storage.ErrNotFound) {
		http.Error(w, "unauthorized", 401)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if operatorOnly(r.URL.Path) {
		http.Error(w, "owner token: the operator manages companies", 403)
		return
	}
	if _, err = c.requestCompany(r, t.Company, false); err != nil {
		reply(w, nil, err)
		return
	}
	ctx := context.WithValue(context.WithValue(r.Context(), companyKey{}, t.Company), principalKey{}, principal{Kind: "token", Role: storage.RoleAdmin, Token: t.ID})
	m.ServeHTTP(w, r.WithContext(ctx))
}

// CompanyCreate is POST /companies.
type CompanyCreate struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Template string `json:"template"`
}

// CompanyCreated answers it: the first owner token is shown only here.
type CompanyCreated struct {
	Company        string                   `json:"company"`
	CatalogVersion int64                    `json:"catalog_version"`
	Owner          storage.IssuedOwnerToken `json:"owner_token"`
}

// CreateCompany creates a company with template's catalog and a first owner token.
func (c *Core) CreateCompany(ctx context.Context, in CompanyCreate) (CompanyCreated, error) {
	var out CompanyCreated
	if c.Root == "" {
		return out, fmt.Errorf("%w: this Core serves its catalog from files", storage.ErrInput)
	}
	if _, err := config.CompanyDir(in.ID); err != nil || in.ID == storage.DefaultCompany {
		return out, fmt.Errorf("%w: invalid company id %q", storage.ErrInput, in.ID)
	}
	if in.Template == "" {
		return out, fmt.Errorf("%w: template required (%s)", storage.ErrInput, strings.Join(c.templates(), ", "))
	}
	docs, err := c.templateDocuments(in.ID, in.Template)
	if err != nil {
		return out, fmt.Errorf("%w: %v (templates: %s)", storage.ErrInput, err, strings.Join(c.templates(), ", "))
	}
	if strings.TrimSpace(in.Name) == "" {
		in.Name = in.ID
	}
	if err = c.Store.CreateCompany(ctx, in.ID, in.Name); err != nil {
		return out, err
	}
	v, err := c.SaveCatalog(ctx, in.ID, storage.CatalogSave{Docs: docs, Author: "operator", Note: "created from template " + in.Template})
	if err != nil {
		return out, err
	}
	tok, err := c.Store.CreateOwnerToken(ctx, in.ID, "first owner", "operator")
	if err != nil {
		return out, err
	}
	return CompanyCreated{Company: in.ID, CatalogVersion: v.Version, Owner: tok}, nil
}

func (c *Core) companyRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /companies", func(w http.ResponseWriter, r *http.Request) {
		if !operator(r) {
			http.Error(w, "the operator creates companies", 403)
			return
		}
		var p CompanyCreate
		if !decode(w, r, &p) {
			return
		}
		v, err := c.CreateCompany(r.Context(), p)
		reply(w, v, err)
	})
	m.HandleFunc("GET /companies", func(w http.ResponseWriter, r *http.Request) {
		if !operator(r) {
			http.Error(w, "the operator lists companies", 403)
			return
		}
		list, err := c.Store.Companies(r.Context())
		if err != nil {
			reply(w, nil, err)
			return
		}
		type row struct {
			storage.Company
			CatalogVersion int64 `json:"catalog_version"`
		}
		out := make([]row, 0, len(list))
		for _, co := range list {
			out = append(out, row{co, c.catalogVersion(co.ID)})
		}
		write(w, out)
	})
	m.HandleFunc("GET /templates", func(w http.ResponseWriter, r *http.Request) { write(w, c.templates()) })
	m.HandleFunc("GET /owner-tokens", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.Store.OwnerTokens(r.Context(), c.companyFrom(r))
		reply(w, v, err)
	})
	m.HandleFunc("POST /owner-tokens", func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			Name string `json:"name"`
		}
		if !decode(w, r, &p) {
			return
		}
		v, err := c.Store.CreateOwnerToken(r.Context(), c.companyFrom(r), p.Name, author(r))
		reply(w, v, err)
	})
	m.HandleFunc("POST /owner-tokens/{id}/revoke", func(w http.ResponseWriter, r *http.Request) {
		err := c.Store.RevokeOwnerToken(r.Context(), c.companyFrom(r), r.PathValue("id"))
		reply(w, map[string]bool{"revoked": err == nil}, err)
	})
}
