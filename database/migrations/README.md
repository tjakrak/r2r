# Historical prototype migrations

These numbered scripts describe the earlier `r2r` and `monitoring` schema design. They are retained only as development history and must not be executed against the current SQL Server 2019 production database.

The deployed application contract uses the manually provisioned `PCS` schema documented in `docs/production_sql_server_data_dictionary.md`, including:

- `PCS.AUDIT_EVENTS` instead of `audit_ledger_events`
- `PCS.OPERATIONAL_EVENTS` instead of a separate monitoring schema
- SQL Server 2019-compatible append-only audit permissions/trigger instead of SQL Server Ledger

Future production changes should start from a consolidated `PCS` baseline and use new migrations generated from that baseline.
