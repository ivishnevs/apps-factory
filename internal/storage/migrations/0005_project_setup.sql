-- Project creation (project-creation D2): a project whose repository the node still has to set up has no
-- workspace yet, so the workspace is unique only once it is set.
ALTER TABLE projects DROP CONSTRAINT IF EXISTS projects_company_id_workspace_key;
CREATE UNIQUE INDEX projects_workspace ON projects(company_id, workspace) WHERE workspace <> '';
