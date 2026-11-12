-- Approval comments handled (approval-comments-handled): the owner's decision reason on an approval (other than
-- the default) is a board comment on the approval's task, linked to the approval, so the comment-outcome rules
-- of owner-comments-handled apply to it and the approval shows its outcome.
ALTER TABLE task_comments ADD COLUMN approval_id text NOT NULL DEFAULT '';
