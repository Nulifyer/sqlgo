// Package athena registers the uber/athenadriver/go driver (pure Go,
// AWS SDK over HTTPS). Import for side effects.
//
// AWS Athena is cloud-only: there is no self-hostable image, so this
// adapter has no compose service or seed entry. The integration test
// is env-gated and SKIPs when SQLGO_IT_ATHENA_* vars aren't populated
// (see athena_integration_test.go).
//
// Config mapping:
//
//	cfg.User                        -> accessID (or "" to use IAM / env chain)
//	cfg.Password                    -> secretAccessKey (or "" when using IAM)
//	cfg.Database                    -> ?db= (Athena database / Glue schema name)
//	cfg.Options["region"]           -> ?region= (required unless aws_profile picks it up)
//	cfg.Options["output_location"]  -> DSN base path (s3://bucket/prefix/); required
//	cfg.Options["session_token"]    -> ?sessionToken= (STS / assumed role)
//	cfg.Options["workgroup"]        -> ?workgroupName=
//	cfg.Options["aws_profile"]      -> ?AWSProfile= (named profile, ~/.aws/credentials)
//	cfg.Options["catalog"]          -> ?catalog= (Glue data catalog name, default AwsDataCatalog)
//	cfg.Options["poll_interval"]    -> ?resultPollIntervalSeconds=
//	cfg.Options["read_only"]        -> ?ReadOnly= (true blocks non-SELECT/SHOW/DESC)
//	cfg.Options["moneywise"]        -> ?MoneyWise=
//	cfg.Options["missing_as_nil"]   -> ?missingAsNil=
//	cfg.Options["tag"]              -> ?tag= (pipe-separated workgroup tags)
//
// STS assume-role surface (pre-flight before DSN build):
//
//	cfg.Options["assume_role_arn"]              -> target role ARN; enables the STS flow
//	cfg.Options["assume_role_session_name"]     -> RoleSessionName (default "sqlgo")
//	cfg.Options["assume_role_external_id"]      -> ExternalId (cross-account confused-deputy guard)
//	cfg.Options["assume_role_duration_seconds"] -> DurationSeconds (STS default is 3600)
//	cfg.Options["web_identity_token_file"]      -> path to OIDC token; switches the call to
//	                                               AssumeRoleWithWebIdentity (EKS IRSA, GitHub OIDC)
//
// Athena is managed Presto/Trino, so it reuses DialectTrino and the
// information_schema layout. Transactions are unsupported at the
// driver level (Begin returns ErrAthenaTransactionUnsupported), so
// SupportsTransactions stays false. Cancel is honored during polling.
package athena

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	_ "github.com/uber/athenadriver/go"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/sqltok"
)

const driverName = "athena"

func init() {
	db.RegisterProfile(Profile)
	db.RegisterTransport(NativeTransport)
	db.Register(preset{})
}

// Profile is the Athena dialect brain. Athena is managed Presto /
// Trino (v0.217-ish with AWS extensions), so identifier quoting,
// LIMIT/OFFSET, SHOW CREATE TABLE/VIEW and information_schema layout
// all match Trino. EXPLAIN returns a text plan.
var Profile = db.Profile{
	Name: driverName,
	Capabilities: db.Capabilities{
		SchemaDepth:          db.SchemaDepthSchemas,
		LimitSyntax:          db.LimitSyntaxLimit,
		IdentifierQuote:      '"',
		SupportsCancel:       true,
		SupportsTLS:          true,
		ExplainFormat:        db.ExplainFormatNone,
		Dialect:              sqltok.DialectTrino,
		SupportsTransactions: false,
	},
	SchemaQuery:       schemaQuery,
	ColumnsQuery:      columnsQuery,
	DefinitionFetcher: fetchDefinition,
}

