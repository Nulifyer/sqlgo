package db

type ProviderID string

const (
	ProviderSQLServer ProviderID = "sqlserver"
	ProviderAzureSQL  ProviderID = "azuresql"
	ProviderPostgres  ProviderID = "postgres"
	ProviderMySQL     ProviderID = "mysql"
	ProviderSQLite    ProviderID = "sqlite"
	ProviderSnowflake ProviderID = "snowflake"
	ProviderSybase    ProviderID = "sybase"
)

type AuthMode string

const (
	AuthSQLPassword  AuthMode = "sql_password"
	AuthWindowsSSO   AuthMode = "windows_sso"
	AuthAzureAD      AuthMode = "azure_ad"
	AuthUsernamePass AuthMode = "username_password"
	AuthSQLiteFile   AuthMode = "sqlite_file"
	AuthSnowflakeSSO AuthMode = "snowflake_sso"
	AuthExperimental AuthMode = "experimental"
)

type Capabilities struct {
	PureGo                 bool
	SupportsTransactions   bool
	SupportsSchemas        bool
	SupportsCatalogs       bool
	SupportsIntegratedAuth bool
	SupportsBrowserAuth    bool
	Experimental           bool
}

type Provider struct {
	ID           ProviderID
	DisplayName  string
	DriverName   string
	AuthModes    []AuthMode
	Capabilities Capabilities
	Notes        string
}
