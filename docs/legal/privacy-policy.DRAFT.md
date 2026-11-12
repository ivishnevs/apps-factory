> **DRAFT for review by a lawyer. Not published, not in force, not linked from the site.**
> Written from what the product does today (saas-release-readiness 4.1). Everything in [square brackets] is for the
> owner or the lawyer to fill in or decide. Do not publish until the owner approves the reviewed text; then it
> becomes a page and a footer link.

# Privacy Policy

Last updated: [date]

This policy explains what personal data App Factory ("we", "us") processes when you use the hosted service at
[domain] (the "Service"), why, and what you can do about it.

**Who we are.** The Service is operated by [legal entity name], [registered address], [company number]
("[Short name]"). Contact for privacy questions: [privacy email, e.g. the FACTORY_SUPPORT_EMAIL address].
[If required: our EU/UK representative is …; our data protection officer is ….]

## 1. How the Service works, in short

App Factory runs a team of AI agents for your company. The agents run on **machines you connect** (your "nodes"),
with **your own** AI subscriptions (for example Claude or Codex). Our servers (the "Core") coordinate the work:
they store your company's tasks, conversations, decisions and results, and tell your nodes what to do. The AI
model providers you use on your nodes are your choice and your contract; we do not send your prompts or code to
them and we do not receive your model usage.

## 2. What we process

**Account data.** When you sign in with GitHub or Google we receive your name, email address, whether the email is
verified, and the provider's account identifier. We create a user and, for a new sign-up, an account and a company.

**Company content.** What you and your agents put into a company: company and project names, team and agent
settings, task titles and descriptions, comments, chats with agents, approvals and decisions, reports, artifacts
and files (for example specifications, designs, screenshots), run logs and events reported by your nodes,
repository addresses, branch names and links to pull requests. Run logs and artifacts can contain excerpts of your
source code or other content your agents worked with. You decide what goes in.

**Secrets.** Values you store as company secrets (for example API tokens) and a company's Telegram bot token are
encrypted with a key of your company (AES-256-GCM), itself encrypted with our server key. Our staff do not read
them in the course of operating the Service.

**Team data.** Members of your company (name, email, role), invitations you create, and who did what in the
company's records (for example who approved a change).

**Node data.** For each machine you connect: its name, operating system and architecture, its public keys and
fingerprint, the IP address it connected from when it asked to join, its status and capabilities, and heartbeats.

**Billing data.** If you subscribe to a paid plan, Paddle ([Paddle.com Market Ltd]) is the merchant of record:
Paddle collects your payment details, invoices you and handles taxes. We receive and keep your plan, subscription
status and periods, and Paddle's customer and subscription identifiers. We never receive full card details.

**Technical data.** Our servers and hosting provider log requests (IP address, time, address requested, browser
user agent, errors) to run, secure and debug the Service. IP addresses are also used in memory to rate-limit sign-in
and similar public requests; they are not stored for that.

**Telegram (optional).** If your company connects its own Telegram bot, messages between you and the bot pass
through Telegram and our servers so you can answer questions and receive alerts there.

**Support.** What you write to us when you contact support.

We do not use your data to train AI models, we do not sell it, and we do not use advertising or tracking tools.

## 3. Why we process it (purposes and legal bases)

| Purpose | Data | Legal basis [GDPR, adjust for other regimes] |
|---|---|---|
| Provide the Service (sign-in, your companies, coordinating agents) | account, company content, team, node data | contract (Art. 6(1)(b)) |
| Billing and plan limits | billing data | contract; legal obligation for records (Art. 6(1)(c)) |
| Security, abuse prevention, debugging | technical data, node data | legitimate interests (Art. 6(1)(f)) in a secure, working service |
| Support | support messages | contract; legitimate interests |
| Service and security notices by email [when email is set up] | email address | contract; legitimate interests |

For company content that contains personal data of other people (for example your colleagues or your customers),
you are the controller and we process it on your behalf as a processor: see the [Data Processing Addendum, to be
written].

## 4. Cookies

We use only cookies needed for the Service to work; no analytics or advertising cookies, so we do not ask for
consent:

| Cookie | Purpose | Lifetime |
|---|---|---|
| `factory_session` | keeps you signed in (random id; only its hash is stored on our side) | up to 90 days (30 days without use) |
| `factory_csrf` | protects your actions against cross-site requests | as the session |
| `factory_login` | carries the sign-in while you are at GitHub or Google | at most 10 minutes |
| `factory_company` | remembers which company the open page belongs to | until the browser closes |

The web app may also keep small preferences (for example the colour theme) in your browser's local storage.

## 5. Who else processes your data (sub-processors)

| Who | What for | Where |
|---|---|---|
| Railway ([Railway Corporation]) | hosting of the Core, database and files | [region, e.g. US / EU] |
| GitHub, Google | sign-in, if you choose them | [US] |
| Paddle | payments, invoices, taxes (merchant of record) | [UK / EU / US] |
| Telegram | messages of your company's bot, if you connect one | [various] |
| [Email provider, not chosen yet] | invitations and security notices | [ ] |

The AI providers and code hosts your nodes use (for example Anthropic, OpenAI, GitHub) are used by your machines
under your accounts and are not our sub-processors.

**International transfers.** [Describe transfers outside the EU/UK and the safeguards, e.g. Standard Contractual
Clauses or the EU-US Data Privacy Framework, per provider.]

## 6. How long we keep data

- Account and company data: as long as the account or company exists. When you delete it (section 7) it is removed
  from the live database and file storage at once.
- Backups: deleted data stays in backups until they expire, at most [N] days, and is not restored into the
  Service except to recover from an incident.
- Server logs: [N] days at our hosting provider.
- Billing records: as long as the law requires ([N] years); Paddle keeps its own records as merchant of record.
- Superseded versions of artifacts: as your project's retention setting says (keep forever by default).

## 7. Your rights and choices

You can, in the Service:

- **Export** your data: Settings → Account → Export my data (your profile, sign-ins, memberships, subscription and
  the companies of your account); owners also export a company in Settings → General → Company data. The export is
  a zip of JSON files and your files. Secret values and tokens are left out.
- **Delete** your account (Settings → Account) or a company you own (Settings → General). Deleting an account deletes
  the companies it owns for everyone in them; companies of other people stay and you leave them. A paid
  subscription must be cancelled first.
- **Correct** your name and email at GitHub or Google; they update when you sign in again.

Depending on where you live you may also have the right to access, rectify, erase, restrict or object to
processing, to data portability, and to complain to a supervisory authority ([name the lead authority]). Write to
[privacy email]; we answer within one month.

If you are a member of a company owned by someone else, that company's owner controls the company's content; ask
them, or us and we will pass your request on.

## 8. Security

Sign-in through GitHub or Google (no passwords stored by us), server-side sessions, CSRF protection, encryption of
secrets at rest under per-company keys, signed requests from nodes with keys that never leave your machines, rate
limits on public routes, and encrypted connections (HTTPS). See also [the Security page]. No system is perfectly
secure; if a breach affects your personal data we will tell you and the authorities as the law requires.

## 9. Children

The Service is not meant for anyone under [16] and we do not knowingly collect their data.

## 10. Changes

We will announce material changes in the Service or by email [N] days before they apply. The date at the top shows
the latest version.
