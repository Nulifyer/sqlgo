# seed-testdbs.ps1 -- Bring up the compose stack and seed each test DB
# with two logical databases (where the engine supports them) plus a
# couple of sample tables. The per-tab activeCatalog flow needs
# something to switch between, and the explorer needs user objects so
# the Sys bucket isn't the only thing visible on connect.
#
# Usage:
#   .\.scripts\seed-testdbs.ps1                  # all services
#   .\.scripts\seed-testdbs.ps1 mssql postgres   # subset
#
# Idempotent: re-runs are no-ops. Seeds go through `podman exec` into
# each service container so no host-side DB tooling is required.

param(
    [Parameter(ValueFromRemainingArguments=$true)]
    [string[]]$Services
)

$ErrorActionPreference = "Stop"

$repoRoot = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
Push-Location $repoRoot

$allServices = @("mssql","postgres","mysql","oracle","firebird","libsql","sybase")
if (-not $Services -or $Services.Count -eq 0) {
    $Services = $allServices
}

function Write-Log($msg)  { Write-Host "[seed] $msg" -ForegroundColor Cyan }
function Write-Warn($msg) { Write-Host "[seed] WARN: $msg" -ForegroundColor Yellow }

# Wait-For polls until the scriptblock exits 0 (no throw + nonzero
# LASTEXITCODE treated as failure) or 60 tries (~120s) have elapsed.
function Wait-For($desc, [scriptblock]$check) {
    for ($i = 1; $i -le 60; $i++) {
        try {
            & $check *> $null
            if ($LASTEXITCODE -eq 0 -or $null -eq $LASTEXITCODE) { return $true }
        } catch { }
        Start-Sleep -Seconds 2
    }
    Write-Warn "$desc did not become ready after 120s"
    return $false
}

# Exec-In runs a command inside a service container via `podman exec`.
# Stdin content (for here-doc style input) is piped via the $stdin arg.
function Exec-In($container, $argsList, $stdin = $null) {
    if ($null -ne $stdin) {
        $stdin | podman exec -i $container @argsList 2>&1
    } else {
        podman exec $container @argsList 2>&1
    }
}

# --- mssql -----------------------------------------------------------------
# The mssql-init sidecar in compose.yaml already creates SqlgoA/SqlgoB
# with widgets/gadgets. This re-asserts to keep the PS path authoritative.
function Seed-Mssql {
    Write-Log "mssql: waiting for server"
    $ok = Wait-For "mssql" { podman exec sqlgo-mssql /opt/mssql-tools18/bin/sqlcmd -S localhost -U sa -P 'SqlGo_dev_Pass1!' -C -Q "SELECT 1" }
    if (-not $ok) { return }

    Write-Log "mssql: ensuring SqlgoA/SqlgoB + sample tables"
    podman exec sqlgo-mssql /opt/mssql-tools18/bin/sqlcmd -S localhost -U sa -P 'SqlGo_dev_Pass1!' -C -Q @"
IF DB_ID('SqlgoA') IS NULL CREATE DATABASE SqlgoA;
IF DB_ID('SqlgoB') IS NULL CREATE DATABASE SqlgoB;
"@ | Out-Null

    podman exec sqlgo-mssql /opt/mssql-tools18/bin/sqlcmd -S localhost -d SqlgoA -U sa -P 'SqlGo_dev_Pass1!' -C -Q @"
IF OBJECT_ID('dbo.widgets','U') IS NULL
    CREATE TABLE dbo.widgets (id INT PRIMARY KEY, name NVARCHAR(50));
IF NOT EXISTS (SELECT 1 FROM dbo.widgets)
    INSERT dbo.widgets VALUES (1,'alpha-A'),(2,'beta-A');
"@ | Out-Null

    podman exec sqlgo-mssql /opt/mssql-tools18/bin/sqlcmd -S localhost -d SqlgoB -U sa -P 'SqlGo_dev_Pass1!' -C -Q @"
IF OBJECT_ID('dbo.gadgets','U') IS NULL
    CREATE TABLE dbo.gadgets (id INT PRIMARY KEY, label NVARCHAR(50));
IF NOT EXISTS (SELECT 1 FROM dbo.gadgets)
    INSERT dbo.gadgets VALUES (1,'gizmo-B'),(2,'widget-B');
"@ | Out-Null
    Write-Log "mssql: done"
}

