package secret

import "testing"

func TestIsSensitiveOption(t *testing.T) {
	t.Parallel()
	tests := map[string]bool{
		"access_token":            true,
		"clientSecret":            true,
		"credentials_json":        true,
		"extra_credentials":       true,
		"jwt_token":               true,
		"wallet_password":         true,
		"private_key_passphrase":  true,
		"privateKeyFilePwd":       true,
		"OS PASS":                 true,
		"api-key":                 true,
		"jwt_token_file":          false,
		"web_identity_token_file": false,
		"private_key_path":        false,
		"credentials":             false,
		"allowCleartextPasswords": false,
		"passcode_in_password":    false,
		"database_role":           false,
		"timeout":                 false,
	}
	for name, want := range tests {
		name, want := name, want
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := IsSensitiveOption(name); got != want {
				t.Fatalf("IsSensitiveOption(%q) = %v, want %v", name, got, want)
			}
		})
	}
}

func TestRedactAndRestoreOptions(t *testing.T) {
	t.Parallel()
	original := map[string]string{
		"schema":       "analytics",
		"access_token": "secret-token",
	}
	redacted := RedactOptions(original)
	if redacted["schema"] != "analytics" {
		t.Fatalf("schema = %q", redacted["schema"])
	}
	if redacted["access_token"] != RedactedPlaceholder {
		t.Fatalf("access_token = %q", redacted["access_token"])
	}
	if original["access_token"] != "secret-token" {
		t.Fatal("RedactOptions mutated its input")
	}

	restored := RestoreRedactedOptions(redacted, original)
	if restored["access_token"] != "secret-token" || restored["schema"] != "analytics" {
		t.Fatalf("restored = %#v", restored)
	}
	if got := RestoreRedactedOptions(redacted, nil); len(got) != 1 || got["schema"] != "analytics" {
		t.Fatalf("new-connection restore = %#v", got)
	}
}
