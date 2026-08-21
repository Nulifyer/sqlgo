package secret

import "strings"

// RedactedPlaceholder replaces a plaintext secret in exported connection
// metadata. Importers treat it as "preserve the local value if one exists"
// rather than as a credential to send to a driver.
const RedactedPlaceholder = "{sqlgo:redacted}"

// RedactStoredValue replaces plaintext with the export placeholder while
// preserving empty values and keyring references.
func RedactStoredValue(value string) string {
	if value == "" || value == Placeholder || value == RedactedPlaceholder {
		return value
	}
	return RedactedPlaceholder
}

// RestoreRedactedValue preserves the current local value when an import
// contains the export placeholder. For a new connection current is empty, so
// the placeholder cannot accidentally become a driver credential.
func RestoreRedactedValue(imported, current string) string {
	if imported == RedactedPlaceholder {
		return current
	}
	return imported
}

// IsSensitiveOption reports whether an adapter option is likely to contain a
// credential rather than ordinary connection metadata. Adapter-native names
// vary between snake_case and camelCase, so matching uses a compact lowercase
// form. File paths and known boolean knobs are explicitly safe.
func IsSensitiveOption(name string) bool {
	compact := compactOptionName(name)
	if compact == "" {
		return false
	}

	switch compact {
	case "allowcleartextpasswords", "passcodeinpassword":
		return false
	}
	if strings.HasSuffix(compact, "tokenfile") ||
		strings.HasSuffix(compact, "tokenpath") ||
		strings.HasSuffix(compact, "keyfile") ||
		strings.HasSuffix(compact, "keypath") ||
		strings.HasSuffix(compact, "certfile") ||
		strings.HasSuffix(compact, "certpath") {
		return false
	}
	// These adapter options name credential files, not inline credentials.
	if compact == "credentials" || compact == "credentialfile" || compact == "credentialpath" {
		return false
	}

	if strings.Contains(compact, "password") ||
		strings.Contains(compact, "passwd") ||
		strings.Contains(compact, "passphrase") ||
		strings.Contains(compact, "secret") ||
		strings.Contains(compact, "token") ||
		strings.Contains(compact, "credential") {
		return true
	}

	switch compact {
	case "passcode", "apikey", "accesskey", "privatekey", "privatekeyfilepwd", "signingkey", "ospass", "authorization":
		return true
	}
	return false
}

// RedactOptions returns a copy suitable for display or export. Non-sensitive
// options are unchanged and the caller's map is never mutated.
func RedactOptions(options map[string]string) map[string]string {
	if len(options) == 0 {
		return nil
	}
	out := make(map[string]string, len(options))
	for key, value := range options {
		if value != "" && IsSensitiveOption(key) {
			value = RedactedPlaceholder
		}
		out[key] = value
	}
	return out
}

// RestoreRedactedOptions returns imported options with redacted values filled
// from current. A redacted option absent from current is omitted so it cannot
// reach a driver as a literal placeholder.
func RestoreRedactedOptions(imported, current map[string]string) map[string]string {
	if len(imported) == 0 {
		return nil
	}
	out := make(map[string]string, len(imported))
	for key, value := range imported {
		if value == RedactedPlaceholder {
			if previous, ok := current[key]; ok {
				out[key] = previous
			}
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func compactOptionName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch r {
		case '_', '-', '.', ' ', '/':
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