# --- postgres --------------------------------------------------------------
# Create sqlgo_a / sqlgo_b databases alongside the POSTGRES_DB-seeded
# sqlgo_test so the cross-catalog flow has two DBs to switch between.
function Seed-Postgres {
    Write-Log "postgres: waiting for server"
    $ok = Wait-For "postgres" { podman exec sqlgo-postgres pg_isready -U sqlgo -d sqlgo_test }
    if (-not $ok) { return }

    Write-Log "postgres: ensuring sqlgo_a/sqlgo_b"
    # \gexec only parsed from script/stdin; psql -c doesn't handle backslash commands.
    foreach ($db in @('sqlgo_a','sqlgo_b')) {
        $exists = podman exec sqlgo-postgres psql -U sqlgo -d sqlgo_test -tAc "SELECT 1 FROM pg_database WHERE datname='$db'"
        if ("$exists".Trim() -ne '1') {
            podman exec sqlgo-postgres psql -U sqlgo -d sqlgo_test -v ON_ERROR_STOP=1 -c "CREATE DATABASE $db OWNER sqlgo" | Out-Null
        }
    }

    podman exec sqlgo-postgres psql -U sqlgo -d sqlgo_a -v ON_ERROR_STOP=1 -c @"
CREATE TABLE IF NOT EXISTS public.widgets (id INT PRIMARY KEY, name TEXT);
INSERT INTO public.widgets VALUES (1,'alpha-A'),(2,'beta-A') ON CONFLICT DO NOTHING;
"@ | Out-Null

    podman exec sqlgo-postgres psql -U sqlgo -d sqlgo_b -v ON_ERROR_STOP=1 -c @"
CREATE TABLE IF NOT EXISTS public.gadgets (id INT PRIMARY KEY, label TEXT);
INSERT INTO public.gadgets VALUES (1,'gizmo-B'),(2,'widget-B') ON CONFLICT DO NOTHING;
"@ | Out-Null
    Write-Log "postgres: done"
}

# --- mysql -----------------------------------------------------------------
# MySQL "database" = schema. Create sqlgo_a/sqlgo_b and grant the sqlgo
# user full rights on both.
function Seed-Mysql {
    Write-Log "mysql: waiting for server"
    $ok = Wait-For "mysql" { podman exec sqlgo-mysql mysqladmin ping -h localhost -usqlgo -psqlgo_dev }
    if (-not $ok) { return }

    Write-Log "mysql: ensuring sqlgo_a/sqlgo_b"
    podman exec sqlgo-mysql mysql -uroot -psqlgo_dev -e @"
CREATE DATABASE IF NOT EXISTS sqlgo_a;
CREATE DATABASE IF NOT EXISTS sqlgo_b;
GRANT ALL ON sqlgo_a.* TO 'sqlgo'@'%';
GRANT ALL ON sqlgo_b.* TO 'sqlgo'@'%';
FLUSH PRIVILEGES;
CREATE TABLE IF NOT EXISTS sqlgo_a.widgets (id INT PRIMARY KEY, name VARCHAR(50));
INSERT IGNORE INTO sqlgo_a.widgets VALUES (1,'alpha-A'),(2,'beta-A');
CREATE TABLE IF NOT EXISTS sqlgo_b.gadgets (id INT PRIMARY KEY, label VARCHAR(50));
INSERT IGNORE INTO sqlgo_b.gadgets VALUES (1,'gizmo-B'),(2,'widget-B');
"@ 2>&1 | Where-Object { $_ -notmatch "Using a password" } | Out-Null
    Write-Log "mysql: done"
}

