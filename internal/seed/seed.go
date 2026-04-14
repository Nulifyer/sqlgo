// Package seed populates a database with a fictional-company dataset used
// for development and manual testing of the sqlgo TUI. It talks to any
// backend that implements db.Conn, with per-dialect rendering for the DDL
// and parameter placeholders.
//
// The dataset is deterministic for a given -seed value, so two runs against
// two different engines produce byte-identical logical content, which makes
// side-by-side comparisons useful.
package seed

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/Nulifyer/sqlgo/internal/db"
)

// Options controls a seeding run.
type Options struct {
	// Scale multiplies the base row counts. Scale=1 yields a few thousand
	// rows across all tables; scale=10 yields tens of thousands, etc.
	Scale int
	// Seed is the RNG seed. Same seed -> same dataset across runs and
	// across engines.
	Seed uint64
	// Drop removes existing tables before creating them. Defaults to true
	// because the seeder always produces a fresh dataset keyed on Scale.
	Drop bool
	// Progress receives human-readable progress lines. nil is fine.
	Progress func(string)
}

// Run is the package entry point: resolves a dialect from conn.Driver(),
// runs DDL, and inserts the full dataset.
func Run(ctx context.Context, conn db.Conn, opts Options) error {
	if opts.Scale < 1 {
		opts.Scale = 1
	}
	d, err := dialectFor(conn.Driver())
	if err != nil {
		return err
	}
	logf := func(format string, args ...any) {
		if opts.Progress != nil {
			opts.Progress(fmt.Sprintf(format, args...))
		}
	}

	logf("dialect: %s", d.name)

	if opts.Drop {
		logf("dropping existing extras")
		for _, stmt := range d.dropExtras {
			if err := conn.Exec(ctx, stmt); err != nil {
				return fmt.Errorf("drop extra: %w\n%s", err, stmt)
			}
		}
		logf("dropping existing tables")
		// Reverse order: children before parents. FKs aren't declared but
		// the order still matters if the user later adds them by hand.
		for i := len(tables) - 1; i >= 0; i-- {
			if err := conn.Exec(ctx, d.dropIfExists(tables[i].name)); err != nil {
				return fmt.Errorf("drop %s: %w", tables[i].name, err)
			}
		}
	}

	logf("creating tables")
	for _, t := range tables {
		ddl := d.createDDL(t)
		if err := conn.Exec(ctx, ddl); err != nil {
			return fmt.Errorf("create %s: %w\n%s", t.name, err, ddl)
		}
	}

	//nolint:gosec // not cryptographic; deterministic seeding is the point
	r := rand.New(rand.NewPCG(opts.Seed, opts.Seed^0x9e3779b97f4a7c15))
	data := generate(r, opts.Scale)

	logf("inserting departments: %d", len(data.departments))
	if err := insertRows(ctx, conn, d, tables[0], data.departmentRows()); err != nil {
		return err
	}
	logf("inserting suppliers: %d", len(data.suppliers))
	if err := insertRows(ctx, conn, d, tables[1], data.supplierRows()); err != nil {
		return err
	}
	logf("inserting employees: %d", len(data.employees))
	if err := insertRows(ctx, conn, d, tables[2], data.employeeRows()); err != nil {
		return err
	}
	logf("inserting products: %d", len(data.products))
	if err := insertRows(ctx, conn, d, tables[3], data.productRows()); err != nil {
		return err
	}
	logf("inserting customers: %d", len(data.customers))
	if err := insertRows(ctx, conn, d, tables[4], data.customerRows()); err != nil {
		return err
	}
	logf("inserting users: %d", len(data.users))
	if err := insertRows(ctx, conn, d, tables[5], data.userRows()); err != nil {
		return err
	}
	logf("inserting purchase_orders: %d", len(data.orders))
	if err := insertRows(ctx, conn, d, tables[6], data.orderRows()); err != nil {
		return err
	}
	logf("inserting purchase_order_items: %d", len(data.orderItems))
	if err := insertRows(ctx, conn, d, tables[7], data.orderItemRows()); err != nil {
		return err
	}
	logf("inserting test_notes: %d", len(data.testNotes))
	if err := insertRows(ctx, conn, d, tables[8], data.testNoteRows()); err != nil {
		return err
	}

	if len(d.extras) > 0 {
		logf("creating extras: %d statements", len(d.extras))
		for _, stmt := range d.extras {
			if err := conn.Exec(ctx, stmt); err != nil {
				return fmt.Errorf("extra: %w\n%s", err, stmt)
			}
		}
	}

	logf("done")
	return nil
}

// --- schema -----------------------------------------------------------------

// colType is an abstract type; per-dialect SQL comes from dialect.typeSQL.
type colType int

const (
	colInt colType = iota
	colBigInt
	colText     // variable-length string; size in colDef.size
	colLongText // large text; TEXT / NVARCHAR(MAX). Used by test_notes.
	colDecimal  // fixed precision; size=precision, scale=colDef.scale
	colBool     // 0/1 semantics
	colDate     // date only
	colDateTime // date + time
)

type colDef struct {
	name     string
	typ      colType
	size     int  // length for colText, precision for colDecimal
	scale    int  // for colDecimal
	nullable bool
	pk       bool
}

type tableDef struct {
	name string
	cols []colDef
}

