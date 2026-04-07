package db

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type ConnectionSettings struct {
	Host             string `json:"host,omitempty"`
	Port             int    `json:"port,omitempty"`
	Database         string `json:"database,omitempty"`
	Schema           string `json:"schema,omitempty"`
	Username         string `json:"username,omitempty"`
	PasswordKey      string `json:"password_key,omitempty"`
	FilePath         string `json:"file_path,omitempty"`
	Account          string `json:"account,omitempty"`
	Warehouse        string `json:"warehouse,omitempty"`
	Role             string `json:"role,omitempty"`
	AdditionalParams string `json:"additional_params,omitempty"`
}

type ConnectionProfile struct {
	Name       string             `json:"name"`
	ProviderID ProviderID         `json:"provider_id"`
	AuthMode   AuthMode           `json:"auth_mode,omitempty"`
	DSN        string             `json:"dsn,omitempty"`
	Settings   ConnectionSettings `json:"settings,omitempty"`
	ReadOnly   bool               `json:"read_only"`
	Notes      string             `json:"notes,omitempty"`
	CreatedAt  time.Time          `json:"created_at"`
	UpdatedAt  time.Time          `json:"updated_at"`
}

func (p ConnectionProfile) Validate() error {
	switch {
	case p.Name == "":
		return ErrInvalidProfile("profile name is required")
	case p.ProviderID == "":
		return ErrInvalidProfile("provider is required")
	}

	if p.DSN != "" {
		return nil
	}

	switch p.ProviderID {
	case ProviderSQLite:
		if strings.TrimSpace(p.Settings.FilePath) == "" {
			return ErrInvalidProfile("sqlite file path is required")
		}
	default:
		if strings.TrimSpace(p.Settings.Host) == "" && p.ProviderID != ProviderSnowflake {
			return ErrInvalidProfile("host is required")
		}
		if p.ProviderID == ProviderSnowflake && strings.TrimSpace(p.Settings.Account) == "" {
			return ErrInvalidProfile("snowflake account is required")
		}
	}

	return nil
}

func (p ConnectionProfile) SecretKey() string {
	return "profile:" + strings.ToLower(p.Name) + ":password"
}

func (p ConnectionProfile) ResolveDSN(secrets SecretStore) (string, error) {
	if strings.TrimSpace(p.DSN) != "" {
		return p.DSN, nil
	}

	password, err := p.lookupPassword(secrets)
	if err != nil {
		return "", err
	}

	switch p.ProviderID {
	case ProviderSQLite:
		return "file:" + filepath.ToSlash(p.Settings.FilePath), nil
	case ProviderSQLServer, ProviderAzureSQL:
		return buildSQLServerDSN(p, password)
	case ProviderPostgres:
		return buildPostgresDSN(p, password)
	case ProviderMySQL:
		return buildMySQLDSN(p, password)
	case ProviderSnowflake:
		return buildSnowflakeDSN(p, password)
	case ProviderSybase:
		return buildSybaseDSN(p, password)
	default:
		return "", fmt.Errorf("dsn builder not implemented for provider %s", p.ProviderID)
	}
}

func (p ConnectionProfile) lookupPassword(secrets SecretStore) (string, error) {
	if secrets == nil || p.Settings.PasswordKey == "" {
		return "", nil
	}
	value, err := secrets.Get(p.Settings.PasswordKey)
	if err != nil {
		return "", err
	}
	return value, nil
}

func buildSQLServerDSN(profile ConnectionProfile, password string) (string, error) {
	query := url.Values{}
	if profile.Settings.Database != "" {
		query.Set("database", profile.Settings.Database)
	}
	applyAdditionalParams(query, profile.Settings.AdditionalParams)

	host := withPort(profile.Settings.Host, profile.Settings.Port, 1433)

	switch profile.AuthMode {
	case AuthWindowsSSO:
		return (&url.URL{
			Scheme:   "sqlserver",
			Host:     host,
			RawQuery: query.Encode(),
		}).String(), nil
	case AuthAzureAD:
		if profile.Settings.Username != "" {
			query.Set("fedauth", "ActiveDirectoryPassword")
			return (&url.URL{
				Scheme:   "sqlserver",
				User:     url.UserPassword(profile.Settings.Username, password),
				Host:     host,
				RawQuery: query.Encode(),
			}).String(), nil
		}
		query.Set("fedauth", "ActiveDirectoryDefault")
		return (&url.URL{
			Scheme:   "sqlserver",
			Host:     host,
			RawQuery: query.Encode(),
		}).String(), nil
	default:
		return (&url.URL{
			Scheme:   "sqlserver",
			User:     url.UserPassword(profile.Settings.Username, password),
			Host:     host,
			RawQuery: query.Encode(),
		}).String(), nil
	}
}

func buildPostgresDSN(profile ConnectionProfile, password string) (string, error) {
	query := url.Values{}
	applyAdditionalParams(query, profile.Settings.AdditionalParams)
	if query.Get("sslmode") == "" {
		query.Set("sslmode", "disable")
	}
	return (&url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(profile.Settings.Username, password),
		Host:     withPort(profile.Settings.Host, profile.Settings.Port, 5432),
		Path:     "/" + profile.Settings.Database,
		RawQuery: query.Encode(),
	}).String(), nil
}

func buildMySQLDSN(profile ConnectionProfile, password string) (string, error) {
	query := url.Values{}
	applyAdditionalParams(query, profile.Settings.AdditionalParams)
	if query.Get("parseTime") == "" {
		query.Set("parseTime", "true")
	}
	return fmt.Sprintf(
		"%s:%s@tcp(%s)/%s?%s",
		profile.Settings.Username,
		password,
		withPort(profile.Settings.Host, profile.Settings.Port, 3306),
		profile.Settings.Database,
		query.Encode(),
	), nil
}

func buildSnowflakeDSN(profile ConnectionProfile, password string) (string, error) {
	query := url.Values{}
	if profile.Settings.Warehouse != "" {
		query.Set("warehouse", profile.Settings.Warehouse)
	}
	if profile.Settings.Role != "" {
		query.Set("role", profile.Settings.Role)
	}
	applyAdditionalParams(query, profile.Settings.AdditionalParams)

	path := "/" + profile.Settings.Database
	if profile.Settings.Schema != "" {
		path += "/" + profile.Settings.Schema
	}

	return (&url.URL{
		Scheme:   "snowflake",
		User:     url.UserPassword(profile.Settings.Username, password),
		Host:     profile.Settings.Account,
		Path:     path,
		RawQuery: query.Encode(),
	}).String(), nil
}

func buildSybaseDSN(profile ConnectionProfile, password string) (string, error) {
	query := url.Values{}
	applyAdditionalParams(query, profile.Settings.AdditionalParams)
	return (&url.URL{
		Scheme:   "tds",
		User:     url.UserPassword(profile.Settings.Username, password),
		Host:     withPort(profile.Settings.Host, profile.Settings.Port, 5000),
		Path:     "/" + profile.Settings.Database,
		RawQuery: query.Encode(),
	}).String(), nil
}

func withPort(host string, port, defaultPort int) string {
	actualPort := port
	if actualPort == 0 {
		actualPort = defaultPort
	}
	return host + ":" + strconv.Itoa(actualPort)
}

func applyAdditionalParams(values url.Values, raw string) {
	if strings.TrimSpace(raw) == "" {
		return
	}
	extra, err := url.ParseQuery(raw)
	if err != nil {
		return
	}
	for key, items := range extra {
		for _, item := range items {
			values.Add(key, item)
		}
	}
}
