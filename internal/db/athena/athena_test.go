package athena

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	ststypes "github.com/aws/aws-sdk-go-v2/service/sts/types"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/sqltok"
)

// TestBuildDSN covers the s3://<bucket>/<path>?... form, credential
// placement, output_location normalization, and raw passthrough.
// athenadriver DSNs use the literal "s3://" scheme so url.Parse
// round-trips -- we split on '?' for prefix inspection and parse the
// query ourselves (url.QueryUnescape for slashes in values).
func TestBuildDSN(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		cfg          db.Config
		wantPrefix   string
		wantParams   map[string]string
		absentParams []string
	}{
		{
			name: "static_creds_happy_path",
			cfg: db.Config{
				User:     "AKIA1234",
				Password: "secret-key",
				Database: "sales",
				Options: map[string]string{
					"region":          "us-east-1",
					"output_location": "s3://athena-results/queries/",
					"workgroup":       "primary",
				},
			},
			wantPrefix: "s3://athena-results/queries/",
			wantParams: map[string]string{
				"accessID":        "AKIA1234",
				"secretAccessKey": "secret-key",
				"db":              "sales",
				"region":          "us-east-1",
				"workgroupName":   "primary",
			},
		},
		{
			name: "output_location_bucket_only",
			cfg: db.Config{
				User:     "AKIA1234",
				Password: "secret",
				Options: map[string]string{
					"region":          "us-west-2",
					"output_location": "s3://just-a-bucket",
				},
			},
			wantPrefix: "s3://just-a-bucket",
			wantParams: map[string]string{
				"accessID":        "AKIA1234",
				"secretAccessKey": "secret",
				"region":          "us-west-2",
			},
		},
		{
			name: "output_location_no_scheme_normalized",
			cfg: db.Config{
				Options: map[string]string{
					"region":          "us-east-1",
					"output_location": "my-bucket/athena/",
				},
			},
			wantPrefix: "s3://my-bucket/athena/",
			wantParams: map[string]string{"region": "us-east-1"},
		},
		{
			name: "iam_role_no_static_creds",
			cfg: db.Config{
				Database: "analytics",
				Options: map[string]string{
					"region":          "eu-west-1",
					"output_location": "s3://results/",
				},
			},
			wantPrefix: "s3://results/",
			wantParams: map[string]string{
				"region": "eu-west-1",
				"db":     "analytics",
			},
			absentParams: []string{"accessID", "secretAccessKey"},
		},
		{
			name: "sts_session_token",
			cfg: db.Config{
				User:     "ASIA1234",
				Password: "temp-secret",
				Database: "ops",
				Options: map[string]string{
					"region":          "us-east-1",
					"output_location": "s3://results/",
					"session_token":   "FwoGZXIvYXdzEJr//",
				},
			},
			wantPrefix: "s3://results/",
			wantParams: map[string]string{
				"accessID":        "ASIA1234",
				"secretAccessKey": "temp-secret",
				"sessionToken":    "FwoGZXIvYXdzEJr//",
			},
		},
		{
			name: "aws_profile_named",
			cfg: db.Config{
				Options: map[string]string{
					"region":          "us-east-1",
					"output_location": "s3://results/",
					"aws_profile":     "dev-sandbox",
				},
			},
			wantPrefix: "s3://results/",
			wantParams: map[string]string{
				"region":     "us-east-1",
				"AWSProfile": "dev-sandbox",
			},
		},
		{
			name: "tuning_options_passthrough",
			cfg: db.Config{
				User:     "AKIA1234",
				Password: "secret",
				Options: map[string]string{
					"region":          "us-east-1",
					"output_location": "s3://results/",
					"poll_interval":   "5",
					"read_only":       "true",
					"moneywise":       "true",
					"missing_as_nil":  "true",
					"catalog":         "AwsDataCatalog",
					"tag":             "team=data|env=prod",
				},
			},
			wantParams: map[string]string{
				"resultPollIntervalSeconds": "5",
				"ReadOnly":                  "true",
				"MoneyWise":                 "true",
				"missingAsNil":              "true",
				"catalog":                   "AwsDataCatalog",
				"tag":                       "team=data|env=prod",
			},
		},
		{
			name: "empty_option_absent",
			cfg: db.Config{
				User:     "AKIA1234",
				Password: "secret",
				Options: map[string]string{
					"region":          "us-east-1",
					"output_location": "s3://results/",
					"workgroup":       "",
					"poll_interval":   "3",
				},
			},
			wantParams:   map[string]string{"resultPollIntervalSeconds": "3"},
			absentParams: []string{"workgroupName"},
		},
		{
			name: "passthrough_unknown_option",
			cfg: db.Config{
				Options: map[string]string{
					"region":          "us-east-1",
					"output_location": "s3://results/",
					"custom_knob":     "value",
				},
			},
			wantParams: map[string]string{"custom_knob": "value"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dsn := buildDSN(tc.cfg)
			if !strings.HasPrefix(dsn, "s3://") {
				t.Errorf("DSN missing s3:// scheme: %q", dsn)
			}
			prefix, rawQuery, _ := strings.Cut(dsn, "?")
			if tc.wantPrefix != "" && prefix != tc.wantPrefix {
				t.Errorf("prefix = %q, want %q (dsn=%s)", prefix, tc.wantPrefix, dsn)
			}

			got := map[string]string{}
			if rawQuery != "" {
				for _, pair := range strings.Split(rawQuery, "&") {
					k, v, ok := strings.Cut(pair, "=")
					if !ok {
						continue
					}
					if dec, err := url.QueryUnescape(v); err == nil {
						v = dec
					}
					got[k] = v
				}
			}
			for k, want := range tc.wantParams {
				if g := got[k]; g != want {
					t.Errorf("q[%q] = %q, want %q (dsn=%s)", k, g, want, dsn)
				}
			}
			for _, k := range tc.absentParams {
				if _, ok := got[k]; ok {
					t.Errorf("q[%q] present (=%q), want absent (dsn=%s)", k, got[k], dsn)
				}
			}
		})
	}
}

