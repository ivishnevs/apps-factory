## 1. Verify

- [ ] 1.1 Claude Code with its own CLAUDE_CONFIG_DIR on macOS: login lands in a separate Keychain entry, the
      person's terminal login is untouched, accountInfo reports the node's account.
- [ ] 1.2 Codex with its own CODEX_HOME: login, account/read, skills and sessions under it.

## 2. Own runtime logins (option 1)

- [x] 2.1 Node config directories per runtime; drivers pass CLAUDE_CONFIG_DIR / CODEX_HOME to every run.
- [x] 2.2 `factory-node login claude|codex` and `logout`; doctor shows the account (plan, organisation, no email).
- [x] 2.3 Installer and upgrade: offer to copy the current login once or log in anew; doctor warns while a runtime
      still uses the shared login.
- [x] 2.4 Transcripts and session lookup (TUI, web) read the node's runtime directories.

## 3. Account change detection (option 2)

- [x] 3.1 Fingerprint per runtime (plan, organisation id, email hash) recorded at login / first run.
- [x] 3.2 Check before each run; a change or logout pauses that runtime on the node with the reason.
- [x] 3.3 Core: Inbox item and Telegram notice with Continue / Keep paused; Continue records the new fingerprint.
- [x] 3.4 Tests: account switch pauses only that runtime; continue resumes; logout reported.