// NativeTransport wraps athenadriver. DefaultPort=0 because the DSN
// (s3:// URL + query params) carries no port -- AWS endpoints are
// region-scoped HTTPS on 443 implicitly.
//
// Open is wired so the STS AssumeRole / AssumeRoleWithWebIdentity
// pre-flight can mutate cfg before buildDSN runs, injecting temporary
// access keys + session token into the DSN query string. When no
// assume-role options are set the flow passes straight through to
// the default athenadriver credential chain (static keys -> profile
// -> AWS SDK default chain).
var NativeTransport = db.Transport{
	Name:          "athena",
	SQLDriverName: "awsathena",
	DefaultPort:   0,
	SupportsTLS:   true,
	BuildDSN:      buildDSN,
	Open:          openAthena,
}

// stsAPI is the subset of *sts.Client we call. A package-level var
// produces the client so tests can stub the whole thing without
// needing real AWS config.
type stsAPI interface {
	AssumeRole(ctx context.Context, in *sts.AssumeRoleInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleOutput, error)
	AssumeRoleWithWebIdentity(ctx context.Context, in *sts.AssumeRoleWithWebIdentityInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleWithWebIdentityOutput, error)
}

// newSTSClient is overridable by tests. Default loads the AWS SDK
// credential chain (env, shared config, IRSA, EC2/ECS metadata) scoped
// to the requested region and returns a concrete *sts.Client.
var newSTSClient = func(ctx context.Context, region string) (stsAPI, error) {
	var opts []func(*awsconfig.LoadOptions) error
	if region != "" {
		opts = append(opts, awsconfig.WithRegion(region))
	}
	awscfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return sts.NewFromConfig(awscfg), nil
}

// openAthena is the Transport.Open entry point. It runs the STS
// pre-flight (when assume_role_arn is set), rewrites cfg with the
// returned temporary credentials, and then opens through the normal
// buildDSN + sql.Open("awsathena", ...) path.
func openAthena(ctx context.Context, cfg db.Config) (*sql.DB, func() error, error) {
	mutated, err := applyAssumeRole(ctx, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("athena sts assume-role: %w", err)
	}
	sqlDB, err := sql.Open("awsathena", buildDSN(mutated))
	return sqlDB, nil, err
}