// tables is the schema definition. Order matters for FKs (parents first);
// dropIfExists iterates in reverse. Column names are ANSI-safe.
var tables = []tableDef{
	{
		name: "departments",
		cols: []colDef{
			{name: "id", typ: colInt, pk: true},
			{name: "name", typ: colText, size: 80},
			{name: "location", typ: colText, size: 80},
		},
	},
	{
		name: "suppliers",
		cols: []colDef{
			{name: "id", typ: colInt, pk: true},
			{name: "name", typ: colText, size: 120},
			{name: "contact_email", typ: colText, size: 120},
			{name: "country", typ: colText, size: 60},
		},
	},
	{
		name: "employees",
		cols: []colDef{
			{name: "id", typ: colInt, pk: true},
			{name: "first_name", typ: colText, size: 40},
			{name: "last_name", typ: colText, size: 40},
			{name: "email", typ: colText, size: 120},
			{name: "hire_date", typ: colDate},
			{name: "department_id", typ: colInt},
			{name: "manager_id", typ: colInt, nullable: true},
			{name: "salary", typ: colDecimal, size: 10, scale: 2},
		},
	},
	{
		name: "products",
		cols: []colDef{
			{name: "id", typ: colInt, pk: true},
			{name: "sku", typ: colText, size: 40},
			{name: "name", typ: colText, size: 120},
			{name: "description", typ: colText, size: 400},
			{name: "unit_price", typ: colDecimal, size: 10, scale: 2},
			{name: "supplier_id", typ: colInt},
			{name: "category", typ: colText, size: 60},
			{name: "stock_qty", typ: colInt},
		},
	},
	{
		name: "customers",
		cols: []colDef{
			{name: "id", typ: colInt, pk: true},
			{name: "name", typ: colText, size: 120},
			{name: "email", typ: colText, size: 120},
			{name: "phone", typ: colText, size: 40},
			{name: "city", typ: colText, size: 80},
			{name: "country", typ: colText, size: 60},
			{name: "created_at", typ: colDateTime},
		},
	},
	{
		name: "users",
		cols: []colDef{
			{name: "id", typ: colInt, pk: true},
			{name: "username", typ: colText, size: 60},
			{name: "email", typ: colText, size: 120},
			{name: "employee_id", typ: colInt, nullable: true},
			{name: "role", typ: colText, size: 40},
			{name: "is_active", typ: colBool},
			{name: "created_at", typ: colDateTime},
		},
	},
	{
		name: "purchase_orders",
		cols: []colDef{
			{name: "id", typ: colInt, pk: true},
			{name: "customer_id", typ: colInt},
			{name: "employee_id", typ: colInt},
			{name: "order_date", typ: colDateTime},
			{name: "status", typ: colText, size: 20},
			{name: "total_amount", typ: colDecimal, size: 12, scale: 2},
		},
	},
	{
		name: "purchase_order_items",
		cols: []colDef{
			{name: "id", typ: colBigInt, pk: true},
			{name: "order_id", typ: colInt},
			{name: "product_id", typ: colInt},
			{name: "quantity", typ: colInt},
			{name: "unit_price", typ: colDecimal, size: 10, scale: 2},
			{name: "line_total", typ: colDecimal, size: 12, scale: 2},
		},
	},
	// test_notes holds curated edge-case text rows used to exercise
	// the TUI's rendering: escape-char display, wrap mode, cell
	// inspector, export escaping, unicode width, filter/search on
	// trap characters. Rows are deterministic (not RNG-driven) so
	// the same set shows up across every seeded run. content is
	// LONGTEXT so arbitrarily long payloads don't need a size cap.
	{
		name: "test_notes",
		cols: []colDef{
			{name: "id", typ: colInt, pk: true},
			{name: "label", typ: colText, size: 80},
			{name: "category", typ: colText, size: 40},
			{name: "content", typ: colLongText},
			{name: "note", typ: colText, size: 200},
		},
	},
}

// --- dialects ---------------------------------------------------------------

type dialect struct {
	name         string
	typeSQL      func(c colDef) string
	placeholder  func(idx1 int) string // 1-based
	quoteIdent   func(name string) string
	dropIfExists func(name string) string
	// maxParams is the hard cap on bound parameters per statement. Batched
	// multi-row INSERTs are sized so colCount * batchRows stays below this.
	maxParams int
	// extras is run after base tables+rows are inserted: views, routines,
	// triggers. Each entry is a single statement. Populated per dialect.
	extras []string
	// dropExtras is run before dropping tables, in the order given (already
	// reverse of extras). Used to clear prior objects on reseed.
	dropExtras []string
}

var dialects = map[string]*dialect{
	"mssql":    mssqlDialect,
	"postgres": postgresDialect,
	"mysql":    mysqlDialect,
	"sqlite":   sqliteDialect,
	// libsql is hrana-over-HTTP talking to a SQLite engine, so the SQL
	// dialect is identical to our sqlite adapter. Same for Turso.
	"libsql":   sqliteDialect,
	"oracle":   oracleDialect,
	"firebird": firebirdDialect,
}

func dialectFor(name string) (*dialect, error) {
	d, ok := dialects[name]
	if !ok {
		return nil, fmt.Errorf("seed: no dialect registered for driver %q", name)
	}
	return d, nil
}

