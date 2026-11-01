-- Stored catalogs (company-self-service D1): a company's catalog is a versioned set of documents (paths as
-- in the file layout). A version is an immutable full snapshot; the latest version is current (a rollback
-- saves an older version's documents as a new one).
CREATE TABLE catalog_versions (
 company_id text NOT NULL REFERENCES companies(id),
 version bigint NOT NULL,
 author text NOT NULL,
 note text NOT NULL DEFAULT '',
 created_at bigint NOT NULL,
 PRIMARY KEY (company_id, version)
);
CREATE TABLE catalog_documents (
 company_id text NOT NULL,
 version bigint NOT NULL,
 path text NOT NULL,
 content text NOT NULL,
 PRIMARY KEY (company_id, version, path),
 FOREIGN KEY (company_id, version) REFERENCES catalog_versions(company_id, version)
);
