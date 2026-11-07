package node

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Runtime accounts (node-runtime-accounts): the node remembers which account each runtime is logged in to — the plan,
// the organisation and a hash of the account (never the email) — and a different account disables that runtime on
// the node until a person accepts it (`factory-node accounts accept RUNTIME`). Other runtimes go on.

// Account is a runtime's account as the node records it.
type Account struct {
	Runtime    string `json:"runtime"`
	Plan       string `json:"plan,omitempty"`
	Org        string `json:"org,omitempty"`
	Hash       string `json:"hash"`
	RecordedAt int64  `json:"recorded_at"`
}

// AccountHash hashes what identifies an account (an email, an account id); "" stays "".
func AccountHash(parts ...string) string {
	s := strings.ToLower(strings.TrimSpace(strings.Join(parts, "\x00")))
	if strings.Trim(s, "\x00") == "" {
		return ""
	}
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:8])
}

func (a Account) words() string {
	w := a.Plan
	if w == "" {
		w = "an account"
	}
	if a.Org != "" {
		w += " of " + a.Org
	}
	return w + " (" + a.Hash + ")"
}

// AccountsDir is where a node keeps its accounts: <state_dir>/accounts.
func AccountsDir(stateDir string) string { return filepath.Join(stateDir, "accounts") }

func accountPath(dir, runtime string) string { return filepath.Join(dir, runtime+".json") }

// LoadAccount is the recorded account of runtime (ok false when none).
func LoadAccount(dir, runtime string) (Account, bool) {
	var a Account
	b, err := os.ReadFile(accountPath(dir, runtime))
	if err != nil || json.Unmarshal(b, &a) != nil {
		return a, false
	}
	return a, true
}

// AcceptAccount forgets runtime's recorded account: the next check records the current one.
func AcceptAccount(dir, runtime string) error {
	err := os.Remove(accountPath(dir, runtime))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// CheckAccount compares the runtime's current account with the recorded one: the first one seen is recorded; a
// different one fails the check (the probe disables the runtime) with what changed and how to continue.
func CheckAccount(dir string, now Account) CheckResult {
	check := now.Runtime + ".account"
	if dir == "" || now.Hash == "" {
		return CheckResult{Check: check, Status: Skip, Detail: "account not identified"}
	}
	old, ok := LoadAccount(dir, now.Runtime)
	if !ok {
		now.RecordedAt = time.Now().Unix()
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return CheckResult{Check: check, Status: Warn, Detail: "account not recorded: " + err.Error()}
		}
		b, _ := json.Marshal(now)
		if err := os.WriteFile(accountPath(dir, now.Runtime), b, 0o600); err != nil {
			return CheckResult{Check: check, Status: Warn, Detail: "account not recorded: " + err.Error()}
		}
		return CheckResult{Check: check, Status: Pass, Detail: "recorded " + now.words()}
	}
	if old.Hash == now.Hash {
		return CheckResult{Check: check, Status: Pass, Detail: now.words()}
	}
	return CheckResult{Check: check, Status: Fail, Detail: fmt.Sprintf("the %s login changed: it was %s, now %s; this node's %s agents are paused", now.Runtime, old.words(), now.words(), now.Runtime),
		Hint: fmt.Sprintf("log the node back in to its account (factory-node login %s), or continue with the new one: factory-node accounts accept %s", now.Runtime, now.Runtime)}
}

// CodexAccount reads the Codex login of home (CODEX_HOME, default ~/.codex): the account id and the plan from its
// token's claims; nothing is sent anywhere.
func CodexAccount(home string) Account {
	a := Account{Runtime: "codex"}
	if home == "" {
		h, _ := os.UserHomeDir()
		home = filepath.Join(h, ".codex")
	}
	var auth struct {
		Tokens struct {
			IDToken   string `json:"id_token"`
			AccountID string `json:"account_id"`
		} `json:"tokens"`
	}
	b, err := os.ReadFile(filepath.Join(home, "auth.json"))
	if err != nil || json.Unmarshal(b, &auth) != nil {
		return a
	}
	a.Hash = AccountHash(auth.Tokens.AccountID)
	if parts := strings.Split(auth.Tokens.IDToken, "."); len(parts) == 3 {
		if payload, err := base64.RawURLEncoding.DecodeString(parts[1]); err == nil {
			var claims map[string]json.RawMessage
			if json.Unmarshal(payload, &claims) == nil {
				var oa struct {
					Plan string `json:"chatgpt_plan_type"`
				}
				_ = json.Unmarshal(claims["https://api.openai.com/auth"], &oa)
				a.Plan = oa.Plan
			}
		}
	}
	return a
}

// ExpandHome turns a leading ~/ into the user's home directory.
func ExpandHome(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, rest)
		}
	}
	return p
}