var mssqlDialect = &dialect{
	name: "mssql",
	typeSQL: func(c colDef) string {
		switch c.typ {
		case colInt:
			return "INT"
		case colBigInt:
			return "BIGINT"
		case colText:
			return fmt.Sprintf("NVARCHAR(%d)", c.size)
		case colLongText:
			return "NVARCHAR(MAX)"
		case colDecimal:
			return fmt.Sprintf("DECIMAL(%d,%d)", c.size, c.scale)
		case colBool:
			return "BIT"
		case colDate:
			return "DATE"
		case colDateTime:
			return "DATETIME2"
		}
		return "NVARCHAR(255)"
	},
	placeholder: func(i int) string { return fmt.Sprintf("@p%d", i) },
	quoteIdent:  func(s string) string { return "[" + s + "]" },
	dropIfExists: func(name string) string {
		return fmt.Sprintf("IF OBJECT_ID(N'dbo.%s', N'U') IS NOT NULL DROP TABLE [dbo].[%s]", name, name)
	},
	maxParams: 2000, // MSSQL caps at 2100 parameters per batch.
}

var postgresDialect = &dialect{
	name: "postgres",
	typeSQL: func(c colDef) string {
		switch c.typ {
		case colInt:
			return "INTEGER"
		case colBigInt:
			return "BIGINT"
		case colText:
			return fmt.Sprintf("VARCHAR(%d)", c.size)
		case colLongText:
			return "TEXT"
		case colDecimal:
			return fmt.Sprintf("NUMERIC(%d,%d)", c.size, c.scale)
		case colBool:
			return "BOOLEAN"
		case colDate:
			return "DATE"
		case colDateTime:
			return "TIMESTAMP"
		}
		return "TEXT"
	},
	placeholder:  func(i int) string { return fmt.Sprintf("$%d", i) },
	quoteIdent:   func(s string) string { return `"` + s + `"` },
	dropIfExists: func(name string) string { return fmt.Sprintf(`DROP TABLE IF EXISTS "%s"`, name) },
	maxParams:    30000, // PG limit is 65535.
}

var mysqlDialect = &dialect{
	name: "mysql",
	typeSQL: func(c colDef) string {
		switch c.typ {
		case colInt:
			return "INT"
		case colBigInt:
			return "BIGINT"
		case colText:
			return fmt.Sprintf("VARCHAR(%d)", c.size)
		case colLongText:
			return "TEXT"
		case colDecimal:
			return fmt.Sprintf("DECIMAL(%d,%d)", c.size, c.scale)
		case colBool:
			return "TINYINT(1)"
		case colDate:
			return "DATE"
		case colDateTime:
			return "DATETIME"
		}
		return "VARCHAR(255)"
	},
	placeholder:  func(i int) string { return "?" },
	quoteIdent:   func(s string) string { return "`" + s + "`" },
	dropIfExists: func(name string) string { return fmt.Sprintf("DROP TABLE IF EXISTS `%s`", name) },
	maxParams:    60000, // prepared statement param cap is ~65535.
}

var sqliteDialect = &dialect{
	name: "sqlite",
	typeSQL: func(c colDef) string {
		switch c.typ {
		case colInt, colBigInt, colBool:
			return "INTEGER"
		case colText, colLongText, colDate, colDateTime:
			return "TEXT"
		case colDecimal:
			return "NUMERIC"
		}
		return "TEXT"
	},
	placeholder:  func(i int) string { return "?" },
	quoteIdent:   func(s string) string { return `"` + s + `"` },
	dropIfExists: func(name string) string { return fmt.Sprintf(`DROP TABLE IF EXISTS "%s"`, name) },
	maxParams:    900, // SQLite default SQLITE_MAX_VARIABLE_NUMBER is 999.
}

// Oracle has no DROP TABLE IF EXISTS; wrap each drop in an anonymous
// PL/SQL block that swallows ORA-00942 (table or view does not exist)
// so re-runs are idempotent. Placeholders are :1, :2, ...
var oracleDialect = &dialect{
	name: "oracle",
	typeSQL: func(c colDef) string {
		switch c.typ {
		case colInt:
			return "NUMBER(10)"
		case colBigInt:
			return "NUMBER(19)"
		case colText:
			return fmt.Sprintf("VARCHAR2(%d)", c.size)
		case colLongText:
			return "CLOB"
		case colDecimal:
			return fmt.Sprintf("NUMBER(%d,%d)", c.size, c.scale)
		case colBool:
			return "NUMBER(1)"
		case colDate:
			return "DATE"
		case colDateTime:
			return "TIMESTAMP"
		}
		return "VARCHAR2(255)"
	},
	placeholder: func(i int) string { return fmt.Sprintf(":%d", i) },
	quoteIdent:  func(s string) string { return `"` + strings.ToUpper(s) + `"` },
	dropIfExists: func(name string) string {
		return fmt.Sprintf(
			`BEGIN EXECUTE IMMEDIATE 'DROP TABLE "%s"'; EXCEPTION WHEN OTHERS THEN IF SQLCODE != -942 THEN RAISE; END IF; END;`,
			strings.ToUpper(name))
	},
	// Oracle caps bind variables per statement at 65535, but a single
	// batched INSERT is an INSERT ALL construct; keep batches modest.
	maxParams: 1000,
}

