-- node-packaging-updates (design.md parts B and C, task 3.6): a node reports its package version (Core gives no work to a
-- node below min_node_version) and the labels it detects itself (os, arch, xcode, ios-sim, headless, gpu), which
-- select like the labels its node.yaml sets. config_report is the node's last report of the settings from the web it
-- applied (version, per setting applied or refused, node.yaml's own values).
ALTER TABLE nodes ADD COLUMN version text NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN detected_labels jsonb NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE nodes ADD COLUMN config_report jsonb NOT NULL DEFAULT '{}'::jsonb;

-- The desired settings of a node, edited on the Nodes page (PUT /nodes/{id}/config); version grows with every change
-- and reaches the node in its poll answer. The row goes with its node.
CREATE TABLE node_configs (
 company_id text NOT NULL,
 node_id text NOT NULL,
 version bigint NOT NULL,
 config jsonb NOT NULL,
 updated_by text NOT NULL,
 updated_at bigint NOT NULL,
 PRIMARY KEY (company_id, node_id),
 FOREIGN KEY (company_id, node_id) REFERENCES nodes(company_id, id) ON DELETE CASCADE
);
