Status: proposal — needs the owner's decision (raised 2026-11-05 in saas-web-ui, P1). Nothing is implemented.

## Why

A company is one person today. Memberships may only join a user and a company of the same account (composite keys
of `user-accounts`), and every first sign-in creates its own account, so nobody can be added to someone else's
company. Settings → Members already lists people and lets owners change roles and remove them; adding people is
missing.

## What Changes (proposed)

- Memberships across accounts: the membership keys drop the account; the company's account stays its billing owner.
- Invitation links: an owner creates `POST /invitations {role}` → a single-use link valid 7 days, listed and revocable
  in Settings → Members; `/invite/<token>` signs the person in (or up) and joins the company with that role.
- Email delivery of invitations later (needs a mail provider).

## Decisions needed

1. Accounts: is an account a billing tenant that may own several companies with people from other accounts (this
   proposal), or should invitations move the invitee into the inviting account?
2. Who invites: owners only, or admins too (for the member and admin roles)?
3. Links only for now, or email from the start (and which provider)?

## Impact

`internal/storage` (migration on memberships, invitations table), `internal/core` (routes, session gateway for
`/invite`), web Settings → Members and `/invite/<token>`, `docs/owner-ui.md`, user-accounts spec.