# --- oracle ----------------------------------------------------------------
# Oracle treats users == schemas; two schemas stand in for two DBs from
# the cross-catalog flow's perspective.
function Seed-Oracle {
    Write-Log "oracle: waiting for server (may take 60-120s on first boot)"
    $ok = Wait-For "oracle" {
        podman exec sqlgo-oracle bash -c "echo 'SELECT 1 FROM dual;' | sqlplus -s system/sqlgo_dev@//localhost:1521/FREEPDB1"
    }
    if (-not $ok) { return }

    Write-Log "oracle: ensuring SQLGO_A/SQLGO_B schemas"
    $createUsers = @"
WHENEVER SQLERROR CONTINUE;
CREATE USER sqlgo_a IDENTIFIED BY sqlgo_dev QUOTA UNLIMITED ON USERS;
GRANT CONNECT, RESOURCE TO sqlgo_a;
CREATE USER sqlgo_b IDENTIFIED BY sqlgo_dev QUOTA UNLIMITED ON USERS;
GRANT CONNECT, RESOURCE TO sqlgo_b;
EXIT;
"@
    $createUsers | podman exec -i sqlgo-oracle sqlplus -s "system/sqlgo_dev@//localhost:1521/FREEPDB1" | Out-Null

    $seedA = @"
WHENEVER SQLERROR CONTINUE;
CREATE TABLE widgets (id NUMBER PRIMARY KEY, name VARCHAR2(50));
INSERT INTO widgets VALUES (1,'alpha-A');
INSERT INTO widgets VALUES (2,'beta-A');
COMMIT;
EXIT;
"@
    $seedA | podman exec -i sqlgo-oracle sqlplus -s "sqlgo_a/sqlgo_dev@//localhost:1521/FREEPDB1" | Out-Null

    $seedB = @"
WHENEVER SQLERROR CONTINUE;
CREATE TABLE gadgets (id NUMBER PRIMARY KEY, label VARCHAR2(50));
INSERT INTO gadgets VALUES (1,'gizmo-B');
INSERT INTO gadgets VALUES (2,'widget-B');
COMMIT;
EXIT;
"@
    $seedB | podman exec -i sqlgo-oracle sqlplus -s "sqlgo_b/sqlgo_dev@//localhost:1521/FREEPDB1" | Out-Null
    Write-Log "oracle: done"
}

# --- firebird --------------------------------------------------------------
# Firebird is file-per-DB; cross-catalog isn't a first-class concept, so
# just ensure widgets/gadgets exist in the seeded sqlgo_test.fdb.
function Seed-Firebird {
    Write-Log "firebird: waiting for server"
    $ok = Wait-For "firebird" {
        podman exec sqlgo-firebird bash -c "echo 'SELECT 1 FROM RDB`$DATABASE;' | /opt/firebird/bin/isql -user sqlgo -password sqlgo_dev /var/lib/firebird/data/sqlgo_test.fdb"
    }
    if (-not $ok) { return }

    Write-Log "firebird: ensuring widgets/gadgets"
    # isql parses `;` as a statement terminator and has no portable IF NOT EXISTS
    # for DDL, so create unconditionally and ignore the duplicate-table error on
    # re-runs. MERGE handles row-level idempotency.
    # PowerShell's stdin pipe to `podman exec -i isql` silently fails to
    # execute MERGE statements (isql returns 0 but the DML never runs).
    # Shell out to bash -c inside the container with a heredoc instead,
    # which is the pattern the .sh sibling already uses.
    $ddl = @'
SET AUTODDL ON;
CREATE TABLE widgets (id INTEGER PRIMARY KEY, name VARCHAR(50));
CREATE TABLE gadgets (id INTEGER PRIMARY KEY, label VARCHAR(50));
COMMIT;
'@
    $dml = @'
MERGE INTO widgets w USING (SELECT 1 id, 'alpha' name FROM RDB$DATABASE UNION SELECT 2,'beta' FROM RDB$DATABASE) s
  ON w.id=s.id WHEN NOT MATCHED THEN INSERT (id,name) VALUES (s.id,s.name);
MERGE INTO gadgets g USING (SELECT 1 id, 'gizmo' label FROM RDB$DATABASE UNION SELECT 2,'widget' FROM RDB$DATABASE) s
  ON g.id=s.id WHEN NOT MATCHED THEN INSERT (id,label) VALUES (s.id,s.label);
COMMIT;
'@
    $runFb = {
        param($script)
        # Stage SQL to a tmp file in the container, then invoke isql -i.
        $enc = [System.Text.UTF8Encoding]::new($false)
        $bytes = $enc.GetBytes($script)
        $tmp = [System.IO.Path]::GetTempFileName()
        try {
            [System.IO.File]::WriteAllBytes($tmp, $bytes)
            podman cp $tmp sqlgo-firebird:/tmp/fb-seed.sql | Out-Null
            podman exec sqlgo-firebird /opt/firebird/bin/isql -user sqlgo -password sqlgo_dev -i /tmp/fb-seed.sql /var/lib/firebird/data/sqlgo_test.fdb 2>&1
        } finally {
            Remove-Item $tmp -ErrorAction SilentlyContinue
        }
    }
    & $runFb $ddl | Out-Null
    try {
        & $runFb $dml | Out-Null
    } catch {
        Write-Warn "firebird seed may have partially failed: $_"
    }
    Write-Log "firebird: done"
}