// Firebird lacks DROP TABLE IF EXISTS pre-v4; use EXECUTE BLOCK guarded
// by a lookup in RDB$RELATIONS so old server versions still work. All
// identifiers are upper-cased in the system catalog, so we quote and
// upper-case to keep DDL and DML consistent.
var firebirdDialect = &dialect{
	name: "firebird",
	typeSQL: func(c colDef) string {
		switch c.typ {
		case colInt:
			return "INTEGER"
		case colBigInt:
			return "BIGINT"
		case colText:
			return fmt.Sprintf("VARCHAR(%d)", c.size)
		case colLongText:
			return "BLOB SUB_TYPE TEXT"
		case colDecimal:
			return fmt.Sprintf("DECIMAL(%d,%d)", c.size, c.scale)
		case colBool:
			return "BOOLEAN"
		case colDate:
			return "DATE"
		case colDateTime:
			return "TIMESTAMP"
		}
		return "VARCHAR(255)"
	},
	placeholder: func(i int) string { return "?" },
	quoteIdent:  func(s string) string { return `"` + strings.ToUpper(s) + `"` },
	dropIfExists: func(name string) string {
		upper := strings.ToUpper(name)
		return fmt.Sprintf(
			`EXECUTE BLOCK AS BEGIN IF (EXISTS(SELECT 1 FROM RDB$RELATIONS WHERE RDB$RELATION_NAME = '%s')) THEN EXECUTE STATEMENT 'DROP TABLE "%s"'; END`,
			upper, upper)
	},
	// Firebird XSQLDA limits the message buffer; 1491 columns max per
	// prepared statement. Keep batches small — large multi-row VALUES
	// also hit parser-stack limits.
	maxParams: 1000,
}

// extras DDL: a view, a scalar function, a stored procedure, and a trigger.
// Kept intentionally minimal — their purpose is to surface each object kind
// in the TUI explorer, not to exercise database features.
func init() {
	mssqlDialect.extras = []string{
		`CREATE VIEW dbo.active_employees AS
SELECT id, first_name, last_name, department_id FROM dbo.employees`,
		`CREATE FUNCTION dbo.employee_full_name(@id INT) RETURNS NVARCHAR(90) AS
BEGIN
    DECLARE @n NVARCHAR(90);
    SELECT @n = first_name + N' ' + last_name FROM dbo.employees WHERE id = @id;
    RETURN @n;
END`,
		`CREATE PROCEDURE dbo.list_departments AS
BEGIN
    SET NOCOUNT ON;
    SELECT id, name, location FROM dbo.departments ORDER BY id;
END`,
		`CREATE TRIGGER dbo.trg_test_notes_touch ON dbo.test_notes AFTER INSERT AS
BEGIN SET NOCOUNT ON; END`,
	}
	mssqlDialect.dropExtras = []string{
		`IF OBJECT_ID(N'dbo.trg_test_notes_touch', N'TR') IS NOT NULL DROP TRIGGER dbo.trg_test_notes_touch`,
		`IF OBJECT_ID(N'dbo.list_departments', N'P') IS NOT NULL DROP PROCEDURE dbo.list_departments`,
		`IF OBJECT_ID(N'dbo.employee_full_name', N'FN') IS NOT NULL DROP FUNCTION dbo.employee_full_name`,
		`IF OBJECT_ID(N'dbo.active_employees', N'V') IS NOT NULL DROP VIEW dbo.active_employees`,
	}

	postgresDialect.extras = []string{
		`CREATE VIEW active_employees AS
SELECT id, first_name, last_name, department_id FROM employees`,
		`CREATE FUNCTION employee_full_name(eid INT) RETURNS TEXT AS $$
    SELECT first_name || ' ' || last_name FROM employees WHERE id = eid
$$ LANGUAGE SQL`,
		`CREATE PROCEDURE list_departments() LANGUAGE plpgsql AS $$
BEGIN PERFORM 1; END
$$`,
		`CREATE FUNCTION trg_test_notes_touch_fn() RETURNS TRIGGER AS $$
BEGIN RETURN NEW; END
$$ LANGUAGE plpgsql`,
		`CREATE TRIGGER trg_test_notes_touch AFTER INSERT ON test_notes
FOR EACH ROW EXECUTE FUNCTION trg_test_notes_touch_fn()`,
	}
	postgresDialect.dropExtras = []string{
		`DROP TRIGGER IF EXISTS trg_test_notes_touch ON test_notes`,
		`DROP FUNCTION IF EXISTS trg_test_notes_touch_fn()`,
		`DROP PROCEDURE IF EXISTS list_departments()`,
		`DROP FUNCTION IF EXISTS employee_full_name(INT)`,
		`DROP VIEW IF EXISTS active_employees`,
	}

	mysqlDialect.extras = []string{
		"CREATE VIEW `active_employees` AS\nSELECT id, first_name, last_name, department_id FROM `employees`",
		"CREATE FUNCTION `employee_full_name`(eid INT) RETURNS VARCHAR(90) DETERMINISTIC READS SQL DATA\nRETURN (SELECT CONCAT(first_name, ' ', last_name) FROM `employees` WHERE id = eid)",
		"CREATE PROCEDURE `list_departments`()\nSELECT id, name, location FROM `departments` ORDER BY id",
		"CREATE TRIGGER `trg_test_notes_touch` AFTER INSERT ON `test_notes`\nFOR EACH ROW SET @last_note_id = NEW.id",
	}
	mysqlDialect.dropExtras = []string{
		"DROP TRIGGER IF EXISTS `trg_test_notes_touch`",
		"DROP PROCEDURE IF EXISTS `list_departments`",
		"DROP FUNCTION IF EXISTS `employee_full_name`",
		"DROP VIEW IF EXISTS `active_employees`",
	}

	sqliteDialect.extras = []string{
		`CREATE VIEW "active_employees" AS
SELECT id, first_name, last_name, department_id FROM "employees"`,
		`CREATE TRIGGER "trg_test_notes_touch" AFTER INSERT ON "test_notes"
BEGIN SELECT 1; END`,
	}
	sqliteDialect.dropExtras = []string{
		`DROP TRIGGER IF EXISTS "trg_test_notes_touch"`,
		`DROP VIEW IF EXISTS "active_employees"`,
	}
}

