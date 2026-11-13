# Tenant isolation test suite (Master Spec §13)

RLS is enforced at the database and verified by tests. Procedure (against
`make dev-infra` + `make migrate`):

1. Create tenants A and B; create a client under each.
2. Connect as the app role; `SET app.tenant_id = <A>`; assert
   `SELECT count(*) FROM clients` sees only A's rows; same for B.
3. Attempt cross-tenant write: as A, `INSERT INTO preferences (tenant_id, ...)`
   with B's tenant id — must violate the WITH CHECK policy.
4. Assert `outbox` is invisible without `SET app.platform_reader = 'on'`.
5. Repeat for every tenant-scoped table in `db/migrations/0001_init.sql`.

The same checks run in CI as `go test -tags pg ./internal/store/...` against
an ephemeral Postgres (service container).
