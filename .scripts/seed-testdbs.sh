#!/usr/bin/env bash
# Bring up the compose stack and seed each DB with two logical databases
# (where the engine supports them) plus a couple of sample tables. The
# per-tab activeCatalog flow needs something to switch between, and the
# explorer needs user objects under dbo/public/etc. so the Sys bucket
# isn't the only thing visible on connect.
#
# Usage:
#   ./scripts/seed-testdbs.sh                  # all services
#   ./scripts/seed-testdbs.sh mssql postgres   # subset
#
# Idempotent: re-runs are no-ops. Seeds go through `podman exec` into
# each service container so no host-side DB tooling is required.

set -euo pipefail
export MSYS_NO_PATHCONV=1

cd "$(dirname "$0")/.."

ALL_SERVICES=(mssql postgres mysql oracle firebird libsql sybase)
if [ $# -gt 0 ]; then
    SERVICES=("$@")
else
    SERVICES=("${ALL_SERVICES[@]}")
fi

log()  { printf '[seed] %s\n' "$*"; }
warn() { printf '[seed] WARN: %s\n' "$*" >&2; }

# wait_for <description> <cmd...> — polls until cmd exits 0 or 60 tries.
wait_for() {
    local desc="$1"; shift
    for i in $(seq 1 60); do
        if "$@" >/dev/null 2>&1; then
            return 0
        fi
        sleep 2
    done
    warn "$desc did not become ready after 120s"
    return 1
}

# --- mssql -----------------------------------------------------------------
# The mssql-init sidecar in compose.yaml already creates SqlgoA/SqlgoB
# with widgets/gadgets. Wait for it to finish, then verify.
seed_mssql() {
    log "mssql: waiting for server"
    wait_for "mssql" podman exec sqlgo-mssql \
        /opt/mssql-tools18/bin/sqlcmd -S localhost -U sa \
        -P 'SqlGo_dev_Pass1!' -C -Q "SELECT 1" || return 0

    log "mssql: ensuring SqlgoA/SqlgoB + sample tables"
    podman exec sqlgo-mssql /opt/mssql-tools18/bin/sqlcmd \
        -S localhost -U sa -P 'SqlGo_dev_Pass1!' -C -Q "
        IF DB_ID('SqlgoA') IS NULL CREATE DATABASE SqlgoA;
        IF DB_ID('SqlgoB') IS NULL CREATE DATABASE SqlgoB;" >/dev/null

    podman exec sqlgo-mssql /opt/mssql-tools18/bin/sqlcmd \
        -S localhost -d SqlgoA -U sa -P 'SqlGo_dev_Pass1!' -C -Q "
        IF OBJECT_ID('dbo.widgets','U') IS NULL
            CREATE TABLE dbo.widgets (id INT PRIMARY KEY, name NVARCHAR(50));
        IF NOT EXISTS (SELECT 1 FROM dbo.widgets)
            INSERT dbo.widgets VALUES (1,'alpha-A'),(2,'beta-A');" >/dev/null

    podman exec sqlgo-mssql /opt/mssql-tools18/bin/sqlcmd \
        -S localhost -d SqlgoB -U sa -P 'SqlGo_dev_Pass1!' -C -Q "
        IF OBJECT_ID('dbo.gadgets','U') IS NULL
            CREATE TABLE dbo.gadgets (id INT PRIMARY KEY, label NVARCHAR(50));
        IF NOT EXISTS (SELECT 1 FROM dbo.gadgets)
            INSERT dbo.gadgets VALUES (1,'gizmo-B'),(2,'widget-B');" >/dev/null
    log "mssql: done"
}

# --- postgres --------------------------------------------------------------
# Create sqlgo_a / sqlgo_b databases. The compose service's POSTGRES_DB
# seeds sqlgo_test; keep it for the base integration test path but add
# the two catalogs the cross-database flow expects.
seed_postgres() {
    log "postgres: waiting for server"
    wait_for "postgres" podman exec sqlgo-postgres \
        pg_isready -U sqlgo -d sqlgo_test || return 0

    log "postgres: ensuring sqlgo_a/sqlgo_b"
    # \gexec needs script/stdin input; psql -c doesn't parse backslash meta-commands.
    for db in sqlgo_a sqlgo_b; do
        exists=$(podman exec sqlgo-postgres psql -U sqlgo -d sqlgo_test -tAc \
            "SELECT 1 FROM pg_database WHERE datname='$db'")
        if [ "$exists" != "1" ]; then
            podman exec sqlgo-postgres psql -U sqlgo -d sqlgo_test -v ON_ERROR_STOP=1 \
                -c "CREATE DATABASE $db OWNER sqlgo" >/dev/null
        fi
    done

    podman exec sqlgo-postgres psql -U sqlgo -d sqlgo_a -v ON_ERROR_STOP=1 -c "
        CREATE TABLE IF NOT EXISTS public.widgets (id INT PRIMARY KEY, name TEXT);
        INSERT INTO public.widgets VALUES (1,'alpha-A'),(2,'beta-A')
        ON CONFLICT DO NOTHING;" >/dev/null

    podman exec sqlgo-postgres psql -U sqlgo -d sqlgo_b -v ON_ERROR_STOP=1 -c "
        CREATE TABLE IF NOT EXISTS public.gadgets (id INT PRIMARY KEY, label TEXT);
        INSERT INTO public.gadgets VALUES (1,'gizmo-B'),(2,'widget-B')
        ON CONFLICT DO NOTHING;" >/dev/null
    log "postgres: done"
}

# --- mysql -----------------------------------------------------------------
# MySQL "database" = schema. Create sqlgo_a/sqlgo_b schemas and grant
# the sqlgo user full rights on both.
seed_mysql() {
    log "mysql: waiting for server"
    wait_for "mysql" podman exec sqlgo-mysql \
        mysqladmin ping -h localhost -usqlgo -psqlgo_dev || return 0

    log "mysql: ensuring sqlgo_a/sqlgo_b"
    podman exec sqlgo-mysql mysql -uroot -psqlgo_dev -e "
        CREATE DATABASE IF NOT EXISTS sqlgo_a;
        CREATE DATABASE IF NOT EXISTS sqlgo_b;
        GRANT ALL ON sqlgo_a.* TO 'sqlgo'@'%';
        GRANT ALL ON sqlgo_b.* TO 'sqlgo'@'%';
        FLUSH PRIVILEGES;

        CREATE TABLE IF NOT EXISTS sqlgo_a.widgets (id INT PRIMARY KEY, name VARCHAR(50));
        INSERT IGNORE INTO sqlgo_a.widgets VALUES (1,'alpha-A'),(2,'beta-A');

        CREATE TABLE IF NOT EXISTS sqlgo_b.gadgets (id INT PRIMARY KEY, label VARCHAR(50));
        INSERT IGNORE INTO sqlgo_b.gadgets VALUES (1,'gizmo-B'),(2,'widget-B');
    " 2>&1 | grep -v "Using a password" || true
    log "mysql: done"
}

# --- oracle ----------------------------------------------------------------
# Oracle treats users == schemas; two schemas stand in for two DBs from
# the cross-catalog flow's perspective. Uses SYSTEM to create the users.
seed_oracle() {
    log "oracle: waiting for server (may take 60-120s on first boot)"
    wait_for "oracle" podman exec sqlgo-oracle \
        bash -c "echo 'SELECT 1 FROM dual;' | sqlplus -s system/sqlgo_dev@//localhost:1521/FREEPDB1" \
        || return 0

    log "oracle: ensuring SQLGO_A/SQLGO_B schemas"
    podman exec sqlgo-oracle bash -c "sqlplus -s system/sqlgo_dev@//localhost:1521/FREEPDB1 <<'SQL'
WHENEVER SQLERROR CONTINUE;
CREATE USER sqlgo_a IDENTIFIED BY sqlgo_dev QUOTA UNLIMITED ON USERS;
GRANT CONNECT, RESOURCE TO sqlgo_a;
CREATE USER sqlgo_b IDENTIFIED BY sqlgo_dev QUOTA UNLIMITED ON USERS;
GRANT CONNECT, RESOURCE TO sqlgo_b;
EXIT;
SQL" >/dev/null

    podman exec sqlgo-oracle bash -c "sqlplus -s sqlgo_a/sqlgo_dev@//localhost:1521/FREEPDB1 <<'SQL'
WHENEVER SQLERROR CONTINUE;
CREATE TABLE widgets (id NUMBER PRIMARY KEY, name VARCHAR2(50));
INSERT INTO widgets VALUES (1,'alpha-A');
INSERT INTO widgets VALUES (2,'beta-A');
COMMIT;
EXIT;
SQL" >/dev/null

    podman exec sqlgo-oracle bash -c "sqlplus -s sqlgo_b/sqlgo_dev@//localhost:1521/FREEPDB1 <<'SQL'
WHENEVER SQLERROR CONTINUE;
CREATE TABLE gadgets (id NUMBER PRIMARY KEY, label VARCHAR2(50));
INSERT INTO gadgets VALUES (1,'gizmo-B');
INSERT INTO gadgets VALUES (2,'widget-B');
COMMIT;
EXIT;
SQL" >/dev/null
    log "oracle: done"
}

# --- firebird --------------------------------------------------------------
# Firebird is file-per-DB; cross-catalog isn't a first-class concept, so
# just ensure widgets/gadgets exist in the seeded sqlgo_test.fdb.
seed_firebird() {
    log "firebird: waiting for server"
    wait_for "firebird" podman exec sqlgo-firebird \
        bash -c "echo 'SELECT 1 FROM RDB\$DATABASE;' | /opt/firebird/bin/isql -user sqlgo -password sqlgo_dev /var/lib/firebird/data/sqlgo_test.fdb" \
        || return 0

    log "firebird: ensuring widgets/gadgets"
    # isql parses `;` as a statement terminator and has no portable IF NOT EXISTS
    # for DDL, so create unconditionally and ignore the duplicate-table error on
    # re-runs. MERGE handles row-level idempotency.
    # isql aborts the rest of the script on the first failing statement, so
    # split DDL (tolerant) from DML (must succeed) into separate invocations.
    podman exec sqlgo-firebird bash -c "/opt/firebird/bin/isql -user sqlgo -password sqlgo_dev /var/lib/firebird/data/sqlgo_test.fdb <<'SQL'
SET AUTODDL ON;
CREATE TABLE widgets (id INTEGER PRIMARY KEY, name VARCHAR(50));
CREATE TABLE gadgets (id INTEGER PRIMARY KEY, label VARCHAR(50));
COMMIT;
SQL" >/dev/null 2>&1 || true
    podman exec sqlgo-firebird bash -c "/opt/firebird/bin/isql -user sqlgo -password sqlgo_dev /var/lib/firebird/data/sqlgo_test.fdb <<'SQL'
MERGE INTO widgets w USING (SELECT 1 id, 'alpha' name FROM RDB\$DATABASE UNION SELECT 2,'beta' FROM RDB\$DATABASE) s
  ON w.id=s.id WHEN NOT MATCHED THEN INSERT (id,name) VALUES (s.id,s.name);
MERGE INTO gadgets g USING (SELECT 1 id, 'gizmo' label FROM RDB\$DATABASE UNION SELECT 2,'widget' FROM RDB\$DATABASE) s
  ON g.id=s.id WHEN NOT MATCHED THEN INSERT (id,label) VALUES (s.id,s.label);
COMMIT;
SQL" >/dev/null 2>&1 || warn "firebird merge may have failed"
    log "firebird: done"
}

# --- libsql ----------------------------------------------------------------
# libsql-server runs one embedded DB; cross-catalog not applicable. Seed
# one sample table via the hrana HTTP endpoint so the explorer isn't
# empty on connect.
seed_libsql() {
    log "libsql: waiting for server"
    # libsql-server image has no curl/wget; probe the hrana port via bash /dev/tcp.
    wait_for "libsql" podman exec sqlgo-libsql \
        bash -c ': </dev/tcp/localhost/8080' || return 0

    log "libsql: ensuring widgets"
    # Seed via host-side curl against the published port (18080 by default).
    # SQLD_AUTH_JWT_KEY unset in compose.yaml → no auth header needed.
    local port
    port=$(podman port sqlgo-libsql 8080/tcp 2>/dev/null | head -1 | awk -F: '{print $NF}')
    port="${port:-18080}"
    curl -sS -H 'Content-Type: application/json' \
        -d '{"requests":[{"type":"execute","stmt":{"sql":"CREATE TABLE IF NOT EXISTS widgets (id INTEGER PRIMARY KEY, name TEXT)"}},{"type":"execute","stmt":{"sql":"INSERT OR IGNORE INTO widgets VALUES (1,'"'"'alpha'"'"'),(2,'"'"'beta'"'"')"}},{"type":"close"}]}' \
        "http://localhost:${port}/v2/pipeline" >/dev/null || warn "libsql seed may have failed"
    log "libsql: done"
}

# --- sybase ----------------------------------------------------------------
# ASE supports multiple user DBs; create sqlgo_a/sqlgo_b alongside the
# preseeded testdb. Uses isql over TDS 5.0 inside the container. sa
# password on the datagrip image is blank.
seed_sybase() {
    log "sybase: waiting for server"
    # isql needs SYBASE env to find its localization files; neither login
    # shell nor /etc/profile.d sets it, so source SYBASE.sh explicitly.
    wait_for "sybase" podman exec sqlgo-sybase \
        bash -c ". /opt/sybase/SYBASE.sh && printf 'select 1\ngo\n' | /opt/sybase/OCS-16_0/bin/isql -U sa -P myPassword -S MYSYBASE" \
        || return 0

    log "sybase: ensuring sqlgo_a/sqlgo_b + sample tables"
    podman exec sqlgo-sybase bash -c ". /opt/sybase/SYBASE.sh && /opt/sybase/OCS-16_0/bin/isql -U sa -P myPassword -S MYSYBASE <<'SQL'
-- Entrypoint sizes master at 60MB and consumes 48MB for testdb. Expand
-- to fit two more 24MB user DBs (model minimum size).
disk resize name='master', size='180m'
go
if not exists (select 1 from master..sysdatabases where name='sqlgo_a')
  create database sqlgo_a on master='24m'
go
if not exists (select 1 from master..sysdatabases where name='sqlgo_b')
  create database sqlgo_b on master='24m'
go
sp_dboption sqlgo_a, 'select into/bulkcopy', true
go
sp_dboption sqlgo_b, 'select into/bulkcopy', true
go
use sqlgo_a
go
if not exists (select 1 from sysusers where name='tester')
  exec sp_adduser tester
go
if not exists (select 1 from sysobjects where name='widgets' and type='U')
  create table widgets (id int primary key, name varchar(50))
go
if not exists (select 1 from widgets) insert widgets values (1,'alpha-A')
go
if not exists (select 1 from widgets where id=2) insert widgets values (2,'beta-A')
go
use sqlgo_b
go
if not exists (select 1 from sysusers where name='tester')
  exec sp_adduser tester
go
if not exists (select 1 from sysobjects where name='gadgets' and type='U')
  create table gadgets (id int primary key, label varchar(50))
go
if not exists (select 1 from gadgets) insert gadgets values (1,'gizmo-B')
go
if not exists (select 1 from gadgets where id=2) insert gadgets values (2,'widget-B')
go
SQL" >/dev/null 2>&1 || warn "sybase seed may have partially failed (check sa creds / isql path)"
    log "sybase: done"
}

log "starting services: ${SERVICES[*]}"
podman compose up -d "${SERVICES[@]}"

for svc in "${SERVICES[@]}"; do
    case "$svc" in
        mssql)     seed_mssql ;;
        postgres)  seed_postgres ;;
        mysql)     seed_mysql ;;
        oracle)    seed_oracle ;;
        firebird)  seed_firebird ;;
        libsql)    seed_libsql ;;
        sybase)    seed_sybase ;;
        sshd|mssql-init) : ;;
        *) warn "unknown service: $svc" ;;
    esac