// createDDL renders CREATE TABLE for a single table using d's type map.
func (d *dialect) createDDL(t tableDef) string {
	var b strings.Builder
	fmt.Fprintf(&b, "CREATE TABLE %s (\n", d.quoteIdent(t.name))
	for i, c := range t.cols {
		if i > 0 {
			b.WriteString(",\n")
		}
		fmt.Fprintf(&b, "  %s %s", d.quoteIdent(c.name), d.typeSQL(c))
		if c.pk {
			b.WriteString(" PRIMARY KEY")
		}
		if !c.nullable && !c.pk {
			b.WriteString(" NOT NULL")
		}
	}
	b.WriteString("\n)")
	return b.String()
}

// insertRows writes rows in batched multi-row INSERTs. Each batch is sized
// so col*rows stays under the dialect's parameter cap.
func insertRows(ctx context.Context, conn db.Conn, d *dialect, t tableDef, rows [][]any) error {
	if len(rows) == 0 {
		return nil
	}
	ncols := len(t.cols)
	batch := d.maxParams / ncols
	if batch < 1 {
		batch = 1
	}
	if batch > 500 {
		batch = 500
	}

	for start := 0; start < len(rows); start += batch {
		end := start + batch
		if end > len(rows) {
			end = len(rows)
		}
		sql, args := buildInsert(d, t, rows[start:end])
		if err := conn.Exec(ctx, sql, args...); err != nil {
			return fmt.Errorf("insert %s [%d..%d]: %w", t.name, start, end, err)
		}
	}
	return nil
}

func buildInsert(d *dialect, t tableDef, rows [][]any) (string, []any) {
	var b strings.Builder
	fmt.Fprintf(&b, "INSERT INTO %s (", d.quoteIdent(t.name))
	for i, c := range t.cols {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(d.quoteIdent(c.name))
	}
	b.WriteString(") VALUES ")

	ncols := len(t.cols)
	args := make([]any, 0, ncols*len(rows))
	paramIdx := 1
	for rIdx, row := range rows {
		if rIdx > 0 {
			b.WriteString(", ")
		}
		b.WriteByte('(')
		for cIdx := 0; cIdx < ncols; cIdx++ {
			if cIdx > 0 {
				b.WriteString(", ")
			}
			b.WriteString(d.placeholder(paramIdx))
			paramIdx++
		}
		b.WriteByte(')')
		args = append(args, row...)
	}
	return b.String(), args
}

// --- data generation --------------------------------------------------------

type dataset struct {
	departments []department
	suppliers   []supplier
	employees   []employee
	products    []product
	customers   []customer
	users       []user
	orders      []purchaseOrder
	orderItems  []purchaseOrderItem
	testNotes   []testNote
}

type department struct {
	id       int
	name     string
	location string
}

type supplier struct {
	id      int
	name    string
	email   string
	country string
}

type employee struct {
	id         int
	first      string
	last       string
	email      string
	hireDate   time.Time
	deptID     int
	managerID  *int
	salaryHund int64 // salary * 100 to keep decimals exact
}

type product struct {
	id          int
	sku         string
	name        string
	description string
	priceCents  int64
	supplierID  int
	category    string
	stockQty    int
}

type customer struct {
	id        int
	name      string
	email     string
	phone     string
	city      string
	country   string
	createdAt time.Time
}

type user struct {
	id         int
	username   string
	email      string
	employeeID *int
	role       string
	isActive   bool
	createdAt  time.Time
}

type purchaseOrder struct {
	id          int
	customerID  int
	employeeID  int
	orderDate   time.Time
	status      string
	totalCents  int64
}

type purchaseOrderItem struct {
	id         int64
	orderID    int
	productID  int
	quantity   int
	priceCents int64
	lineCents  int64
}

// testNote is a curated edge-case text row. The table is there so the
// TUI team can stress-test rendering without having to hand-craft a
// query to produce quirky values: just `SELECT * FROM test_notes`.
type testNote struct {
	id       int
	label    string
	category string
	content  string
	note     string
}

