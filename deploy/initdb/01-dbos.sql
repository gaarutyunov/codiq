-- DBOS Transact's system database (SPEC.md §9, §10).
--
-- DORMANT UNTIL M3. Nothing reads or writes it before then; it is created now
-- because `docker-entrypoint-initdb.d` runs exactly once, on an empty data
-- directory. Adding it at M3 instead would mean either a `docker compose down -v`
-- — destroying the graph — or an out-of-band CREATE DATABASE that this file is
-- supposed to make unnecessary.
--
-- It is a *separate database on the same instance*, not a schema and not a
-- separate server: DBOS checkpoints every step of the batch workflow, and those
-- writes must not contend with the reduce phase's bulk CopyFrom into the graph
-- tables (§9 "Isolation"). Same instance, because §9's other half is "no extra
-- infra" — DBOS is an embedded library over the Postgres CodiQ already runs.
--
-- CREATE DATABASE cannot run inside a transaction block, so this file must stay
-- free of BEGIN/COMMIT. The entrypoint runs it with psql, which is fine.
CREATE DATABASE codiq_dbos;

COMMENT ON DATABASE codiq_dbos IS
  'DBOS Transact system tables (CodiQ SPEC.md §9). Dormant until M3.';