// applyAssumeRole returns a cfg copy with temporary credentials from
// STS injected into User/Password/Options["session_token"] when
// assume_role_arn is set. When no assume-role option is present the
// input is returned unchanged.
func applyAssumeRole(ctx context.Context, cfg db.Config) (db.Config, error) {
	roleArn := strings.TrimSpace(cfg.Options["assume_role_arn"])
	if roleArn == "" {
		return cfg, nil
	}
	region := strings.TrimSpace(cfg.Options["region"])
	cli, err := newSTSClient(ctx, region)
	if err != nil {
		return cfg, fmt.Errorf("load aws config: %w", err)
	}
	sessionName := strings.TrimSpace(cfg.Options["assume_role_session_name"])
	if sessionName == "" {
		sessionName = "sqlgo"
	}
	var duration *int32
	if d := strings.TrimSpace(cfg.Options["assume_role_duration_seconds"]); d != "" {
		n, err := strconv.Atoi(d)
		if err != nil || n <= 0 {
			return cfg, fmt.Errorf("assume_role_duration_seconds %q: must be a positive integer", d)
		}
		v := int32(n)
		duration = &v
	}

	var ak, sk, st string
	if tokenFile := strings.TrimSpace(cfg.Options["web_identity_token_file"]); tokenFile != "" {
		data, err := os.ReadFile(tokenFile)
		if err != nil {
			return cfg, fmt.Errorf("read web identity token %q: %w", tokenFile, err)
		}
		token := strings.TrimSpace(string(data))
		in := &sts.AssumeRoleWithWebIdentityInput{
			RoleArn:          aws.String(roleArn),
			RoleSessionName:  aws.String(sessionName),
			WebIdentityToken: aws.String(token),
		}
		if duration != nil {
			in.DurationSeconds = duration
		}
		out, err := cli.AssumeRoleWithWebIdentity(ctx, in)
		if err != nil {
			return cfg, fmt.Errorf("AssumeRoleWithWebIdentity: %w", err)
		}
		if out == nil || out.Credentials == nil {
			return cfg, fmt.Errorf("AssumeRoleWithWebIdentity returned no credentials")
		}
		ak = aws.ToString(out.Credentials.AccessKeyId)
		sk = aws.ToString(out.Credentials.SecretAccessKey)
		st = aws.ToString(out.Credentials.SessionToken)
	} else {
		in := &sts.AssumeRoleInput{
			RoleArn:         aws.String(roleArn),
			RoleSessionName: aws.String(sessionName),
		}
		if ext := strings.TrimSpace(cfg.Options["assume_role_external_id"]); ext != "" {
			in.ExternalId = aws.String(ext)
		}
		if duration != nil {
			in.DurationSeconds = duration
		}
		out, err := cli.AssumeRole(ctx, in)
		if err != nil {
			return cfg, fmt.Errorf("AssumeRole: %w", err)
		}
		if out == nil || out.Credentials == nil {
			return cfg, fmt.Errorf("AssumeRole returned no credentials")
		}
		ak = aws.ToString(out.Credentials.AccessKeyId)
		sk = aws.ToString(out.Credentials.SecretAccessKey)
		st = aws.ToString(out.Credentials.SessionToken)
	}

	// Clone Options so we don't mutate the caller's map (sqlgo Config
	// structs are passed by value but Options is a reference type).
	newOpts := make(map[string]string, len(cfg.Options)+1)
	for k, v := range cfg.Options {
		newOpts[k] = v
	}
	newOpts["session_token"] = st
	// Drop the STS-only keys so they don't leak into the DSN
	// passthrough -- athenadriver would ignore them but the surface
	// stays cleaner.
	for _, k := range []string{
		"assume_role_arn",
		"assume_role_session_name",
		"assume_role_external_id",
		"assume_role_duration_seconds",
		"web_identity_token_file",
	} {
		delete(newOpts, k)
	}

	out := cfg
	out.User = ak
	out.Password = sk
	out.Options = newOpts
	return out, nil
}

type preset struct{}

func (preset) Name() string                  { return driverName }
func (preset) Capabilities() db.Capabilities { return Profile.Capabilities }
func (preset) Open(ctx context.Context, cfg db.Config) (db.Conn, error) {
	return db.OpenWith(ctx, Profile, NativeTransport, cfg)
}

// schemaQuery lists tables and views from the current database's
// information_schema. Athena exposes table_type = 'BASE TABLE' or
// 'VIEW'; information_schema is the only system schema in the
// default AwsDataCatalog view.
const schemaQuery = `
SELECT table_schema  AS schema_name,
       table_name    AS name,
       CASE WHEN table_type = 'VIEW' THEN 1 ELSE 0 END AS is_view,
       CASE WHEN table_schema = 'information_schema' THEN 1 ELSE 0 END AS is_system
FROM information_schema.tables
ORDER BY table_schema, table_name
`

// columnsQuery returns ordered columns for one table. athenadriver
// supports ? positional placeholders; interpolation mode substitutes
// them client-side before submitting the Athena QueryExecution.
const columnsQuery = `
SELECT column_name, data_type
FROM information_schema.columns
WHERE table_schema = ? AND table_name = ?
ORDER BY ordinal_position
`

// fetchDefinition runs SHOW CREATE TABLE|VIEW on the qualified name.
// Athena returns the DDL as one row / one column. Procedures aren't
// a first-class Athena object; those kinds return the unsupported
// sentinel.
func fetchDefinition(ctx context.Context, sqlDB *sql.DB, kind, schema, name string) (string, error) {
	var keyword string
	switch kind {
	case "view":
		keyword = "VIEW"
	case "table":
		keyword = "TABLE"
	default:
		return "", db.ErrDefinitionUnsupported
	}
	qualified := quoteIdent(schema) + "." + quoteIdent(name)
	q := fmt.Sprintf("SHOW CREATE %s %s", keyword, qualified)
	var body sql.NullString
	if err := sqlDB.QueryRowContext(ctx, q).Scan(&body); err != nil {
		return "", fmt.Errorf("show create %s %s: %w", keyword, qualified, err)
	}
	if !body.Valid || strings.TrimSpace(body.String) == "" {
		return "", fmt.Errorf("no definition for %s %s.%s", kind, schema, name)
	}
	return strings.TrimRight(body.String, "\r\n\t ;") + ";", nil
}