// generate produces the full in-memory dataset. Row counts scale linearly
// with opts.Scale. Epoch is the reference "today" used for hire/order dates;
// fixed so runs are reproducible.
func generate(r *rand.Rand, scale int) *dataset {
	epoch := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

	ds := &dataset{}

	// departments — always 10, location from cities
	for i, name := range departmentNames {
		ds.departments = append(ds.departments, department{
			id:       i + 1,
			name:     name,
			location: pick(r, cities),
		})
	}

	// suppliers — 50 fixed
	const nSuppliers = 50
	for i := 0; i < nSuppliers; i++ {
		name := pick(r, supplierWords) + " " + pick(r, supplierSuffixes)
		s := supplier{
			id:      i + 1,
			name:    name,
			email:   "sales@" + slugify(name) + ".example",
			country: pick(r, countries),
		}
		ds.suppliers = append(ds.suppliers, s)
	}

	// employees — 500 * scale. manager_id references an earlier row so
	// self-referential FKs (if ever added) will hold.
	nEmployees := 500 * scale
	for i := 0; i < nEmployees; i++ {
		first := pick(r, firstNames)
		last := pick(r, lastNames)
		id := i + 1
		var mgr *int
		if i >= 5 { // first handful are managers with no boss
			m := 1 + r.IntN(i)
			mgr = &m
		}
		ds.employees = append(ds.employees, employee{
			id:         id,
			first:      first,
			last:       last,
			email:      strings.ToLower(first+"."+last) + fmt.Sprintf(".%d@acmewidgets.example", id),
			hireDate:   epoch.AddDate(0, 0, -r.IntN(365*8)),
			deptID:     1 + r.IntN(len(ds.departments)),
			managerID:  mgr,
			salaryHund: 4000000 + int64(r.IntN(16000000)), // $40k..$200k
		})
	}

	// products — 300 fixed (catalog size doesn't need to scale)
	const nProducts = 300
	for i := 0; i < nProducts; i++ {
		adj := pick(r, productAdjectives)
		noun := pick(r, productNouns)
		cat := pick(r, productCategories)
		name := adj + " " + noun
		sku := fmt.Sprintf("%s-%04d", strings.ToUpper(adj[:3]), i+1)
		ds.products = append(ds.products, product{
			id:          i + 1,
			sku:         sku,
			name:        name,
			description: fmt.Sprintf("%s %s for %s applications. Precision-engineered, field-tested.", adj, strings.ToLower(noun), strings.ToLower(cat)),
			priceCents:  199 + int64(r.IntN(99999)),
			supplierID:  1 + r.IntN(len(ds.suppliers)),
			category:    cat,
			stockQty:    r.IntN(10000),
		})
	}

	// customers — 1000 * scale
	nCustomers := 1000 * scale
	for i := 0; i < nCustomers; i++ {
		name := pick(r, supplierWords) + " " + pick(r, supplierSuffixes)
		ds.customers = append(ds.customers, customer{
			id:        i + 1,
			name:      name,
			email:     "ap@" + slugify(name) + ".example",
			phone:     fmt.Sprintf("+1-555-%04d", r.IntN(10000)),
			city:      pick(r, cities),
			country:   pick(r, countries),
			createdAt: epoch.AddDate(0, 0, -r.IntN(365*5)).Add(time.Duration(r.IntN(86400)) * time.Second),
		})
	}

	// users — every 3rd employee + a handful of non-employee roles
	for i, e := range ds.employees {
		if i%3 != 0 {
			continue
		}
		empID := e.id
		ds.users = append(ds.users, user{
			id:         len(ds.users) + 1,
			username:   strings.ToLower(e.first+e.last) + fmt.Sprintf("%d", e.id),
			email:      e.email,
			employeeID: &empID,
			role:       pick(r, userRoles),
			isActive:   r.IntN(100) < 92, // ~92% active
			createdAt:  e.hireDate.Add(time.Duration(r.IntN(30)) * 24 * time.Hour),
		})
	}
	for i := 0; i < 20; i++ {
		ds.users = append(ds.users, user{
			id:        len(ds.users) + 1,
			username:  fmt.Sprintf("svc_%s_%d", strings.ToLower(pick(r, supplierWords)), i),
			email:     fmt.Sprintf("svc%d@acmewidgets.example", i),
			role:      "service",
			isActive:  true,
			createdAt: epoch.AddDate(0, 0, -r.IntN(365*3)),
		})
	}

	// purchase orders — 2000 * scale. Each order gets 1..6 line items.
	nOrders := 2000 * scale
	var itemID int64
	for i := 0; i < nOrders; i++ {
		cust := ds.customers[r.IntN(len(ds.customers))]
		sales := ds.employees[r.IntN(len(ds.employees))]
		orderDate := epoch.AddDate(0, 0, -r.IntN(365*3)).Add(time.Duration(r.IntN(86400)) * time.Second)
		order := purchaseOrder{
			id:         i + 1,
			customerID: cust.id,
			employeeID: sales.id,
			orderDate:  orderDate,
			status:     pick(r, orderStatuses),
		}
		nItems := 1 + r.IntN(6)
		var total int64
		for j := 0; j < nItems; j++ {
			p := ds.products[r.IntN(len(ds.products))]
			qty := 1 + r.IntN(20)
			line := p.priceCents * int64(qty)
			total += line
			itemID++
			ds.orderItems = append(ds.orderItems, purchaseOrderItem{
				id:         itemID,
				orderID:    order.id,
				productID:  p.id,
				quantity:   qty,
				priceCents: p.priceCents,
				lineCents:  line,
			})
		}
		order.totalCents = total
		ds.orders = append(ds.orders, order)
	}

	ds.testNotes = buildTestNotes()

	return ds
}

