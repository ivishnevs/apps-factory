-- saas-web-ui P3: when the person finished or skipped the setup wizard, so it opens once on any device.
ALTER TABLE users ADD COLUMN welcomed_at bigint NOT NULL DEFAULT 0;