// TestPreset_Capabilities pins the capability fingerprint so drift in
// Profile -> preset wiring surfaces as a test failure.
func TestPreset_Capabilities(t *testing.T) {
	t.Parallel()
	got := preset{}.Capabilities()
	if got.IdentifierQuote != '"' {
		t.Errorf("IdentifierQuote = %q, want '\"'", got.IdentifierQuote)
	}
	if got.SupportsTransactions {
		t.Error("SupportsTransactions should be false (Athena Begin returns ErrAthenaTransactionUnsupported)")
	}
	if got.Dialect != sqltok.DialectTrino {
		t.Errorf("Dialect = %v, want DialectTrino", got.Dialect)
	}
	if got.SchemaDepth != db.SchemaDepthSchemas {
		t.Errorf("SchemaDepth = %v, want SchemaDepthSchemas", got.SchemaDepth)
	}
	if got.LimitSyntax != db.LimitSyntaxLimit {
		t.Errorf("LimitSyntax = %v, want LimitSyntaxLimit", got.LimitSyntax)
	}
	if got.ExplainFormat != db.ExplainFormatNone {
		t.Errorf("ExplainFormat = %v, want ExplainFormatNone", got.ExplainFormat)
	}
	if !got.SupportsTLS {
		t.Error("SupportsTLS should be true (Athena is HTTPS-only)")
	}
	if !got.SupportsCancel {
		t.Error("SupportsCancel should be true (ctx cancellation during polling)")
	}
	if got.SupportsCrossDatabase {
		t.Error("SupportsCrossDatabase should be false (connection pinned to one db)")
	}
}

