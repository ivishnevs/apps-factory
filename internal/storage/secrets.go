package storage

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"appfactory/internal/config"
	"appfactory/internal/protocol"
	"appfactory/internal/secrets"
)

// Company secrets (core-secret-store D1, D2): values encrypted under the company key, which the master key wraps.
// Nothing here returns a value except SecretValues, which the claim uses to seal values to a node.

// Secret kinds.
const (
	SecretString = "string"
	SecretFile   = "file"
)

// Secret limits (D2): values up to 64 KiB; a string shorter than 8 bytes is refused, because redaction ignores
// shorter values.
const (
	MaxSecretBytes  = 64 << 10
	MinSecretString = 8
)

var secretName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`) // as protocol.SecretRef

// SecretInfo is a secret without its value.
type SecretInfo struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Version   int64  `json:"version"`
	UpdatedBy string `json:"updated_by"`
	UpdatedAt int64  `json:"updated_at"`
}

// SecretValue is a decrypted value for a delivery.
type SecretValue struct {
	Name    string
	Kind    string
	Version int64
	Value   []byte
}

// ValidSecretName reports whether name may name a secret.
func ValidSecretName(name string) bool { return secretName.MatchString(name) }

func (s *Store) keys() (secrets.KeyProvider, error) {
	if s.Keys == nil {
		return nil, secrets.ErrNoMasterKey
	}
	return s.Keys, nil
}

// companyKey returns the company's key, creating it (wrapped by the master key) with create.
func companyKey(ctx context.Context, tx *Tx, keys secrets.KeyProvider, create bool) ([]byte, error) {
	var wrapped []byte
	err := tx.QueryRowContext(ctx, "SELECT wrapped FROM company_keys WHERE company_id=?", tx.company).Scan(&wrapped)
	if errors.Is(err, sql.ErrNoRows) {
		if !create {
			return nil, ErrNotFound
		}
		key := secrets.NewCompanyKey()
		if wrapped, err = keys.Wrap(key); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO company_keys(company_id,wrapped,created_at) VALUES(?,?,?)", tx.company, wrapped, tx.now().Unix()); err != nil {
			return nil, err
		}
		return key, nil
	}
	if err != nil {
		return nil, err
	}
	return keys.Unwrap(wrapped)
}

// SetSecret stores value as the next version of the company's secret name (D2): a string of 8 bytes to 64 KiB of
// UTF-8, or a file of up to 64 KiB.
func (s *Store) SetSecret(ctx context.Context, company, name, kind string, value []byte, actor string) (SecretInfo, error) {
	var out SecretInfo
	switch {
	case !ValidSecretName(name):
		return out, inputError("secret name: a letter or _ then letters, digits or _, at most 128")
	case kind != SecretString && kind != SecretFile:
		return out, inputError("secret kind string or file required")
	case len(value) > MaxSecretBytes:
		return out, inputError("secret values are at most 64 KiB")
	case kind == SecretString && (len(value) < MinSecretString || !utf8.Valid(value)):
		return out, inputError("a string secret is UTF-8 of at least 8 bytes (shorter values are not redacted)")
	case kind == SecretFile && len(value) == 0:
		return out, inputError("a file secret is not empty")
	case actor == "":
		return out, inputError("actor required")
	}
	keys, err := s.keys()
	if err != nil {
		return out, err
	}
	tx, err := s.begin(ctx, company)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	key, err := companyKey(ctx, tx, keys, true)
	if err != nil {
		return out, fmt.Errorf("company key: %w", err)
	}
	var version int64
	if err = tx.QueryRowContext(ctx, "SELECT coalesce(max(version),0) FROM company_secrets WHERE company_id=? AND name=?", company, name).Scan(&version); err != nil {
		return out, err
	}
	version++
	sealed, err := secrets.Encrypt(key, company, name, version, value)
	if err != nil {
		return out, err
	}
	now := tx.now().Unix()
	if _, err = tx.ExecContext(ctx, `INSERT INTO company_secrets(company_id,name,kind,version,ciphertext,updated_by,updated_at) VALUES(?,?,?,?,?,?,?)
 ON CONFLICT (company_id,name) DO UPDATE SET kind=excluded.kind,version=excluded.version,ciphertext=excluded.ciphertext,updated_by=excluded.updated_by,updated_at=excluded.updated_at`,
		company, name, kind, version, sealed, actor, now); err != nil {
		return out, err
	}
	out = SecretInfo{Name: name, Kind: kind, Version: version, UpdatedBy: actor, UpdatedAt: now}
	if err = feed(ctx, tx, FeedItem{EventID: protocol.ID(), Type: "secret.set", EntityID: "secret:" + name, CreatedAt: tx.now(), Actor: actor,
		Summary: fmt.Sprintf("secret %s set (%s, version %d)", name, kind, version)}); err != nil {
		return out, err
	}
	tx.wakeNodes() // tasks waiting for the secret (D9) are dispatched at the next tick
	return out, tx.Commit()
}

// Secrets lists the company's secrets by name, without values.
func (s *Store) Secrets(ctx context.Context, company string) ([]SecretInfo, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT name,kind,version,updated_by,updated_at FROM company_secrets WHERE company_id=? ORDER BY name", company)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SecretInfo{}
	for rows.Next() {
		var v SecretInfo
		if err = rows.Scan(&v.Name, &v.Kind, &v.Version, &v.UpdatedBy, &v.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// SecretKinds is the kind of every set secret of the company, by name.
func (s *Store) SecretKinds(ctx context.Context, company string) (map[string]string, error) {
	list, err := s.Secrets(ctx, company)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, v := range list {
		out[v.Name] = v.Kind
	}
	return out, nil
}

// DeleteSecret removes the company's secret; a secret that is not set is ErrNotFound.
func (s *Store) DeleteSecret(ctx context.Context, company, name, actor string) error {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var kind string
	var version int64
	err = tx.QueryRowContext(ctx, "DELETE FROM company_secrets WHERE company_id=? AND name=? RETURNING kind,version", company, name).Scan(&kind, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if err = feed(ctx, tx, FeedItem{EventID: protocol.ID(), Type: "secret.deleted", EntityID: "secret:" + name, CreatedAt: tx.now(), Actor: actor,
		Summary: fmt.Sprintf("secret %s deleted (%s, version %d)", name, kind, version)}); err != nil {
		return err
	}
	return tx.Commit()
}

// SecretValues decrypts the company's secrets names for a delivery (D4). A name that is not set is reported in
// missing; a value that does not decrypt is an error naming the secret, never a value.
func (s *Store) SecretValues(ctx context.Context, company string, names []string) (map[string]SecretValue, []string, error) {
	out := map[string]SecretValue{}
	if len(names) == 0 {
		return out, nil, nil
	}
	keys, err := s.keys()
	if err != nil {
		return nil, nil, err
	}
	tx, err := s.begin(ctx, company)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	return secretValues(ctx, tx, keys, names)
}

func secretValues(ctx context.Context, tx *Tx, keys secrets.KeyProvider, names []string) (map[string]SecretValue, []string, error) {
	out := map[string]SecretValue{}
	var missing []string
	var key []byte
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	for _, name := range sorted {
		var v SecretValue
		var sealed []byte
		err := tx.QueryRowContext(ctx, "SELECT kind,version,ciphertext FROM company_secrets WHERE company_id=? AND name=?", tx.company, name).Scan(&v.Kind, &v.Version, &sealed)
		if errors.Is(err, sql.ErrNoRows) {
			missing = append(missing, name)
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		if key == nil {
			if key, err = companyKey(ctx, tx, keys, false); err != nil {
				return nil, nil, fmt.Errorf("secret %s: company key: %w", name, err)
			}
		}
		if v.Value, err = secrets.Decrypt(key, tx.company, name, v.Version, sealed); err != nil {
			return nil, nil, fmt.Errorf("secret %s: %w", name, err)
		}
		v.Name = name
		out[name] = v
	}
	return out, missing, nil
}

// CheckMasterKey makes sure the master key opens every company key (D1: Core refuses to start with a key that
// cannot unwrap the existing ones). Without a master key it reports whether any company key exists.
func (s *Store) CheckMasterKey(ctx context.Context) error {
	rows, err := s.DB.QueryContext(ctx, "SELECT company_id,wrapped FROM company_keys ORDER BY company_id")
	if err != nil {
		return err
	}
	defer rows.Close()
	var bad []string
	for rows.Next() {
		var company string
		var wrapped []byte
		if err = rows.Scan(&company, &wrapped); err != nil {
			return err
		}
		if s.Keys == nil {
			return fmt.Errorf("%w; company %s has secrets", secrets.ErrNoMasterKey, company)
		}
		if _, err = s.Keys.Unwrap(wrapped); err != nil {
			bad = append(bad, company)
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if len(bad) > 0 {
		return fmt.Errorf("%s does not open the company keys of %s: wrong or old master key", secrets.MasterKeyEnv, strings.Join(bad, ", "))
	}
	return nil
}

// RotateMasterKey re-wraps every company key under the new master key in one transaction (D1, rotate-master).
func (s *Store) RotateMasterKey(ctx context.Context, keys *secrets.MasterKeys) (int, error) {
	if !keys.Rotating() {
		return 0, inputError("set " + secrets.NewMasterKeyEnv + " on Core to rotate the master key")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT company_id,wrapped FROM company_keys ORDER BY company_id FOR UPDATE")
	if err != nil {
		return 0, err
	}
	type row struct {
		company string
		wrapped []byte
	}
	var all []row
	for rows.Next() {
		var r row
		if err = rows.Scan(&r.company, &r.wrapped); err != nil {
			rows.Close()
			return 0, err
		}
		all = append(all, r)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return 0, err
	}
	for _, r := range all {
		w, err := keys.RewrapUnder(r.wrapped)
		if err != nil {
			return 0, fmt.Errorf("company %s: %w", r.company, err)
		}
		if _, err = tx.ExecContext(ctx, "UPDATE company_keys SET wrapped=$1,key_version=key_version+1 WHERE company_id=$2", w, r.company); err != nil {
			return 0, err
		}
	}
	return len(all), tx.Commit()
}

// runSecretNeeds are the secrets a run needs, from the agent snapshot it carries (D3).
func runSecretNeeds(spec protocol.RunSpec) []config.SecretNeed {
	if len(spec.ConfigSnapshot) == 0 {
		return nil
	}
	var a config.Agent
	if json.Unmarshal(spec.ConfigSnapshot, &a) != nil {
		return nil
	}
	return a.NeededSecrets()
}

// checkRunSecrets says why a run cannot get its secrets ("secret_missing: NAME", wrong kind, unreadable), "" when
// every one is set with its kind and opens.
func checkRunSecrets(ctx context.Context, tx *Tx, keys secrets.KeyProvider, needed []config.SecretNeed) (string, error) {
	names := secretNames(needed)
	vals, missing, err := secretValues(ctx, tx, keys, names)
	if errors.Is(err, secrets.ErrDecrypt) || errors.Is(err, ErrNotFound) {
		return "secret_unreadable: " + err.Error(), nil
	}
	if err != nil {
		return "", err
	}
	if len(missing) > 0 {
		return "secret_missing: " + missing[0], nil
	}
	for _, n := range needed {
		if v := vals[n.Name]; v.Kind != n.Kind {
			return fmt.Sprintf("secret_wrong_kind: %s is a %s, the run needs a %s", n.Name, v.Kind, n.Kind), nil
		}
	}
	return "", nil
}

func secretNames(needed []config.SecretNeed) []string {
	var names []string
	for _, n := range needed {
		if len(names) == 0 || names[len(names)-1] != n.Name {
			names = append(names, n.Name)
		}
	}
	return names
}

// sealRunSecrets seals the run's secrets to its node's active encryption key (D4); none when the run needs none.
func sealRunSecrets(ctx context.Context, tx *Tx, company, run string, spec protocol.RunSpec) ([]protocol.SealedSecret, error) {
	needed := runSecretNeeds(spec)
	if len(needed) == 0 {
		return nil, nil
	}
	keys, err := tx.store.keys()
	if err != nil {
		return nil, err
	}
	var node, boxPub string
	if err = tx.QueryRowContext(ctx, "SELECT node_id FROM runs WHERE company_id=? AND id=?", company, run).Scan(&node); err != nil {
		return nil, err
	}
	if err = tx.QueryRowContext(ctx, "SELECT box_pub FROM node_keys WHERE company_id=? AND node_id=? AND status=?", company, node, NodeKeyActive).Scan(&boxPub); err != nil {
		return nil, fmt.Errorf("node %s has no active encryption key: %w", node, err)
	}
	pub, err := hex.DecodeString(boxPub)
	if err != nil {
		return nil, err
	}
	vals, missing, err := secretValues(ctx, tx, keys, secretNames(needed))
	if err != nil {
		return nil, err
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("secret %s is not set", missing[0])
	}
	out := []protocol.SealedSecret{}
	for _, name := range secretNames(needed) {
		v := vals[name]
		sealed, err := secrets.SealToNode(pub, secrets.DeliveryInfo(company, run, node, name, v.Version), v.Value)
		if err != nil {
			return nil, err
		}
		out = append(out, protocol.SealedSecret{Name: name, Kind: v.Kind, Version: v.Version, Sealed: sealed})
	}
	return out, nil
}
