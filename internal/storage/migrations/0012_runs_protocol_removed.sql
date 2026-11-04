-- Every run is claimed through runtime protocol v2 (Store.ClaimV2); the v1 protocol is gone.
ALTER TABLE runs DROP COLUMN protocol;