// buildTestNotes returns the curated edge-case row set for test_notes.
// Each row exercises a specific rendering concern in the TUI:
//
//   - escape: preserves \n \r \t so the draw path can dim them
//   - unicode: CJK / RTL / emoji / combining marks / zero-width
//   - whitespace: leading/trailing/all-blank cells
//   - long: wrap-mode stress + cell inspector scroll
//   - trap: quotes / pipes / commas / backslashes that break naive
//     export escaping
//   - markup: html/xml/json/url shapes that occur in real payloads
//
// The slice is intentionally hand-written (not RNG-driven) so the
// same ids stay on the same rows across seeded runs. Anyone eyeballing
// the TUI can reproduce a specific case by `WHERE id = 13`.
func buildTestNotes() []testNote {
	// loremLine is a single sentence repeated to build long runs
	// without committing a 2 KB string literal to source.
	loremLine := "Lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor incididunt. "

	longSingleLine := strings.Repeat(loremLine, 40) // ~3600 chars, one row
	longMultiLine := func() string {
		var b strings.Builder
		for i := 1; i <= 40; i++ {
			fmt.Fprintf(&b, "line %02d of forty -- %s\n", i, loremLine)
		}
		return strings.TrimRight(b.String(), "\n")
	}()
	veryLongWord := strings.Repeat("supercalifragilistic", 10) // 200 unbroken chars

	notes := []testNote{
		// --- baseline ASCII ----------------------------------------
		{label: "simple ascii", category: "text",
			content: "hello world",
			note:    "sanity check; nothing interesting"},
		{label: "empty string", category: "text",
			content: "",
			note:    "should render as blank cell, not NULL"},
		{label: "single char", category: "text",
			content: "a",
			note:    "column width floor"},

		// --- whitespace edge cases ---------------------------------
		{label: "leading spaces", category: "whitespace",
			content: "    four leading spaces",
			note:    "check that leading space isn't trimmed in display"},
		{label: "trailing spaces", category: "whitespace",
			content: "trailing four spaces    ",
			note:    "check that trailing space survives through stringify"},
		{label: "only whitespace", category: "whitespace",
			content: "        ",
			note:    "all spaces; should still count as non-empty"},
		{label: "tab indented", category: "whitespace",
			content: "\tindented with a tab",
			note:    "leading tab becomes dim \\t in the table view"},

		// --- escape chars ------------------------------------------
		{label: "single newline", category: "escape",
			content: "before the newline\nafter the newline",
			note:    "\\n should render dim between segments"},
		{label: "many newlines", category: "escape",
			content: "one\ntwo\nthree\nfour\nfive\nsix\nseven",
			note:    "stresses wrap mode vs in-line escape display"},
		{label: "crlf windows", category: "escape",
			content: "windows\r\nstyle\r\nline\r\nendings",
			note:    "both \\r and \\n should dim"},
		{label: "tab separated", category: "escape",
			content: "col1\tcol2\tcol3\tcol4",
			note:    "looks like TSV; every tab dims"},
		{label: "mixed escapes", category: "escape",
			content: "tab\there\nnewline\rcarriage return",
			note:    "all three escape kinds in one cell"},
		{label: "carriage return only", category: "escape",
			content: "before\rafter",
			note:    "bare \\r is historically display-destructive"},

		// --- unicode -----------------------------------------------
		{label: "cjk", category: "unicode",
			content: "你好世界 こんにちは 안녕하세요",
			note:    "wide glyphs; check column width math"},
		{label: "rtl hebrew arabic", category: "unicode",
			content: "مرحبا بالعالم עברית",
			note:    "RTL script; bidi handling is probably naive"},
		{label: "emoji mix", category: "unicode",
			content: "Hello 👋 World 🌍 with emoji 🎉🎊🎁",
			note:    "surrogate pairs / variation selectors"},
		{label: "accented latin", category: "unicode",
			content: "café, naïve, résumé, piñata, Zürich, São Paulo",
			note:    "precomposed accents"},
		{label: "combining marks", category: "unicode",
			content: "cafe\u0301 vs café (combining vs precomposed)",
			note:    "same glyph, different byte length"},
		{label: "zero width", category: "unicode",
			content: "a\u200bb\u200cc\u200dd (zero-width space/joiner)",
			note:    "invisible cells break column-width heuristics"},
		{label: "box drawing", category: "unicode",
			content: "┌─┬─┐ │a│b│ ├─┼─┤ │c│d│ └─┴─┘",
			note:    "terminal graphics in data, not in our own UI"},

		// --- trap characters for CSV/JSON/markdown exporters -------
		{label: "single quotes", category: "trap",
			content: "O'Brien said 'hello' and left",
			note:    "sql-ish quote chaos"},
		{label: "double quotes", category: "trap",
			content: `she said "hello" with "emphasis"`,
			note:    "csv export must escape"},
		{label: "embedded comma", category: "trap",
			content: "first, second, third, fourth",
			note:    "csv export must quote the cell"},
		{label: "pipes", category: "trap",
			content: "col1 | col2 | col3 | pipe table trap",
			note:    "markdown export must backslash-escape"},
		{label: "backslash path", category: "trap",
			content: `C:\Users\Admin\AppData\Roaming\sqlgo`,
			note:    "raw backslashes; no escape interpretation"},
		{label: "quoted comma newline", category: "trap",
			content: "row with \"quotes\", commas, and\na newline",
			note:    "every csv trap in one cell"},

		// --- markup / structured payloads --------------------------
		{label: "json inline", category: "markup",
			content: `{"user":"alice","tags":["x","y"],"meta":{"n":42}}`,
			note:    "json in a cell; syntax highlighting? no. readable? check"},
		{label: "xml inline", category: "markup",
			content: `<root attr="val"><child>text with &amp; entity</child></root>`,
			note:    "xml entities; angle brackets"},
		{label: "html tags", category: "markup",
			content: `<script>alert('xss')</script> <b>bold</b>`,
			note:    "never html-escape on display"},
		{label: "url with query", category: "markup",
			content: "https://example.com/path/to/thing?q=hello+world&lang=en&other=%20value#fragment",
			note:    "long horizontal; scrolls past panel edge"},

		// --- long content ------------------------------------------
		{label: "long single line", category: "long",
			content: longSingleLine,
			note:    fmt.Sprintf("%d chars on one logical line; exercises h-scroll + wrap", len(longSingleLine))},
		{label: "long multi line", category: "long",
			content: longMultiLine,
			note:    "40 lines; cell inspector should scroll, main view clips"},
		{label: "very long word", category: "long",
			content: veryLongWord,
			note:    "no spaces; wrap mode must hard-chop"},
		{label: "paragraph", category: "long",
			content: strings.Repeat(loremLine, 3),
			note:    "mid-length; fits cell inspector without scroll"},
	}

	for i := range notes {
		notes[i].id = i + 1
	}
	return notes
}