// fakeSTS is an in-test stsAPI that records inputs and returns a
// scripted result. Each test hands back a fresh instance so the
// package-level newSTSClient override is isolated per subtest.
type fakeSTS struct {
	assumeOut   *sts.AssumeRoleOutput
	assumeErr   error
	assumeIn    *sts.AssumeRoleInput
	webOut      *sts.AssumeRoleWithWebIdentityOutput
	webErr      error
	webIn       *sts.AssumeRoleWithWebIdentityInput
	assumeCalls int
	webCalls    int
}

func (f *fakeSTS) AssumeRole(ctx context.Context, in *sts.AssumeRoleInput, _ ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
	f.assumeCalls++
	f.assumeIn = in
	return f.assumeOut, f.assumeErr
}

func (f *fakeSTS) AssumeRoleWithWebIdentity(ctx context.Context, in *sts.AssumeRoleWithWebIdentityInput, _ ...func(*sts.Options)) (*sts.AssumeRoleWithWebIdentityOutput, error) {
	f.webCalls++
	f.webIn = in
	return f.webOut, f.webErr
}

// withFakeSTS swaps newSTSClient for a stub pointing at f, runs fn,
// then restores the original. Using t.Cleanup keeps the override
// scoped to the calling test even if fn panics.
func withFakeSTS(t *testing.T, f *fakeSTS) {
	t.Helper()
	orig := newSTSClient
	newSTSClient = func(_ context.Context, _ string) (stsAPI, error) { return f, nil }
	t.Cleanup(func() { newSTSClient = orig })
}

// TestApplyAssumeRole_NoArn verifies the STS path is bypassed when
// assume_role_arn is absent -- cfg returns unchanged.
func TestApplyAssumeRole_NoArn(t *testing.T) {
	f := &fakeSTS{}
	withFakeSTS(t, f)
	cfg := db.Config{
		User:     "AKIAEXISTING",
		Password: "stay",
		Options:  map[string]string{"region": "us-east-1"},
	}
	got, err := applyAssumeRole(context.Background(), cfg)
	if err != nil {
		t.Fatalf("applyAssumeRole err: %v", err)
	}
	if got.User != "AKIAEXISTING" || got.Password != "stay" {
		t.Errorf("creds mutated: %+v", got)
	}
	if f.assumeCalls != 0 || f.webCalls != 0 {
		t.Errorf("unexpected STS calls: assume=%d web=%d", f.assumeCalls, f.webCalls)
	}
}

// TestApplyAssumeRole_Basic drives sts:AssumeRole and verifies that
// returned temp credentials overwrite User/Password/session_token and
// the STS-only option keys are stripped from the output map.
func TestApplyAssumeRole_Basic(t *testing.T) {
	f := &fakeSTS{
		assumeOut: &sts.AssumeRoleOutput{
			Credentials: &ststypes.Credentials{
				AccessKeyId:     aws.String("ASIATEMP"),
				SecretAccessKey: aws.String("temp-secret"),
				SessionToken:    aws.String("temp-token"),
			},
		},
	}
	withFakeSTS(t, f)
	cfg := db.Config{
		User:     "AKIAAMBIENT",
		Password: "ambient-secret",
		Options: map[string]string{
			"region":                       "us-east-1",
			"output_location":              "s3://results/",
			"assume_role_arn":              "arn:aws:iam::111:role/analytics",
			"assume_role_session_name":     "ops-session",
			"assume_role_external_id":      "confused-deputy-guard",
			"assume_role_duration_seconds": "1800",
		},
	}
	got, err := applyAssumeRole(context.Background(), cfg)
	if err != nil {
		t.Fatalf("applyAssumeRole err: %v", err)
	}
	if f.assumeCalls != 1 {
		t.Fatalf("AssumeRole calls = %d, want 1", f.assumeCalls)
	}
	in := f.assumeIn
	if aws.ToString(in.RoleArn) != "arn:aws:iam::111:role/analytics" {
		t.Errorf("RoleArn = %q", aws.ToString(in.RoleArn))
	}
	if aws.ToString(in.RoleSessionName) != "ops-session" {
		t.Errorf("RoleSessionName = %q", aws.ToString(in.RoleSessionName))
	}
	if aws.ToString(in.ExternalId) != "confused-deputy-guard" {
		t.Errorf("ExternalId = %q", aws.ToString(in.ExternalId))
	}
	if in.DurationSeconds == nil || *in.DurationSeconds != 1800 {
		t.Errorf("DurationSeconds = %v, want 1800", in.DurationSeconds)
	}
	if got.User != "ASIATEMP" || got.Password != "temp-secret" {
		t.Errorf("temp creds not injected: user=%q password=%q", got.User, got.Password)
	}
	if got.Options["session_token"] != "temp-token" {
		t.Errorf("session_token = %q, want temp-token", got.Options["session_token"])
	}
	for _, k := range []string{
		"assume_role_arn", "assume_role_session_name", "assume_role_external_id",
		"assume_role_duration_seconds", "web_identity_token_file",
	} {
		if _, ok := got.Options[k]; ok {
			t.Errorf("Options[%q] should be stripped after assume-role", k)
		}
	}
	if got.Options["region"] != "us-east-1" {
		t.Errorf("region option dropped; got=%v", got.Options)
	}
	if cfg.Options["assume_role_arn"] == "" {
		t.Error("input cfg.Options mutated (want pure-returning applyAssumeRole)")
	}
}