# --- libsql ----------------------------------------------------------------
# libsql-server runs one embedded DB; cross-catalog not applicable. Seed
# one sample table via the hrana /v2/pipeline endpoint so the explorer
# isn't empty on connect.
function Seed-Libsql {
    Write-Log "libsql: waiting for server"
    # libsql-server image has no curl/wget; probe the hrana port via bash /dev/tcp.
    $ok = Wait-For "libsql" { podman exec sqlgo-libsql bash -c ': </dev/tcp/localhost/8080' }
    if (-not $ok) { return }

    Write-Log "libsql: ensuring widgets"
    # Seed via host-side HTTP against the published port (default 18080).
    # SQLD_AUTH_JWT_KEY unset in compose.yaml → no auth header needed.
    $portLine = (podman port sqlgo-libsql 8080/tcp 2>$null | Select-Object -First 1)
    if ($portLine -match ':(\d+)$') { $port = $Matches[1] } else { $port = '18080' }
    $body = '{"requests":[{"type":"execute","stmt":{"sql":"CREATE TABLE IF NOT EXISTS widgets (id INTEGER PRIMARY KEY, name TEXT)"}},{"type":"execute","stmt":{"sql":"INSERT OR IGNORE INTO widgets VALUES (1,''alpha''),(2,''beta'')"}},{"type":"close"}]}'
    try {
        Invoke-RestMethod -Method Post -Uri "http://localhost:$port/v2/pipeline" -ContentType 'application/json' -Body $body | Out-Null
    } catch {
        Write-Warn "libsql seed may have failed: $_"
    }
    Write-Log "libsql: done"
}

# --- sybase ----------------------------------------------------------------
# ASE supports multiple user DBs; create sqlgo_a/sqlgo_b alongside the
# preseeded testdb. The datagrip/sybase image's sa password is blank.
function Seed-Sybase {
    Write-Log "sybase: waiting for server"
    # isql needs SYBASE env to find localization files; neither login shell
    # nor /etc/profile.d sets it, so source SYBASE.sh explicitly.
    $ok = Wait-For "sybase" {
        podman exec sqlgo-sybase bash -c ". /opt/sybase/SYBASE.sh && printf 'select 1\ngo\n' | /opt/sybase/OCS-16_0/bin/isql -U sa -P myPassword -S MYSYBASE"
    }
    if (-not $ok) { return }

    Write-Log "sybase: ensuring sqlgo_a/sqlgo_b + sample tables"
    $sql = @'
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
'@
    try {
        $sql | podman exec -i sqlgo-sybase bash -c ". /opt/sybase/SYBASE.sh && /opt/sybase/OCS-16_0/bin/isql -U sa -P myPassword -S MYSYBASE" 2>&1 | Out-Null
    } catch {
        Write-Warn "sybase seed may have partially failed: $_"
    }
    Write-Log "sybase: done"
}

