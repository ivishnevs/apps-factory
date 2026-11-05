## 0. Decision

- [x] 0.1 Owner decides the account model, who invites and links versus email (2026-11-05: links, owners, accounts
  stay billing owners).

## 1. Implementation

- [x] 1.1 Storage: memberships across accounts (migration 0019), invitations (single use, 7 days, token hash);
  sign-up without a company; tests (`TestInvitations`).
- [x] 1.2 Core: `GET/POST /invitations`, `DELETE /invitations/{id}` (owners), public preview
  `GET /auth/invitations/{token}`, `POST /me/invitations/{token}`; sign-up from a link joins the company; tests.
- [x] 1.3 Web: invite links in Settings → Members (create, copy, list, revoke); `/invite/<token>` (signed out:
  sign in to join; signed in: join; already in; used or invalid); browser check of both pages locally (the sign-in
  path itself is covered by `TestInvitations`, it needs a provider).

## 2. Later

- [ ] 2.1 Email delivery of invitations (needs a mail provider; release checklist).