// TestApplyAssumeRole_DefaultSession verifies the session name falls
// back to "sqlgo" when no assume_role_session_name is provided.
func TestApplyAssumeRole_DefaultSession(t *testing.T) {
	f := &fakeSTS{
		assumeOut: &sts.AssumeRoleOutput{
			Credentials: &ststypes.Credentials{
				AccessKeyId:     aws.String("A"),
				SecretAccessKey: aws.String("S"),
				SessionToken:    aws.String("T"),
			},
		},
	}
	withFakeSTS(t, f)
	cfg := db.Config{Options: map[string]string{"assume_role_arn": "arn:aws:iam::1:role/x"}}
	if _, err := applyAssumeRole(context.Background(), cfg); err != nil {
		t.Fatalf("applyAssumeRole: %v", err)
	}
	if got := aws.ToString(f.assumeIn.RoleSessionName); got != "sqlgo" {
		t.Errorf("RoleSessionName default = %q, want sqlgo", got)
	}
}

// TestApplyAssumeRole_WebIdentity covers the OIDC path (EKS IRSA,
// GitHub OIDC). Reads the token from a tempfile and passes it as
// WebIdentityToken; AssumeRole (non-web) must NOT be called.
func TestApplyAssumeRole_WebIdentity(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenPath, []byte("eyJraWQiOiJrMSJ9.payload.sig\n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	f := &fakeSTS{
		webOut: &sts.AssumeRoleWithWebIdentityOutput{
			Credentials: &ststypes.Credentials{
				AccessKeyId:     aws.String("ASIA-WEB"),
				SecretAccessKey: aws.String("web-secret"),
				SessionToken:    aws.String("web-token"),
			},
		},
	}
	withFakeSTS(t, f)
	cfg := db.Config{
		Options: map[string]string{
			"assume_role_arn":         "arn:aws:iam::1:role/irsa",
			"web_identity_token_file": tokenPath,
		},
	}
	got, err := applyAssumeRole(context.Background(), cfg)
	if err != nil {
		t.Fatalf("applyAssumeRole: %v", err)
	}
	if f.assumeCalls != 0 {
		t.Errorf("AssumeRole called %d times; WebIdentity should take the path", f.assumeCalls)
	}
	if f.webCalls != 1 {
		t.Fatalf("AssumeRoleWithWebIdentity calls = %d, want 1", f.webCalls)
	}
	if tok := aws.ToString(f.webIn.WebIdentityToken); tok != "eyJraWQiOiJrMSJ9.payload.sig" {
		t.Errorf("WebIdentityToken = %q (trimmed expected)", tok)
	}
	if got.User != "ASIA-WEB" || got.Password != "web-secret" || got.Options["session_token"] != "web-token" {
		t.Errorf("web-identity creds not injected: %+v", got)
	}
}

