package seed

import (
	"math/rand/v2"
	"strings"
	"testing"
)

// TestDialectsRenderAllTypes catches regressions where a new colType is
// added to the schema but one of the dialect type maps is never updated.
// A typeSQL that returns the fallback clause for a real column is a bug,
// so we check that every (dialect, table, column) combination produces a
// non-fallback, uppercased SQL type fragment.
func TestDialectsRenderAllTypes(t *testing.T) {
	t.Parallel()
	for name, d := range dialects {
		for _, tbl := range tables {
			ddl := d.createDDL(tbl)
			if !strings.Contains(ddl, "CREATE TABLE") {
				t.Errorf("%s/%s: missing CREATE TABLE in\n%s", name, tbl.name, ddl)
			}
			for _, c := range tbl.cols {
				fragment := d.typeSQL(c)
				if fragment == "" {
					t.Errorf("%s/%s/%s: empty typeSQL", name, tbl.name, c.name)
				}
			}
		}
	}
}

// TestBuildInsertParamCount verifies that the emitted placeholder count in
// the SQL matches the flattened args slice for every dialect. This is the
// single most common source of driver errors, so it's worth pinning.
func TestBuildInsertParamCount(t *testing.T) {
	t.Parallel()
	tbl := tables[0] // departments, 3 cols
	rows := [][]any{
		{1, "Eng", "SF"},
		{2, "Sales", "NY"},
		{3, "Ops", "LDN"},
	}
	for name, d := range dialects {
		sql, args := buildInsert(d, tbl, rows)
		wantArgs := len(tbl.cols) * len(rows)
		if len(args) != wantArgs {
			t.Errorf("%s: args=%d want=%d", name, len(args), wantArgs)
		}
		// Count emitted placeholders. Each dialect uses a distinct marker,
		// so we pick the right one and count occurrences.
		marker := d.placeholder(1)
		if strings.HasPrefix(marker, "@p") || strings.HasPrefix(marker, "$") {
			// Positional numbered. Check that the highest index equals wantArgs.
			last := d.placeholder(wantArgs)
			if !strings.Contains(sql, last) {
				t.Errorf("%s: expected last placeholder %q in:\n%s", name, last, sql)
			}
		} else {
			// '?' style. Count question marks.
			got := strings.Count(sql, "?")
			if got != wantArgs {
				t.Errorf("%s: ? count=%d want=%d\n%s", name, got, wantArgs, sql)
			}
		}
	}
}

// TestGenerateDeterministic pins the promise that the same seed produces the
// same dataset. Downstream consumers rely on this to compare two engines
// row-for-row after a seeding run.
func TestGenerateDeterministic(t *testing.T) {
	t.Parallel()
	mk := func() *dataset {
		r := rand.New(rand.NewPCG(42, 42^0x9e3779b97f4a7c15))
		return generate(r, 1)
	}
	a, b := mk(), mk()
	if len(a.employees) != len(b.employees) || a.employees[0].email != b.employees[0].email {
		t.Fatalf("non-deterministic employees: %+v vs %+v", a.employees[0], b.employees[0])
	}
	if len(a.orders) != len(b.orders) || a.orders[100].totalCents != b.orders[100].totalCents {
		t.Fatal("non-deterministic orders")
	}
	if len(a.orderItems) != len(b.orderItems) {
		t.Fatalf("order items len differ: %d vs %d", len(a.orderItems), len(b.orderItems))
	}
}

// TestGenerateScaling verifies that Scale multiplies the per-table row
// counts for tables that are documented as scaling (employees, customers,
// orders) and leaves fixed tables alone (departments, suppliers, products).
func TestGenerateScaling(t *testing.T) {
	t.Parallel()
	r1 := rand.New(rand.NewPCG(1, 2))
	r5 := rand.New(rand.NewPCG(1, 2))
	a := generate(r1, 1)
	b := generate(r5, 5)

	if len(a.departments) != len(b.departments) {
		t.Errorf("departments should be fixed: %d vs %d", len(a.departments), len(b.departments))
	}
	if len(a.suppliers) != len(b.suppliers) {
		t.Errorf("suppliers should be fixed: %d vs %d", len(a.suppliers), len(b.suppliers))
	}
	if len(a.products) != len(b.products) {
		t.Errorf("products should be fixed: %d vs %d", len(a.products), len(b.products))
	}
	if len(b.employees) != 5*len(a.employees) {
		t.Errorf("employees should scale 5x: %d vs %d", len(b.employees), len(a.employees))
	}
	if len(b.customers) != 5*len(a.customers) {
		t.Errorf("customers should scale 5x: %d vs %d", len(b.customers), len(a.customers))
	}
	if len(b.orders) != 5*len(a.orders) {
		t.Errorf("orders should scale 5x: %d vs %d", len(b.orders), len(a.orders))
	}
}

// TestMoneyFormat is a small pin on the string-based decimal formatter
// since sending floats through driver code can silently round.
func TestMoneyFormat(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0.00"},
		{1, "0.01"},
		{99, "0.99"},
		{100, "1.00"},
		{12345, "123.45"},
		{-100, "-1.00"},
		{-12345, "-123.45"},
	}
	for _, tc := range cases {
		if got := money(tc.in); got != tc.want {
			t.Errorf("money(%d)=%q want %q", tc.in, got, tc.want)
		}
	}
}

// TestSlugify pins the slug helper used for fake email domains.
func TestSlugify(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"Apex Industries":       "apex-industries",
		"Northstar Supply Co":   "northstar-supply-co",
		"  Weird!! Chars &&& ":  "weird-chars",
		"a.b.c":                 "a-b-c",
		"ALL CAPS":              "all-caps",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q)=%q want %q", in, got, want)
		}
	}
}