try {
    Write-Log "starting services: $($Services -join ', ')"
    podman compose up -d @Services
    if ($LASTEXITCODE -ne 0) { throw "podman compose up failed" }

    foreach ($svc in $Services) {
        switch ($svc) {
            "mssql"    { Seed-Mssql }
            "postgres" { Seed-Postgres }
            "mysql"    { Seed-Mysql }
            "oracle"   { Seed-Oracle }
            "firebird" { Seed-Firebird }
            "libsql"   { Seed-Libsql }
            "sybase"   { Seed-Sybase }
            "sshd"     { }
            "mssql-init" { }
            default { Write-Warn "unknown service: $svc" }
        }
    }

    # --- register sqlgo connections ------------------------------------------
    # Save a connection entry in the sqlgo store for each seeded service so
    # the TUI picker already lists them.
    function Register-Conn {
        param([string]$Name, [string]$Password, [string[]]$Rest)
        if ($Password) {
            $result = $Password | & go run -tags sqlite_fts5 ./cmd/sqlgo conns add $Name --force --keyring=false --password-stdin @Rest 2>&1
        } else {
            $result = & go run -tags sqlite_fts5 ./cmd/sqlgo conns add $Name --force --keyring=false @Rest 2>&1
        }
        if ($LASTEXITCODE -ne 0) { Write-Warn "register failed: $result" }
        else { Write-Log $result }
    }

    Write-Log "registering sqlgo connections"
    foreach ($svc in $Services) {
        switch ($svc) {
            "mssql" {
                Register-Conn -Name "Dev MSSQL" -Password "SqlGo_dev_Pass1!" -Rest @(
                    "--driver", "mssql", "--host", "localhost", "--port", "11433",
                    "--user", "sa", "--option", "encrypt=disable"
                )
            }
            "postgres" {
                Register-Conn -Name "Dev Postgres" -Password "sqlgo_dev" -Rest @(
                    "--driver", "postgres", "--host", "localhost", "--port", "15432",
                    "--user", "sqlgo", "--database", "sqlgo_a", "--option", "sslmode=disable"
                )
            }
            "mysql" {
                Register-Conn -Name "Dev MySQL" -Password "sqlgo_dev" -Rest @(
                    "--driver", "mysql", "--host", "localhost", "--port", "13306",
                    "--user", "sqlgo", "--database", "sqlgo_a"
                )
            }
            "oracle" {
                Register-Conn -Name "Dev Oracle" -Password "sqlgo_dev" -Rest @(
                    "--driver", "oracle", "--host", "localhost", "--port", "11521",
                    "--user", "system", "--database", "FREEPDB1"
                )
            }
            "firebird" {
                Register-Conn -Name "Dev Firebird" -Password "sqlgo_dev" -Rest @(
                    "--driver", "firebird", "--host", "localhost", "--port", "13050",
                    "--user", "sqlgo", "--database", "/var/lib/firebird/data/sqlgo_test.fdb"
                )
            }
            "libsql" {
                Register-Conn -Name "Dev libSQL" -Password "" -Rest @(
                    "--driver", "libsql", "--host", "http://localhost:18080", "--port", "0"
                )
            }
            "sybase" {
                Register-Conn -Name "Dev Sybase" -Password "myPassword" -Rest @(
                    "--driver", "sybase", "--host", "localhost", "--port", "15000",
                    "--user", "sa"
                )
            }
        }
    }

    Write-Log "all services seeded"
} finally {
    Pop-Location
}
