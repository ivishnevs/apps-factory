# Incident runbook

What to do when the hosted App Factory (Railway, `docs/deploy-railway.md`) misbehaves. Written for the operator:
the person holding the Railway project, `FACTORY_TOKEN` and the backups of `FACTORY_SECRETS_KEY`.
(saas-release-readiness 2.4.)

Some pieces this runbook relies on are still open in `openspec/changes/saas-release-readiness/tasks.md`: daily
backups with a tested restore (2.1) and uptime checks with alerts (2.2). Until they are done, you learn about an
incident from people, and a restore means Railway's own Postgres snapshots.

## 1. First ten minutes

1. **Confirm.** Open `https://<core domain>/health` (`{"status":"ok","database":"postgres …"}`; a 503 means Core
   cannot reach Postgres). Try signing in. Look at the Railway services: `apps-factory` (Core), `web`, `dex`,
   `Postgres`. Are they running, restarting, out of memory or out of disk?
2. **Say so.** Set a status note (below) so the landing, the sign-in page and the app show it to everyone. Keep it
   plain and short: what is affected and when you will update it.
3. **Write down the time** you started and every step you take. That becomes the write-up (section 6).
4. **Decide the severity:**
   - **S1:** nobody can sign in or work, data may be lost or exposed. Work on it now, until it is fixed.
   - **S2:** one part is down (Telegram, payments, node connections, one company), work goes on otherwise.
   - **S3:** degraded or cosmetic. Fix it during normal work.

## 2. The status note

One line every visitor sees, set by the operator. It is stored in Postgres (`core_meta`), so it holds across
restarts and every Core process serves it. When Core itself is down, nobody sees it: then the Railway status or a
direct message to the affected people is all there is.

```sh
# set (at most 300 characters, one line)
curl -fsS -X PUT https://<core domain>/status-note -H "Authorization: Bearer $FACTORY_TOKEN" \
  -H 'Content-Type: application/json' -d '{"note":"Sign-in is failing for GitHub accounts. We are on it; next update 14:30 UTC."}'
# clear
curl -fsS -X PUT https://<core domain>/status-note -H "Authorization: Bearer $FACTORY_TOKEN" \
  -H 'Content-Type: application/json' -d '{"note":""}'
```

It shows as a banner on the landing page and in the app, and as a notice on the sign-in page (Core serves it in
`GET /auth/providers` and `GET /me`). Clear it when the incident is over. For planned maintenance, set it a day
before.

## 3. Where to look

- **Core's logs** (Railway → `apps-factory` → Logs). Core logs in `log/slog` text format. Search for `level=ERROR`,
  then `level=WARN`. Useful messages: `sign-in refused` (with a reason), `sign-in start`, `telegram bots`,
  `paddle`, `export`, `company deleted`, `account deleted`, `deleted company's blobs left behind`.
- **Dex's logs** (Railway → `dex`) when sign-in fails between "Continue with GitHub" and the return to Core.
- **web's logs** when pages do not load but `/health` is fine (Core proxies the pages to `web` through
  `FACTORY_WEB_UPSTREAM`; Core logs `web UI unreachable` then).
- **Postgres** (Railway → Postgres → Metrics): connections, disk, memory.
- **The volume** (`/data` on Core): blobs live in `/data/blobs`. A full volume fails uploads, artifacts and run logs.

## 4. Common incidents

### Core does not start or keeps restarting

Look at the last lines before the exit. Usual causes:

- **A migration failed** after a deploy. Core migrates on start and stops on a failed migration. Redeploy the
  previous deployment in Railway (Deployments → the last good one → Redeploy). Migrations only add things, so the
  older Core runs on the newer schema as long as the failed migration was rolled back by Postgres (it runs in a
  transaction). Fix the migration in the repository; never edit the schema by hand.
- **`FACTORY_SECRETS_KEY` is missing or wrong.** Core refuses to start with a key that does not open the stored
  company keys. Put back the original key from its backup outside Railway. **Without that key the companies'
  secrets are lost**: there is no recovery. Companies then set their secrets again.
- **Postgres is unreachable** (`/health` 503, connection errors). Check the Postgres service, its disk and its
  connection limit. Restart Core after Postgres is back if it does not recover by itself.

### Nobody can sign in

1. `GET /auth/providers` answers? If not, Core is down (above).
2. The browser is sent to Dex and Dex shows an error: Dex's logs. Usual causes: the GitHub or Google OAuth app's
   secret changed or expired, the callback URL does not match `https://<dex domain>/dex/callback`, or Dex lost its
   database (`DEX_STORAGE=postgres`).
3. Back at Core with `/signin?error=…`: Core's log line `sign-in refused` names the reason (`state`, `provider`,
   `email_unverified`, `account`, `session`). `state` errors for everyone usually mean the `factory_login` cookie does not survive: check
   `FACTORY_PUBLIC_URL` is the https address people use.
4. Many people get `429 too many sign-in requests`: see rate limits below.

### Rate limits refuse real traffic

Core limits the routes it serves without a credential, per client address and overall, per minute
(`internal/core/ratelimit.go`): sign-in (`/auth/*`, 60 per address, 1,200 overall), invitation previews (20,
300), a node's connection or enrollment request (10, 120), a waiting node's status polls (120, 3,000), the
installer and node downloads (30, 600). A refusal is `429` with `Retry-After: 60` and `"reason":"rate_limited"`.

