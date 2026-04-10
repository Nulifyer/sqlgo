package tui

import (
	"testing"

	"github.com/Nulifyer/sqlgo/internal/config"
)

// TestPostgresSSLModeCyclerWrapsThroughKnownValues drives the
// connection form's sslmode field end-to-end: Right arrow steps
// through every postgresSSLModeValues entry in order and wraps
// back to the first.
func TestPostgresSSLModeCyclerWrapsThroughKnownValues(t *testing.T) {
	t.Parallel()
	f := newConnForm("test", &config.Connection{Driver: "postgres"})
	// Navigate to the sslmode field. It's the first engine field on
	// the postgres spec, so coreCount is the index.
	f.active = coreCount
	ff := f.activeField()
	if ff == nil || !ff.isCycler() {
		t.Fatalf("active field at coreCount should be sslmode cycler, got %+v", ff)
	}

	// Initial value is empty (driver default).
	if got := ff.in.String(); got != "" {
		t.Errorf("initial sslmode = %q, want empty", got)
	}

	// Right-arrow should step through postgresSSLModeValues in
	// order, wrapping back to "" after verify-full.
	for i := 1; i < len(postgresSSLModeValues); i++ {
		f.handle(Key{Kind: KeyRight})
		want := postgresSSLModeValues[i]
		if got := ff.in.String(); got != want {
			t.Errorf("after %d Right: sslmode = %q, want %q", i, got, want)
		}
	}
	// One more Right wraps back to the first entry.
	f.handle(Key{Kind: KeyRight})
	if got := ff.in.String(); got != postgresSSLModeValues[0] {
		t.Errorf("after wrap: sslmode = %q, want %q", got, postgresSSLModeValues[0])
	}
}

// TestCyclerLeftArrowGoesBackwards verifies the reverse direction
// also walks and wraps.
func TestCyclerLeftArrowGoesBackwards(t *testing.T) {
	t.Parallel()
	f := newConnForm("test", &config.Connection{Driver: "postgres"})
	f.active = coreCount
	ff := f.activeField()

	// From empty, Left should wrap to the last entry.
	f.handle(Key{Kind: KeyLeft})
	last := postgresSSLModeValues[len(postgresSSLModeValues)-1]
	if got := ff.in.String(); got != last {
		t.Errorf("Left from empty = %q, want %q", got, last)
	}
	// Left again steps to the second-to-last.
	f.handle(Key{Kind: KeyLeft})
	want := postgresSSLModeValues[len(postgresSSLModeValues)-2]
	if got := ff.in.String(); got != want {
		t.Errorf("Left #2 = %q, want %q", got, want)
	}
}

// TestCyclerSwallowsPrintableKeys covers the "don't accidentally
// type into a non-editable row" rule. Before the cycler lived on
// engine options, typing 'x' would land in the sslmode input field.
func TestCyclerSwallowsPrintableKeys(t *testing.T) {
	t.Parallel()
	f := newConnForm("test", &config.Connection{Driver: "postgres"})
	f.active = coreCount
	ff := f.activeField()
	f.handle(Key{Kind: KeyRune, Rune: 'x'})
	if got := ff.in.String(); got != "" {
		t.Errorf("after printable key, sslmode = %q, want empty", got)
	}
}

// TestCyclerUnknownValueResetsToFirstEntry: imported values that
// aren't in the known set (e.g. a hand-edited JSON with a typo or
// a future value we don't recognize yet) should recover cleanly on
// the first cycler press instead of locking the user out.
func TestCyclerUnknownValueResetsToFirstEntry(t *testing.T) {
	t.Parallel()
	f := newConnForm("test", &config.Connection{
		Driver:  "postgres",
		Options: map[string]string{"sslmode": "not-a-real-mode"},
	})
	f.active = coreCount
	ff := f.activeField()
	if got := ff.in.String(); got != "not-a-real-mode" {
		t.Fatalf("imported sslmode = %q, want preserved", got)
	}
	f.handle(Key{Kind: KeyRight})
	if got := ff.in.String(); got != postgresSSLModeValues[0] {
		t.Errorf("after Right on unknown value, sslmode = %q, want %q", got, postgresSSLModeValues[0])
	}
}