// --- row materialization ----------------------------------------------------

func (d *dataset) departmentRows() [][]any {
	out := make([][]any, len(d.departments))
	for i, x := range d.departments {
		out[i] = []any{x.id, x.name, x.location}
	}
	return out
}

func (d *dataset) supplierRows() [][]any {
	out := make([][]any, len(d.suppliers))
	for i, x := range d.suppliers {
		out[i] = []any{x.id, x.name, x.email, x.country}
	}
	return out
}

func (d *dataset) employeeRows() [][]any {
	out := make([][]any, len(d.employees))
	for i, x := range d.employees {
		var mgr any
		if x.managerID != nil {
			mgr = *x.managerID
		}
		out[i] = []any{
			x.id, x.first, x.last, x.email, x.hireDate,
			x.deptID, mgr, money(x.salaryHund),
		}
	}
	return out
}

func (d *dataset) productRows() [][]any {
	out := make([][]any, len(d.products))
	for i, x := range d.products {
		out[i] = []any{
			x.id, x.sku, x.name, x.description,
			money(x.priceCents), x.supplierID, x.category, x.stockQty,
		}
	}
	return out
}

func (d *dataset) customerRows() [][]any {
	out := make([][]any, len(d.customers))
	for i, x := range d.customers {
		out[i] = []any{x.id, x.name, x.email, x.phone, x.city, x.country, x.createdAt}
	}
	return out
}

func (d *dataset) userRows() [][]any {
	out := make([][]any, len(d.users))
	for i, x := range d.users {
		var emp any
		if x.employeeID != nil {
			emp = *x.employeeID
		}
		out[i] = []any{x.id, x.username, x.email, emp, x.role, x.isActive, x.createdAt}
	}
	return out
}

func (d *dataset) orderRows() [][]any {
	out := make([][]any, len(d.orders))
	for i, x := range d.orders {
		out[i] = []any{x.id, x.customerID, x.employeeID, x.orderDate, x.status, money(x.totalCents)}
	}
	return out
}

func (d *dataset) orderItemRows() [][]any {
	out := make([][]any, len(d.orderItems))
	for i, x := range d.orderItems {
		out[i] = []any{x.id, x.orderID, x.productID, x.quantity, money(x.priceCents), money(x.lineCents)}
	}
	return out
}

func (d *dataset) testNoteRows() [][]any {
	out := make([][]any, len(d.testNotes))
	for i, x := range d.testNotes {
		out[i] = []any{x.id, x.label, x.category, x.content, x.note}
	}
	return out
}

// --- helpers ----------------------------------------------------------------

func pick[T any](r *rand.Rand, xs []T) T { return xs[r.IntN(len(xs))] }

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// money formats a cents amount as a decimal string. Passing a string keeps
// precision exact across drivers — DECIMAL columns accept it verbatim.
func money(cents int64) string {
	neg := ""
	if cents < 0 {
		neg = "-"
		cents = -cents
	}
	return fmt.Sprintf("%s%d.%02d", neg, cents/100, cents%100)
}

// slugify produces a lowercase, punctuation-free version of s suitable for
// building fake email domains. Non-alphanumeric runs collapse to a single
// hyphen, leading/trailing hyphens are trimmed.
func slugify(s string) string {
	var b strings.Builder
	lastHyphen := true
	for _, c := range strings.ToLower(s) {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			b.WriteRune(c)
			lastHyphen = false
		default:
			if !lastHyphen {
				b.WriteByte('-')
				lastHyphen = true
			}
		}
	}
	out := b.String()
	return strings.Trim(out, "-")
}