// quoteIdent wraps identifiers in double quotes (ANSI / Presto /
// Athena). Embedded double quotes are doubled.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// buildDSN produces a DSN accepted by athenadriver:
//
//	s3://<bucket>/<path>?region=...&db=...&accessID=...&secretAccessKey=...
//
// The scheme is literally "s3"; the path is the Athena query-results
// output location (an S3 bucket + optional prefix). output_location
// is required -- the driver rejects DSNs with no bucket.
//
// Credentials: the driver picks accessID / secretAccessKey / sessionToken
// out of the query first, then falls through to AWS_* env vars and
// the default SDK credential chain if AWS_SDK_LOAD_CONFIG=1 is set.
// For instance-role auth, leave User/Password empty and the driver
// will use EC2/ECS metadata.
//
// Unknown Options keys pass through so future driver knobs work
// without a buildDSN patch.
func buildDSN(cfg db.Config) string {
	// output_location is the s3://bucket/path base. Accept either a
	// full "s3://bucket/path" string or a bare "bucket/path" and
	// normalize. Empty output_location produces an empty bucket slot
	// -- the driver will reject this at Open, giving the user a
	// clear AWS-side error message rather than a sqlgo surprise.
	raw := strings.TrimSpace(cfg.Options["output_location"])
	output := strings.TrimPrefix(raw, "s3://")
	// Trim any stray leading slash (bare path like "/bucket/..." is
	// nonsensical for S3); keep the trailing slash as a signal that
	// the value is a prefix rather than an object key.
	output = strings.TrimLeft(output, "/")
	trailing := strings.HasSuffix(output, "/")
	output = strings.TrimRight(output, "/")

	bucket, objectPath, _ := strings.Cut(output, "/")

	u := url.URL{
		Scheme: "s3",
		Host:   bucket,
	}
	if objectPath != "" {
		u.Path = "/" + objectPath
		if trailing {
			u.Path += "/"
		}
	} else if trailing && bucket != "" {
		// "s3://bucket/" collapses to Host="bucket" Path="/" so the
		// rendered DSN keeps the user-visible trailing slash.
		u.Path = "/"
	}

	q := u.Query()

	if cfg.User != "" {
		q.Set("accessID", cfg.User)
	}
	if cfg.Password != "" {
		q.Set("secretAccessKey", cfg.Password)
	}
	if cfg.Database != "" {
		q.Set("db", cfg.Database)
	}

	// Semantic option mapping. Already-set keys win over raw passthrough.
	semantic := map[string]string{
		"region":         "region",
		"session_token":  "sessionToken",
		"workgroup":      "workgroupName",
		"aws_profile":    "AWSProfile",
		"catalog":        "catalog",
		"poll_interval":  "resultPollIntervalSeconds",
		"read_only":      "ReadOnly",
		"moneywise":      "MoneyWise",
		"missing_as_nil": "missingAsNil",
		"tag":            "tag",
	}
	// "output_location" was consumed into the URL base.
	skip := map[string]struct{}{"output_location": {}}
	for src, dst := range semantic {
		if v := strings.TrimSpace(cfg.Options[src]); v != "" {
			q.Set(dst, v)
		}
		skip[src] = struct{}{}
	}

	// Pass through any remaining Options untouched.
	for k, v := range cfg.Options {
		if v == "" {
			continue
		}
		if _, drop := skip[k]; drop {
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
