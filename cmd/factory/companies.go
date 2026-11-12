package main

import (
	"context"
	"errors"
	"net/url"

	"appfactory/internal/core"
	"appfactory/internal/worker"
)

// companiesCommand is `factory companies|templates|owner-tokens ...` (company-self-service 4.1).
func companiesCommand(ctx context.Context, api worker.API, args []string) error {
	usage := errors.New("usage: companies list | companies create ID TEMPLATE [NAME] | companies plan ID free|pro|unlimited | templates | owner-tokens list | owner-tokens create NAME | owner-tokens revoke ID")
	method, path := "GET", ""
	var body any
	switch {
	case args[0] == "templates" && len(args) == 1:
		path = "/templates"
	case args[0] == "companies" && len(args) == 2 && args[1] == "list":
		path = "/companies"
	case args[0] == "companies" && (len(args) == 4 || len(args) == 5) && args[1] == "create":
		p := core.CompanyCreate{ID: args[2], Template: args[3]}
		if len(args) == 5 {
			p.Name = args[4]
		}
		method, path, body = "POST", "/companies", p
	case args[0] == "companies" && len(args) == 4 && args[1] == "plan":
		// saas-billing: the operator sets the plan of the company's account.
		method, path, body = "PUT", "/companies/"+url.PathEscape(args[2])+"/plan", map[string]string{"plan": args[3]}
	case args[0] == "owner-tokens" && len(args) == 2 && args[1] == "list":
		path = "/owner-tokens"
	case args[0] == "owner-tokens" && len(args) == 3 && args[1] == "create":
		method, path, body = "POST", "/owner-tokens", map[string]string{"name": args[2]}
	case args[0] == "owner-tokens" && len(args) == 3 && args[1] == "revoke":
		method, path = "POST", "/owner-tokens/"+url.PathEscape(args[2])+"/revoke"
	default:
		return usage
	}
	var v any
	if err := api.Do(ctx, method, path, body, &v); err != nil {
		return err
	}
	return printJSON(v)
}