done

# --- register sqlgo connections --------------------------------------------
# Save a connection entry in the sqlgo store for each seeded service so the
# TUI picker already lists them.
SQLGO="go run -tags sqlite_fts5 ./cmd/sqlgo"
register() {
    local name="$1" pass="$2"; shift 2
    if [ -n "$pass" ]; then
        echo "$pass" | $SQLGO conns add "$name" --force --keyring=false --password-stdin "$@" \
            || warn "register failed: $name"
    else
        $SQLGO conns add "$name" --force --keyring=false "$@" \
            || warn "register failed: $name"
    fi
}

log "registering sqlgo connections"
for svc in "${SERVICES[@]}"; do
    case "$svc" in
        mssql)
            register "Dev MSSQL" 'SqlGo_dev_Pass1!' \
                --driver mssql --host localhost --port 11433 \
                --user sa --option encrypt=disable
            ;;
        postgres)
            register "Dev Postgres" sqlgo_dev \
                --driver postgres --host localhost --port 15432 \
                --user sqlgo --database sqlgo_a --option sslmode=disable
            ;;
        mysql)
            register "Dev MySQL" sqlgo_dev \
                --driver mysql --host localhost --port 13306 \
                --user sqlgo --database sqlgo_a
            ;;
        oracle)
            register "Dev Oracle" sqlgo_dev \
                --driver oracle --host localhost --port 11521 \
                --user system --database FREEPDB1
            ;;
        firebird)
            register "Dev Firebird" sqlgo_dev \
                --driver firebird --host localhost --port 13050 \
                --user sqlgo --database /var/lib/firebird/data/sqlgo_test.fdb
            ;;
        libsql)
            register "Dev libSQL" "" \
                --driver libsql --host "http://localhost:18080" --port 0
            ;;
        sybase)
            register "Dev Sybase" myPassword \
                --driver sybase --host localhost --port 15000 \
                --user sa
            ;;
    esac
done

log "all services seeded"
