package db

import _ "github.com/go-sql-driver/mysql"
import _ "github.com/jackc/pgx/v5/stdlib"
import _ "github.com/microsoft/go-mssqldb"
import _ "github.com/microsoft/go-mssqldb/azuread"
import _ "github.com/snowflakedb/gosnowflake/v2"
import _ "github.com/thda/tds"
import _ "modernc.org/sqlite"

type Registry struct {
	providers []Provider
}

func DefaultRegistry() *Registry {
	return &Registry{
		providers: []Provider{
			{
				ID:          ProviderSQLServer,
				DisplayName: "SQL Server",
				DriverName:  "sqlserver",
				AuthModes:   []AuthMode{AuthSQLPassword, AuthWindowsSSO},
				Capabilities: Capabilities{
					PureGo:                 true,
					SupportsTransactions:   true,
					SupportsSchemas:        true,
					SupportsCatalogs:       true,
					SupportsIntegratedAuth: true,
				},
				Notes: "Primary workhorse provider and the deepest v1 UX target.",
			},
			{
				ID:          ProviderAzureSQL,
				DisplayName: "Azure SQL",
				DriverName:  "azuresql",
				AuthModes:   []AuthMode{AuthAzureAD, AuthSQLPassword},
				Capabilities: Capabilities{
					PureGo:               true,
					SupportsTransactions: true,
					SupportsSchemas:      true,
					SupportsCatalogs:     true,
					SupportsBrowserAuth:  true,
				},
				Notes: "Uses the Microsoft driver stack with Azure AD auth support.",
			},
			{
				ID:          ProviderPostgres,
				DisplayName: "PostgreSQL",
				DriverName:  "pgx",
				AuthModes:   []AuthMode{AuthUsernamePass},
				Capabilities: Capabilities{
					PureGo:               true,
					SupportsTransactions: true,
					SupportsSchemas:      true,
					SupportsCatalogs:     true,
				},
				Notes: "Backed by pgx stdlib integration.",
			},
			{
				ID:          ProviderMySQL,
				DisplayName: "MySQL",
				DriverName:  "mysql",
				AuthModes:   []AuthMode{AuthUsernamePass},
				Capabilities: Capabilities{
					PureGo:               true,
					SupportsTransactions: true,
					SupportsSchemas:      true,
				},
				Notes: "Backed by go-sql-driver/mysql.",
			},
			{
				ID:          ProviderSQLite,
				DisplayName: "SQLite",
				DriverName:  "sqlite",
				AuthModes:   []AuthMode{AuthSQLiteFile},
				Capabilities: Capabilities{
					PureGo:               true,
					SupportsTransactions: true,
				},
				Notes: "Uses modernc.org/sqlite for CGO-free builds.",
			},
			{
				ID:          ProviderSnowflake,
				DisplayName: "Snowflake",
				DriverName:  "snowflake",
				AuthModes:   []AuthMode{AuthUsernamePass, AuthSnowflakeSSO},
				Capabilities: Capabilities{
					PureGo:               true,
					SupportsTransactions: true,
					SupportsSchemas:      true,
					SupportsCatalogs:     true,
					SupportsBrowserAuth:  true,
				},
				Notes: "Uses gosnowflake v2 and should support SSO-capable flows where the driver allows it.",
			},
			{
				ID:          ProviderSybase,
				DisplayName: "Sybase ASE",
				DriverName:  "tds",
				AuthModes:   []AuthMode{AuthUsernamePass, AuthExperimental},
				Capabilities: Capabilities{
					PureGo:               true,
					SupportsTransactions: true,
					Experimental:         true,
				},
				Notes: "Pure-Go support is currently experimental and should stay isolated until validated against real ASE instances.",
			},
		},
	}
}

func (r *Registry) Providers() []Provider {
	out := make([]Provider, len(r.providers))
	copy(out, r.providers)
	return out
}

func (r *Registry) Provider(id ProviderID) (Provider, bool) {
	for _, provider := range r.providers {
		if provider.ID == id {
			return provider, true
		}
	}
	return Provider{}, false
}
