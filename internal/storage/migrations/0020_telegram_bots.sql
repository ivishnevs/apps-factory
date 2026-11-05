-- Telegram per company (telegram-per-company): a company's own bot. The token and the webhook secret are encrypted
-- under the company key (core-secret-store); the bot's username is kept in clear for links.
CREATE TABLE telegram_bots (
 company_id text PRIMARY KEY REFERENCES companies(id),
 sealed bytea NOT NULL,
 username text NOT NULL,
 created_by text NOT NULL,
 created_at bigint NOT NULL
);
