package mssql

import (
	"context"
	stdsql "database/sql"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Nulifyer/sqlgo/internal/db"
)

func TestColumnsResolvesBareAndQualifiedNamesOnServer(t *testing.T) {
	for _, schema := range []string{"", "dbo", "sales]region"} {
		t.Run(schema, func(t *testing.T) {
			sqlDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
			if err != nil {
				t.Fatal(err)
			}
			mock.ExpectPing()
			// Assert the server owns name resolution as well as the bound arguments.
			if !strings.Contains(columnsQuery, "OBJECT_ID(CASE WHEN @p1 = '' THEN QUOTENAME(@p2)") {
				t.Fatal("column lookup must resolve bare identifiers on SQL Server")
			}
			mock.ExpectQuery(regexp.QuoteMeta(columnsQuery)).WithArgs(schema, "my.table]").
				WillReturnRows(sqlmock.NewRows([]string{"name", "type"}).AddRow("_id", "int"))
			mock.ExpectClose()
			conn, err := db.OpenWith(context.Background(), Profile, db.Transport{
				Name: "mssql", Open: func(context.Context, db.Config) (*stdsql.DB, func() error, error) { return sqlDB, nil, nil },
			}, db.Config{})
			if err != nil {
				t.Fatal(err)
			}
			cols, err := conn.Columns(context.Background(), db.TableRef{Schema: schema, Name: "my.table]"})
			if err != nil {
				t.Fatal(err)
			}
			if len(cols) != 1 || cols[0].Name != "_id" || cols[0].TypeName != "int" {
				t.Fatalf("columns = %+v", cols)
			}
			if err := conn.Close(); err != nil {
				t.Fatal(err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestExplainInRestoresOriginalDatabase(t *testing.T) {
	sqlDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}

	mock.ExpectPing()
	mock.ExpectQuery(regexp.QuoteMeta(currentDatabaseQuery)).
		WillReturnRows(sqlmock.NewRows([]string{"database"}).AddRow("source]db"))
	mock.ExpectExec(regexp.QuoteMeta("USE [analytics]]db]")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("SET SHOWPLAN_XML ON")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT 1")).
		WillReturnRows(sqlmock.NewRows([]string{"plan"}).AddRow("<ShowPlanXML/>"))
	mock.ExpectExec(regexp.QuoteMeta("SET SHOWPLAN_XML OFF")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("USE [source]]db]")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectClose()

	conn, err := db.OpenWith(context.Background(), Profile, db.Transport{
		Name: "mssql",
		Open: func(context.Context, db.Config) (*stdsql.DB, func() error, error) {
			return sqlDB, nil, nil
		},
	}, db.Config{})
	if err != nil {
		t.Fatalf("OpenWith: %v", err)
	}
	explainer, ok := conn.(db.DatabaseExplainer)
	if !ok {
		t.Fatal("connection does not implement DatabaseExplainer")
	}
	rows, err := explainer.ExplainIn(context.Background(), "analytics]db", "SELECT 1")
	if err != nil {
		t.Fatalf("ExplainIn: %v", err)
	}
	if len(rows) != 1 || len(rows[0]) != 1 || rows[0][0] != "<ShowPlanXML/>" {
		t.Fatalf("ExplainIn rows = %#v", rows)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestProfileSupportsNativeAndAzureSQLTransports(t *testing.T) {
	t.Parallel()
	for _, transport := range []string{"mssql", "azuresql"} {
		if !Profile.SupportsTransport(transport) {
			t.Errorf("Profile.SupportsTransport(%q) = false, want true", transport)
		}
	}
	if Profile.SupportsTransport("tds") {
		t.Error(`Profile.SupportsTransport("tds") = true, want false`)
	}
}

func TestBuildDSN(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		cfg        db.Config
		wantHost   string
		wantUser   string
		wantPass   string
		wantQuery  map[string]string
		wantScheme string
	}{
		{
			name: "defaults fill in localhost and 1433",
			cfg: db.Config{
				User:     "sa",
				Password: "p",
			},
			wantHost:   "localhost:1433",
			wantUser:   "sa",
			wantPass:   "p",
			wantScheme: "sqlserver",
			wantQuery:  map[string]string{},
		},
		{
			name: "explicit host, port, database",
			cfg: db.Config{
				Host:     "db.example.com",
				Port:     11433,
				User:     "sa",
				Password: "pw",
				Database: "app",
			},
			wantHost:   "db.example.com:11433",
			wantUser:   "sa",
			wantPass:   "pw",
			wantScheme: "sqlserver",
			wantQuery:  map[string]string{"database": "app"},
		},
		{
			name: "options passed through as query params",
			cfg: db.Config{
				Host:     "h",
				Port:     1,
				User:     "u",
				Password: "pp",
				Options: map[string]string{
					"encrypt":                "disable",
					"TrustServerCertificate": "true",
				},
			},
			wantHost:   "h:1",
			wantUser:   "u",
			wantPass:   "pp",
			wantScheme: "sqlserver",
			wantQuery: map[string]string{
				"encrypt":                "disable",
				"TrustServerCertificate": "true",
			},
		},
		{
			name: "password with special characters is url-encoded",
			cfg: db.Config{
				Host:     "h",
				Port:     1,
				User:     "sa",
				Password: "p@ss w:rd!",
			},
			wantHost:   "h:1",
			wantUser:   "sa",
			wantPass:   "p@ss w:rd!",
			wantScheme: "sqlserver",
			wantQuery:  map[string]string{},
		},
		{
			// winsspi w/o User -> go-mssqldb uses current Windows identity.
			// The authenticator query param is what selects the provider;
			// User/Password may be blank.
			name: "winsspi current identity",
			cfg: db.Config{
				Host: "h",
				Port: 1,
				Options: map[string]string{
					"authenticator": "winsspi",
				},
			},
			wantHost:   "h:1",
			wantUser:   "",
			wantPass:   "",
			wantScheme: "sqlserver",
			wantQuery: map[string]string{
				"authenticator": "winsspi",
			},
		},
		{
			// winsspi with DOMAIN\user + password -- explicit Windows creds.
			name: "winsspi domain user and password",
			cfg: db.Config{
				Host:     "h",
				Port:     1,
				User:     `CORP\alice`,
				Password: "pw",
				Options: map[string]string{
					"authenticator": "winsspi",
				},
			},
			wantHost:   "h:1",
			wantUser:   `CORP\alice`,
			wantPass:   "pw",
			wantScheme: "sqlserver",
			wantQuery: map[string]string{
				"authenticator": "winsspi",
			},
		},
		{
			// NTLM is cross-platform. Needs User + Password.
			name: "ntlm user and password",
			cfg: db.Config{
				Host:     "h",
				Port:     1,
				User:     "alice",
				Password: "pw",
				Options: map[string]string{
					"authenticator": "ntlm",
				},
			},
			wantHost:   "h:1",
			wantUser:   "alice",
			wantPass:   "pw",
			wantScheme: "sqlserver",
			wantQuery: map[string]string{
				"authenticator": "ntlm",
			},
		},
		{
			// Kerberos password mode: User + Password + realm + configfile.
			name: "krb5 password mode",
			cfg: db.Config{
				Host:     "h",
				Port:     1,
				User:     "alice",
				Password: "pw",
				Options: map[string]string{
					"authenticator":   "krb5",
					"krb5-configfile": "/etc/krb5.conf",
					"krb5-realm":      "EXAMPLE.COM",
				},
			},
			wantHost:   "h:1",
			wantUser:   "alice",
			wantPass:   "pw",
			wantScheme: "sqlserver",
			wantQuery: map[string]string{
				"authenticator":   "krb5",
				"krb5-configfile": "/etc/krb5.conf",
				"krb5-realm":      "EXAMPLE.COM",
			},
		},
		{
			// Kerberos keytab mode: keytab replaces password.
			name: "krb5 keytab mode",
			cfg: db.Config{
				Host: "h",
				Port: 1,
				User: "alice",
				Options: map[string]string{
					"authenticator":   "krb5",
					"krb5-configfile": "/etc/krb5.conf",
					"krb5-keytabfile": "/etc/alice.keytab",
					"krb5-realm":      "EXAMPLE.COM",
				},
			},
			wantHost:   "h:1",
			wantUser:   "alice",
			wantPass:   "",
			wantScheme: "sqlserver",
			wantQuery: map[string]string{
				"authenticator":   "krb5",
				"krb5-configfile": "/etc/krb5.conf",
				"krb5-keytabfile": "/etc/alice.keytab",
				"krb5-realm":      "EXAMPLE.COM",
			},
		},
		{
			// Kerberos credcache mode: no User/Password; uses existing TGT.
			name: "krb5 credcache mode",
			cfg: db.Config{
				Host: "h",
				Port: 1,
				Options: map[string]string{
					"authenticator":      "krb5",
					"krb5-configfile":    "/etc/krb5.conf",
					"krb5-credcachefile": "/tmp/krb5cc_1000",
				},
			},
			wantHost:   "h:1",
			wantUser:   "",
			wantPass:   "",
			wantScheme: "sqlserver",
			wantQuery: map[string]string{
				"authenticator":      "krb5",
				"krb5-configfile":    "/etc/krb5.conf",
				"krb5-credcachefile": "/tmp/krb5cc_1000",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := buildDSN(tc.cfg)
			u, err := url.Parse(got)
			if err != nil {
				t.Fatalf("parse DSN %q: %v", got, err)
			}
			if u.Scheme != tc.wantScheme {
				t.Errorf("scheme = %q, want %q", u.Scheme, tc.wantScheme)
			}
			if u.Host != tc.wantHost {
				t.Errorf("host = %q, want %q", u.Host, tc.wantHost)
			}
			if u.User.Username() != tc.wantUser {
				t.Errorf("user = %q, want %q", u.User.Username(), tc.wantUser)
			}
			pw, _ := u.User.Password()
			if pw != tc.wantPass {
				t.Errorf("password = %q, want %q", pw, tc.wantPass)
			}
			q := u.Query()
			if len(q) != len(tc.wantQuery) {
				t.Errorf("query params = %v, want %v", q, tc.wantQuery)
			}
			for k, v := range tc.wantQuery {
				if got := q.Get(k); got != v {
					t.Errorf("query[%q] = %q, want %q", k, got, v)
				}
			}
		})
	}
}

func TestBuildDSNIPv6(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		host string
		want string
	}{
		{name: "literal", host: "2001:db8::1", want: "2001:db8::1"},
		{name: "bracketed", host: "[2001:db8::1]", want: "2001:db8::1"},
		{name: "zone", host: "fe80::1%eth0", want: "fe80::1%eth0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u, err := url.Parse(buildDSN(db.Config{Host: tc.host, Port: 11433, User: "sa"}))
			if err != nil {
				t.Fatalf("parse built DSN: %v", err)
			}
			if u.Hostname() != tc.want || u.Port() != "11433" {
				t.Fatalf("built DSN host/port = %q/%q, want %q/11433", u.Hostname(), u.Port(), tc.want)
			}
		})
	}
}