- One address over its share is usually a script or a loop. It recovers by itself within a minute.
- The **overall** limit being hit means a flood (or a launch). Core keeps working for people already signed in and
  for nodes already connected; only new sign-ins, connections and downloads wait. If it is an attack, block it in
  front of Core (Railway has no WAF: put a proxy such as Cloudflare in front of the Core domain). If it is real
  traffic, raise the limits in `ratelimit.go` and deploy.
- The client address is the first `X-Forwarded-For` entry. Behind Railway's edge that is the visitor's address.
  If every request suddenly shares one address, a proxy in between is not passing it on.

### Nodes do not connect or take no work

- `factory-node login` stays at "waiting": the person did not approve on the web, or the request expired. If many
  nodes report `429`, see rate limits.
- Connected nodes take no work: check the company is not stopped (Settings shows "Company stopped"), the plan's
  node limit (`plan_limit` in the queued reason, `docs/billing.md`), and Core's log for claim errors.
- Nodes went offline or runs were lost after a host slept, `node.signature_refused` or `node.clock_skew` in the
  feed, or events stuck (`delivery.events` check failing): see [node-troubleshooting.md](node-troubleshooting.md).

### Telegram is silent

Each company's bot takes webhooks at `https://<core domain>/telegram/hook/<company>`. Core logs `telegram bots`
and `telegram webhook` warnings. A bot token revoked in Telegram stops that company's bot only; the company adds it
again in Settings → Integrations.

### Payments or plans look wrong

See `docs/billing.md`. Paddle retries webhooks; `/hooks/paddle` applies each event once and in order, so a
temporary failure heals when Core is back. A plan can be set by hand meanwhile:
`factory companies plan <company> free|pro|unlimited`.

### A company's agents do damage (runaway runs, wrong pushes)

Stop the company first, think later. The owner presses Stop on the web. The operator stops any company with
`curl -X POST https://<core domain>/stop -H "Authorization: Bearer $FACTORY_TOKEN" -H "X-Factory-Company: <id>"
-H 'Content-Type: application/json' -d '{"reason":"incident"}'` (`factory stop <reason>` stops the company of
the token it runs with). The kill switch pauses the company and revokes every live run; tasks return to READY.

### Suspected breach or leaked secret

1. Set a status note only if people need to act (sign in again, rotate their own tokens).
2. Rotate what leaked:
   - `FACTORY_TOKEN` (operator): set a new value in Railway; Core restarts with it.
   - A company owner token: revoke it in Settings → Tokens (or ask the company's owner).
   - The OIDC client secret: new random value in both Core's and Dex's `FACTORY_OIDC_CLIENT_SECRET`.
   - The GitHub or Google OAuth app secret: regenerate it at the provider, update Dex's variables.
   - Paddle keys: roll them in Paddle, update `FACTORY_PADDLE_*`.
   - `FACTORY_SECRETS_KEY`: the rotation procedure is task 1.3 of saas-release-readiness (not written yet).
     Do not change the variable without it: Core would no longer open the stored secrets.
3. Web sessions: there is no route yet to end every session at once. Rotating the OIDC secret does not end
   existing sessions (they are Core's own). Note this gap in the write-up if it mattered.
4. Keep Core's logs of the period (Railway keeps them for a limited time: export them).
5. If personal data may have been exposed, the Privacy Policy's breach section applies (notify affected people
   and, in the EU, the supervisory authority within 72 hours). Talk to the lawyer the same day.

## 5. Requests about people's data

Owners and people do this themselves (Settings): **Company data** exports or deletes a company (owners), **Account**
exports the person's data or deletes their account. Core requires typing the company's id or the person's email.

- Deleting a company removes every row of the company in every company table, its skills and its blobs, at once.
  Machines connected to it stop getting work (their keys are gone).
- Deleting an account removes the companies of the account (as above), the person's memberships elsewhere, their
  sign-in identities, sessions, the account's subscription row and billing events. It is refused while a paid
  subscription still renews: the person cancels it on the Billing page first (Paddle keeps its own invoice
  records as merchant of record).
- Names of a deleted person stay where other companies recorded them in their own work (comments, attribution);
  those belong to those companies.
- When someone asks support to do it for them, verify the request comes from the account's email, then:
  `curl -X DELETE https://<core domain>/company -H "Authorization: Bearer $FACTORY_TOKEN" -H "X-Factory-Company: <id>"
  -H 'Content-Type: application/json' -d '{"confirm":"<id>"}'`. Account deletion is only self-service for now
  (a signed-in person); ask them to use Settings → Account.
- Backups keep deleted data until they expire. Say so when asked (the Privacy Policy states the retention).
- If Core logs `deleted company's blobs left behind`, remove `/data/blobs/<company>` on the volume.

## 6. After the incident

Within two working days write a short note (in the repository's `docs/incidents/` or the owner's notes):

- What happened, who was affected, from when to when (UTC).
- How it was noticed and how long until the status note went up.
- The cause, and why nothing caught it earlier.
- What was done, in order.
- Follow-ups as OpenSpec changes or tasks: fix the cause in the system (a check, a limit, an alert), not by hand.

Clear the status note, and tell the people who reported the problem that it is fixed.
