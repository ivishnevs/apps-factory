-- Node identity (node-identity 4.1, 4.2): nodes prove themselves with keys, so node tokens go. A rotation keeps the
-- proposed keys beside the active ones until an owner confirms their fingerprint.
DROP TABLE node_tokens;
ALTER TABLE node_keys ADD COLUMN next_sign_pub text NOT NULL DEFAULT '';
ALTER TABLE node_keys ADD COLUMN next_box_pub text NOT NULL DEFAULT '';
ALTER TABLE node_keys ADD COLUMN next_fingerprint text NOT NULL DEFAULT '';
ALTER TABLE node_keys ADD COLUMN next_at bigint NOT NULL DEFAULT 0;
