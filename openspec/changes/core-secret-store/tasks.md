## 1. Store
- [ ] 1.1 Encrypted per-company secret table and a master key outside the database; owner API to set/delete/list names (write-only values).
- [ ] 1.2 Web UI: secrets page (names, last update, set/replace/delete).

## 2. Delivery
- [ ] 2.1 Resolve `${secret:NAME}` from Core's store at claim; deliver values only in the claim answer; node file as fallback.
- [ ] 2.2 Prove no value reaches RunSpec, snapshots, events, feed, logs or fixtures (tests that scan stored records for a planted value).
- [ ] 2.3 Feed events for secret writes and deliveries (names only).
