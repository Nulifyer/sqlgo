// Package azuresql registers the "azuresql" driver: SQL Server on the
// wire (same Profile as mssql) paired with go-mssqldb's azuread subdriver
// for Azure AD / Entra ID authentication. Import for side effects.
//
// The fedauth selector routes cfg.User/Password + a handful of semantic
// option fields onto the specific DSN keys go-mssqldb expects for each
// mode. Callers set cfg.Options["fedauth"] to the mode; BuildDSN handles
// the rest. If fedauth is unset the driver falls back to SQL auth over
// the native sqlserver driver so this preset can also be used for plain
// Azure SQL with a contained database user.
package azuresql

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	// Registers the "azuresql" driver name with database/sql. The package
	// itself wraps "sqlserver" and interprets fedauth/applicationclientid
	// query params as Azure AD auth requests.
	_ "github.com/microsoft/go-mssqldb/azuread"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/db/mssql"
)

const (
	driverName        = "azuresql"
	azureSQLDriverKey = "azuresql"
)

// Fedauth mode constants mirror what go-mssqldb/azuread accepts. Kept as
// strings (not an enum type) so they round-trip through the Options map
// unchanged.
const (
	FedauthPassword         = "ActiveDirectoryPassword"
	FedauthServicePrincipal = "ActiveDirectoryServicePrincipal"
	FedauthManagedIdentity  = "ActiveDirectoryManagedIdentity"
	FedauthInteractive      = "ActiveDirectoryInteractive"
	FedauthDefault          = "ActiveDirectoryDefault"
)

// FedauthModes lists the accepted mode strings in a stable order. The
// leading empty string means "no fedauth -- fall back to SQL auth over
// the plain sqlserver driver". Exported so the TUI engine_spec can
// surface the same list as a cycler without hardcoding it twice.
var FedauthModes = []string{
	"",
	FedauthPassword,
	FedauthServicePrincipal,
	FedauthManagedIdentity,
	FedauthInteractive,
	FedauthDefault,
}

func init() {
	db.RegisterTransport(AzureSQLTransport)
	db.Register(preset{})
}

// AzureSQLTransport wraps the azuread-registered driver. Default port
// 1433, TLS always implied (Azure SQL mandates encryption) -- BuildDSN
// sets encrypt=true by default.
var AzureSQLTransport = db.Transport{
	Name:          "azuresql",
	SQLDriverName: azureSQLDriverKey,
	DefaultPort:   1433,
	SupportsTLS:   true,
	BuildDSN:      buildDSN,
}

type preset struct{}

func (preset) Name() string                  { return driverName }
func (preset) Capabilities() db.Capabilities { return mssql.Profile.Capabilities }
func (preset) Open(ctx context.Context, cfg db.Config) (db.Conn, error) {
	return db.OpenWith(ctx, mssql.Profile, AzureSQLTransport, cfg)
}

// buildDSN maps sqlgo's semantic fields onto go-mssqldb/azuread query
// params per fedauth mode. Produces a sqlserver:// URL.
//
// Field mapping (sqlgo -> go-mssqldb DSN key):
//   - cfg.Host/Port       -> URL host
//   - cfg.Database        -> ?database=
//   - Options["encrypt"]  -> ?encrypt= (default "true")
//   - Options["fedauth"]  -> ?fedauth=
//
// Per-mode credential mapping:
//
//	Password:
//	  cfg.User     -> ?user id=             (principal email e.g. alice@contoso.com)
//	  cfg.Password -> ?password=
//
//	ServicePrincipal (secret):
//	  cfg.User, Options["tenant_id"] -> ?user id=<client_id>@<tenant_id>
//	  cfg.Password                   -> ?password=   (client secret)
//
//	ServicePrincipal (cert):
//	  cfg.User, Options["tenant_id"]  -> ?user id=<client_id>@<tenant_id>
//	  Options["cert_path"]            -> ?clientcertpath=
//	  Options["cert_password"]        -> ?password=   (cert file password, optional)
//
//	ManagedIdentity:
//	  cfg.User                        -> ?user id=    (client id, optional -- only for user-assigned MI)
//
//	Interactive:
//	  cfg.User                        -> ?user id=    (optional login hint)
//
//	Default:
//	  (no credentials -- Azure SDK DefaultAzureCredential chain picks them up)
//
// Missing required fields for a given mode are not rejected here -- the
// underlying driver surfaces a clear error at connect time, which is
// what the form's Test-Auth hotkey is for.
func buildDSN(cfg db.Config) string {
	host := cfg.Host
	if host == "" {
		host = "localhost"
	}
	port := cfg.Port
	if port == 0 {
		port = 1433
	}

	u := url.URL{
		Scheme: "sqlserver",
		Host:   host + ":" + strconv.Itoa(port),
	}
	q := u.Query()
	if cfg.Database != "" {
		q.Set("database", cfg.Database)
	}

	mode := strings.TrimSpace(cfg.Options["fedauth"])
	opt := func(key string) string { return strings.TrimSpace(cfg.Options[key]) }

	switch mode {
	case FedauthPassword:
		q.Set("fedauth", mode)
		if cfg.User != "" {
			q.Set("user id", cfg.User)
		}
		if cfg.Password != "" {
			q.Set("password", cfg.Password)
		}

	case FedauthServicePrincipal:
		q.Set("fedauth", mode)
		principal := cfg.User
		if tenant := opt("tenant_id"); tenant != "" && principal != "" {
			principal = principal + "@" + tenant
		}
		if principal != "" {
			q.Set("user id", principal)
		}
		if cert := opt("cert_path"); cert != "" {
			// Cert-based SP auth. Password, if present, unlocks the cert file.
			q.Set("clientcertpath", cert)
			if cp := opt("cert_password"); cp != "" {
				q.Set("password", cp)
			} else if cfg.Password != "" {
				q.Set("password", cfg.Password)
			}
		} else if cfg.Password != "" {
			// Secret-based SP auth. cfg.Password is the client secret.
			q.Set("password", cfg.Password)
		}

	case FedauthManagedIdentity:
		q.Set("fedauth", mode)
		if cfg.User != "" {
			// User-assigned MI: cfg.User is the managed-identity client id.
			q.Set("user id", cfg.User)
		}

	case FedauthInteractive:
		q.Set("fedauth", mode)
		if cfg.User != "" {
			q.Set("user id", cfg.User)
		}

	case FedauthDefault:
		q.Set("fedauth", mode)
		// No creds: DefaultAzureCredential picks them up from env/cli/MI.

	default:
		// Empty mode: behave like plain sqlserver auth. Preserves the ability
		// to use this preset for Azure SQL with a contained database user
		// without picking an AAD mode.
		if cfg.User != "" {
			q.Set("user id", cfg.User)
		}
		if cfg.Password != "" {
			q.Set("password", cfg.Password)
		}
	}

	// Azure SQL requires TLS on the wire. Default encrypt=true; explicit
	// Options["encrypt"] still wins if the user picked something else
	// (e.g. "strict" for mutual TLS scenarios).
	if enc := opt("encrypt"); enc != "" {
		q.Set("encrypt", enc)
	} else if !q.Has("encrypt") {
		q.Set("encrypt", "true")
	}

	// Pass-through for any remaining driver knobs. Already-set keys are
	// not overwritten so the mapping above wins over raw Options.
	for k, v := range cfg.Options {
		if v == "" {
			continue
		}
		switch k {
		case "fedauth", "tenant_id", "cert_path", "cert_password", "encrypt":
			continue
		}
		if q.Has(k) {
			continue
		}
		q.Set(k, v)
	}

	u.RawQuery = q.Encode()
	return u.String()
}
