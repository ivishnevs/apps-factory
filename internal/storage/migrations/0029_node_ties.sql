-- node-ties: a task is tied to a node by its work only while its local work could not be saved remotely;
-- unpushed means exactly that now (the node reports it). wip_sha is the work saved to refs/factory/wip/<task>,
-- wip_unsaved why local work could not be saved, tied_since when the work tie began.
ALTER TABLE workspaces ADD COLUMN wip_sha text NOT NULL DEFAULT '';
ALTER TABLE workspaces ADD COLUMN wip_unsaved text NOT NULL DEFAULT '';
ALTER TABLE workspaces ADD COLUMN tied_since bigint NOT NULL DEFAULT 0;
UPDATE workspaces SET tied_since=updated_at WHERE unpushed=1;