// TestMSSQLEncryptFieldIsCycler confirms the encrypt field on the
// MSSQL spec is constrained, not free-form.
func TestMSSQLEncryptFieldIsCycler(t *testing.T) {
	t.Parallel()
	f := newConnForm("test", &config.Connection{Driver: "mssql"})
	// encrypt is the first engine field on the mssql spec.
	f.active = coreCount
	ff := f.activeField()
	if ff == nil || !ff.isCycler() {
		t.Errorf("mssql encrypt should be a cycler, got %+v", ff)
	}
}

// TestMySQLTLSFieldIsCycler confirms the tls field on the MySQL
// spec is constrained.
func TestMySQLTLSFieldIsCycler(t *testing.T) {
	t.Parallel()
	f := newConnForm("test", &config.Connection{Driver: "mysql"})
	f.active = coreCount
	ff := f.activeField()
	if ff == nil || !ff.isCycler() {
		t.Errorf("mysql tls should be a cycler, got %+v", ff)
	}
}

// TestFreeFormEngineFieldStillTakesInput guards against a
// regression where the cycler branch accidentally swallows input
// on non-constrained engine fields.
func TestFreeFormEngineFieldStillTakesInput(t *testing.T) {
	t.Parallel()
	f := newConnForm("test", &config.Connection{Driver: "postgres"})
	// application_name is the third postgres field (after sslmode
	// and sslrootcert) and is NOT a cycler.
	f.active = coreCount + 2
	ff := f.activeField()
	if ff == nil || ff.isCycler() {
		t.Fatalf("application_name should be free-form, got %+v", ff)
	}
	// Type "myapp".
	for _, r := range "myapp" {
		f.handle(Key{Kind: KeyRune, Rune: r})
	}
	if got := ff.in.String(); got != "myapp" {
		t.Errorf("application_name = %q, want %q", got, "myapp")
	}
}

// TestCyclerValueRoundTripsThroughToConnection verifies the
// saved connection carries the cycler's current value out into
// the Options map so it actually reaches the DSN builder.
func TestCyclerValueRoundTripsThroughToConnection(t *testing.T) {
	t.Parallel()
	f := newConnForm("test", &config.Connection{Driver: "postgres"})
	f.fixed[coreName].in.SetString("test")
	f.fixed[coreHost].in.SetString("db.example.com")
	f.active = coreCount
	// Step to "require".
	for _, v := range postgresSSLModeValues {
		if v == "require" {
			break
		}
		f.handle(Key{Kind: KeyRight})
	}

	c, err := f.toConnection()
	if err != nil {
		t.Fatalf("toConnection: %v", err)
	}
	if got := c.Options["sslmode"]; got != "require" {
		t.Errorf("Options[sslmode] = %q, want %q", got, "require")
	}
}

// TestCyclerEmptyValueIsOmittedFromOptions verifies the "(default)"
// state (empty string) doesn't accidentally write an empty sslmode
// key into the saved Options map -- that would be a silent DSN
// difference from "unset".
func TestCyclerEmptyValueIsOmittedFromOptions(t *testing.T) {
	t.Parallel()
	f := newConnForm("test", &config.Connection{Driver: "postgres"})
	f.fixed[coreName].in.SetString("test")
	f.fixed[coreHost].in.SetString("db.example.com")
	// Leave sslmode at its default empty.
	c, err := f.toConnection()
	if err != nil {
		t.Fatalf("toConnection: %v", err)
	}
	if _, present := c.Options["sslmode"]; present {
		t.Errorf("Options[sslmode] present but should be omitted when empty: %+v", c.Options)
	}
}
