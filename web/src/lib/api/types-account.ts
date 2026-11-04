// Who is signed in (Core GET /me, GET /company; user-accounts).
export type Role = "owner" | "admin" | "member";

export interface Membership { company: string; name: string; role: Role }

export interface User { id: string; account: string; email: string; name: string; last_company?: string; created_at: number; welcomed_at?: number }

export interface Me {
  kind: "session" | "token" | "operator";
  user?: User;
  companies: Membership[];
  last_company?: string;
  multi_company: boolean;
}

export interface CompanyInfo { id: string; name: string; role: Role; multi_company: boolean }

// canManage: owners and admins manage the catalog, skills, secrets, nodes, projects and owner tokens.
export const canManage = (role?: Role) => role === "owner" || role === "admin";
