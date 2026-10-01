# Native configuration permissions

EnvironmentFile is read by systemd PID 1. Keep every .env file root:root mode
0600; services receive only the declared environment values.

The service process reads these non-environment files directly, so install them
with the listed owner, group, and mode:

| File | Owner and mode |
|---|---|
| /etc/candace/csf/database.json | root:candace-csf 0640 |
| /etc/candace/csf/clickhouse.xml and clickhouse-users.xml | root:candace-csf-clickhouse 0640 |
| /etc/candace/csf/opensearch/opensearch.yml | root:candace-csf-opensearch 0640 |

The matching static identities are declared in sysusers/candace-csf.conf. Do
not make these files world-readable.
