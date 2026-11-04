"use client";

import { createContext, useContext } from "react";

// The company of the page (user-accounts D6): every route is /c/{company}/…, and every API call names it
// (X-Factory-Company). Images, frames and event streams cannot send headers, so the company also rides in the
// factory_company cookie, which the web API reads when the header is absent.
let current = "";

export function setCompany(company: string) {
  current = company;
  if (typeof document !== "undefined") document.cookie = `factory_company=${encodeURIComponent(company)}; path=/; samesite=lax`;
}

export const currentCompany = () => current;

export const CompanyContext = createContext("");
export const useCompany = () => useContext(CompanyContext);

// Routes outside a company: the sign-in, the company chooser and the API.
const outside = ["/c/", "/api/", "/auth/", "/signin", "/welcome"];

// companyHref puts an app path ("/tasks?x=1") under the company ("/c/acme/tasks?x=1"); other links stay.
export function companyHref(company: string, href: string): string {
  if (!company || !href.startsWith("/") || href.startsWith("//") || outside.some((p) => href === p.replace(/\/$/, "") || href.startsWith(p))) return href;
  return `/c/${encodeURIComponent(company)}${href === "/" ? "" : href}`;
}

// appPath is a pathname without its company prefix ("/c/acme/tasks" → "/tasks").
export function appPath(pathname: string): string {
  const m = pathname.match(/^\/c\/[^/]+(\/.*)?$/);
  return m ? m[1] || "/" : pathname;
}

// csrf is the session's CSRF token, sent as X-Factory-CSRF on every change (user-accounts D5).
export function csrf(): string {
  if (typeof document === "undefined") return "";
  const m = document.cookie.match(/(?:^|;\s*)factory_csrf=([^;]+)/);
  return m ? decodeURIComponent(m[1]) : "";
}

// welcomeSeenKey marks, in this browser, that a person has seen the setup wizard (saas-web-ui D7).
export const welcomeSeenKey = (user: string) => `factory_welcome_seen:${user}`;