// TestApplyAssumeRole_WebIdentityMissingFile bubbles a filesystem
// error when the token file doesn't exist. STS must not be called.
func TestApplyAssumeRole_WebIdentityMissingFile(t *testing.T) {
	f := &fakeSTS{}
	withFakeSTS(t, f)
	cfg := db.Config{
		Options: map[string]string{
			"assume_role_arn":         "arn:aws:iam::1:role/irsa",
			"web_identity_token_file": filepath.Join(t.TempDir(), "does-not-exist"),
		},
	}
	_, err := applyAssumeRole(context.Background(), cfg)
	if err == nil {
		t.Fatal("want error for missing token file, got nil")
	}
	if !strings.Contains(err.Error(), "read web identity token") {
		t.Errorf("err = %v, want read-token error", err)
	}
	if f.webCalls != 0 || f.assumeCalls != 0 {
		t.Errorf("STS called despite missing token file (assume=%d web=%d)", f.assumeCalls, f.webCalls)
	}
}

// TestApplyAssumeRole_StsError surfaces the STS error through the
// wrapping path; cfg must not be mutated on failure.
func TestApplyAssumeRole_StsError(t *testing.T) {
	f := &fakeSTS{assumeErr: errors.New("AccessDenied: role may not be assumed")}
	withFakeSTS(t, f)
	cfg := db.Config{
		User:     "keep",
		Password: "keep-secret",
		Options:  map[string]string{"assume_role_arn": "arn:aws:iam::1:role/x"},
	}
	got, err := applyAssumeRole(context.Background(), cfg)
	if err == nil {
		t.Fatal("want AssumeRole error, got nil")
	}
	if !strings.Contains(err.Error(), "AssumeRole") {
		t.Errorf("err = %v, want AssumeRole wrapper", err)
	}
	if got.User != "keep" || got.Password != "keep-secret" {
		t.Errorf("cfg mutated on failure: %+v", got)
	}
}

// TestApplyAssumeRole_BadDuration rejects non-integer / non-positive
// duration strings before STS is hit.
func TestApplyAssumeRole_BadDuration(t *testing.T) {
	f := &fakeSTS{}
	withFakeSTS(t, f)
	cases := []string{"abc", "0", "-5"}
	for _, d := range cases {
		d := d
		t.Run(d, func(t *testing.T) {
			cfg := db.Config{Options: map[string]string{
				"assume_role_arn":              "arn:aws:iam::1:role/x",
				"assume_role_duration_seconds": d,
			}}
			if _, err := applyAssumeRole(context.Background(), cfg); err == nil {
				t.Fatalf("want error for duration %q, got nil", d)
			}
			if f.assumeCalls != 0 {
				t.Errorf("AssumeRole should not be called for bad duration")
			}
		})
	}
}

// TestApplyAssumeRole_NilCredentials guards against upstream returning
// an empty response -- we want a clear error, not a silent overwrite
// with empty strings.
func TestApplyAssumeRole_NilCredentials(t *testing.T) {
	f := &fakeSTS{assumeOut: &sts.AssumeRoleOutput{}} // Credentials nil
	withFakeSTS(t, f)
	cfg := db.Config{Options: map[string]string{"assume_role_arn": "arn:aws:iam::1:role/x"}}
	if _, err := applyAssumeRole(context.Background(), cfg); err == nil {
		t.Fatal("want error for nil Credentials, got nil")
	}
}

// TestQuoteIdent ensures double-quote escaping matches ANSI / Presto:
// wrap in ", double any embedded ".
func TestQuoteIdent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"widgets", `"widgets"`},
		{"with space", `"with space"`},
		{`has"quote`, `"has""quote"`},
		{"", `""`},
	}
	for _, tc := range cases {
		if got := quoteIdent(tc.in); got != tc.want {
			t.Errorf("quoteIdent(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
