package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"appfactory/internal/secrets"
)

// A company's Telegram bot (telegram-per-company): its token and webhook secret sealed under the company key.

// TelegramBot is a company's bot with its token (only Core reads it).
type TelegramBot struct {
	Company    string `json:"company"`
	Token      string `json:"-"`
	HookSecret string `json:"-"`
	Username   string `json:"username"`
	CreatedBy  string `json:"created_by"`
	CreatedAt  int64  `json:"created_at"`
}

const telegramBotSecret = "telegram-bot"

// SetTelegramBot stores (or replaces) the company's bot. A new bot starts its updates afresh.
func (s *Store) SetTelegramBot(ctx context.Context, company string, bot TelegramBot, actor string) error {
	if bot.Token == "" || bot.HookSecret == "" || bot.Username == "" {
		return inputError("bot token, webhook secret and username required")
	}
	keys, err := s.keys()
	if err != nil {
		return err
	}
	tx, err := s.begin(ctx, company)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	key, err := companyKey(ctx, tx, keys, true)
	if err != nil {
		return fmt.Errorf("company key: %w", err)
	}
	plain, _ := json.Marshal(map[string]string{"token": bot.Token, "hook": bot.HookSecret})
	sealed, err := secrets.Encrypt(key, company, telegramBotSecret, 1, plain)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO telegram_bots(company_id,sealed,username,created_by,created_at) VALUES(?,?,?,?,?)
 ON CONFLICT (company_id) DO UPDATE SET sealed=excluded.sealed,username=excluded.username,created_by=excluded.created_by,created_at=excluded.created_at`,
		company, sealed, bot.Username, actor, tx.now().Unix()); err != nil {
		return err
	}
	// Update ids belong to a bot: a new bot's updates must not be taken for applied ones.
	if _, err = tx.ExecContext(ctx, "DELETE FROM notify_updates WHERE company_id=? AND channel='telegram'", company); err != nil {
		return err
	}
	return tx.Commit()
}

// TelegramBotOf returns the company's bot with its token; ErrNotFound when it has none.
func (s *Store) TelegramBotOf(ctx context.Context, company string) (TelegramBot, error) {
	b := TelegramBot{Company: company}
	var sealed []byte
	err := s.DB.QueryRowContext(ctx, "SELECT sealed,username,created_by,created_at FROM telegram_bots WHERE company_id=?", company).Scan(&sealed, &b.Username, &b.CreatedBy, &b.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return b, ErrNotFound
	}
	if err != nil {
		return b, err
	}
	keys, err := s.keys()
	if err != nil {
		return b, err
	}
	tx, err := s.begin(ctx, company)
	if err != nil {
		return b, err
	}
	defer tx.Rollback()
	key, err := companyKey(ctx, tx, keys, false)
	if err != nil {
		return b, fmt.Errorf("company key: %w", err)
	}
	plain, err := secrets.Decrypt(key, company, telegramBotSecret, 1, sealed)
	if err != nil {
		return b, err
	}
	var v map[string]string
	if err = json.Unmarshal(plain, &v); err != nil {
		return b, err
	}
	b.Token, b.HookSecret = v["token"], v["hook"]
	return b, nil
}

// TelegramBotCompanies lists the companies that have a bot.
func (s *Store) TelegramBotCompanies(ctx context.Context) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT company_id FROM telegram_bots ORDER BY company_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var c string
		if err = rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DeleteTelegramBot removes the company's bot and the chats bound to it.
func (s *Store) DeleteTelegramBot(ctx context.Context, company string) error {
	res, err := s.DB.ExecContext(ctx, "DELETE FROM telegram_bots WHERE company_id=?", company)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	_, err = s.DB.ExecContext(ctx, "DELETE FROM notify_state WHERE company_id=? AND channel='telegram'", company)
	return err
}
