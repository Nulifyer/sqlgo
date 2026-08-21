package db

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestCrossDatabaseOperationsRestoreOriginalDatabase(t *testing.T) {
	tests := []struct {
		name       string
		opts       sqlOptions
		expectWork func(sqlmock.Sqlmock)
		run        func(context.Context, *sqlConn) error
	}{
		{
			name: "columns",
			opts: sqlOptions{ColumnsQuery: "columns query"},
			expectWork: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta("columns query")).
					WithArgs("dbo", "widgets").
					WillReturnRows(sqlmock.NewRows([]string{"name", "type"}).AddRow("id", "int"))
			},
			run: func(ctx context.Context, conn *sqlConn) error {
				_, err := conn.ColumnsIn(ctx, "analytics", TableRef{Schema: "dbo", Name: "widgets"})
				return err
			},
		},
		{
			name: "table design",
			opts: sqlOptions{ColumnsQuery: "columns query"},
			expectWork: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta("columns query")).
					WithArgs("dbo", "widgets").
					WillReturnRows(sqlmock.NewRows([]string{"name", "type"}).AddRow("id", "int"))
			},
			run: func(ctx context.Context, conn *sqlConn) error {
				_, err := conn.TableDesignIn(ctx, "analytics", TableRef{Schema: "dbo", Name: "widgets"})
				return err
			},
		},
		{
			name: "explain",
			opts: sqlOptions{Capabilities: Capabilities{ExplainFormat: ExplainFormatSQLiteRows}},
			expectWork: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta("EXPLAIN QUERY PLAN SELECT 1")).
					WillReturnRows(sqlmock.NewRows([]string{"plan"}).AddRow("scan widgets"))
			},
			run: func(ctx context.Context, conn *sqlConn) error {
				_, err := conn.ExplainIn(ctx, "analytics", "SELECT 1")
				return err
			},
		},
		{
			name: "schema",
			opts: sqlOptions{SchemaQuery: "schema query"},
			expectWork: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta("schema query")).
					WillReturnRows(sqlmock.NewRows([]string{"schema", "name", "is_view", "is_system"}).
						AddRow("dbo", "widgets", 0, 0))
			},
			run: func(ctx context.Context, conn *sqlConn) error {
				_, err := conn.SchemaForDatabase(ctx, "analytics")
				return err
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sqlDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatalf("sqlmock.New: %v", err)
			}
			t.Cleanup(func() { _ = sqlDB.Close() })

			tc.opts.CurrentDatabaseQuery = "current database query"
			tc.opts.UseDatabaseStmt = bracketedUse
			conn := &sqlConn{db: sqlDB, opts: tc.opts}

			mock.ExpectQuery(regexp.QuoteMeta("current database query")).
				WillReturnRows(sqlmock.NewRows([]string{"database"}).AddRow("source"))
			mock.ExpectExec(regexp.QuoteMeta("USE [analytics]")).WillReturnResult(sqlmock.NewResult(0, 0))
			tc.expectWork(mock)
			mock.ExpectExec(regexp.QuoteMeta("USE [source]")).WillReturnResult(sqlmock.NewResult(0, 0))

			if err := tc.run(context.Background(), conn); err != nil {
				t.Fatalf("operation: %v", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCrossDatabaseOperationRestoresAfterWorkError(t *testing.T) {
	workErr := errors.New("metadata failed")
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer sqlDB.Close()

	conn := &sqlConn{db: sqlDB, opts: sqlOptions{
		ColumnsQuery:         "columns query",
		CurrentDatabaseQuery: "current database query",
		UseDatabaseStmt:      bracketedUse,
	}}
	mock.ExpectQuery(regexp.QuoteMeta("current database query")).
		WillReturnRows(sqlmock.NewRows([]string{"database"}).AddRow("source"))
	mock.ExpectExec(regexp.QuoteMeta("USE [analytics]")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta("columns query")).
		WithArgs("dbo", "widgets").
		WillReturnError(workErr)
	mock.ExpectExec(regexp.QuoteMeta("USE [source]")).WillReturnResult(sqlmock.NewResult(0, 0))

	_, err = conn.ColumnsIn(context.Background(), "analytics", TableRef{Schema: "dbo", Name: "widgets"})
	if !errors.Is(err, workErr) {
		t.Fatalf("ColumnsIn error = %v, want %v", err, workErr)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCrossDatabaseRestoreFailureDiscardsPinnedConnection(t *testing.T) {
	restoreErr := errors.New("restore failed")
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}

	conn := &sqlConn{db: sqlDB, opts: sqlOptions{
		ColumnsQuery:         "columns query",
		CurrentDatabaseQuery: "current database query",
		UseDatabaseStmt:      bracketedUse,
	}}
	mock.ExpectQuery(regexp.QuoteMeta("current database query")).
		WillReturnRows(sqlmock.NewRows([]string{"database"}).AddRow("source"))
	mock.ExpectExec(regexp.QuoteMeta("USE [analytics]")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta("columns query")).
		WithArgs("dbo", "widgets").
		WillReturnRows(sqlmock.NewRows([]string{"name", "type"}).AddRow("id", "int"))
	mock.ExpectExec(regexp.QuoteMeta("USE [source]")).WillReturnError(restoreErr)
	mock.ExpectClose()

	cols, err := conn.ColumnsIn(context.Background(), "analytics", TableRef{Schema: "dbo", Name: "widgets"})
	if !errors.Is(err, restoreErr) {
		t.Fatalf("ColumnsIn error = %v, want restore failure", err)
	}
	if cols != nil {
		t.Fatalf("ColumnsIn columns = %#v, want nil after restore failure", cols)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	_ = sqlDB.Close()
}

func TestCrossDatabaseOperationDiscardsConnectionWhenNoDatabaseWasSelected(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}

	conn := &sqlConn{db: sqlDB, opts: sqlOptions{
		ColumnsQuery:         "columns query",
		CurrentDatabaseQuery: "current database query",
		UseDatabaseStmt:      bracketedUse,
	}}
	mock.ExpectQuery(regexp.QuoteMeta("current database query")).
		WillReturnRows(sqlmock.NewRows([]string{"database"}).AddRow(nil))
	mock.ExpectExec(regexp.QuoteMeta("USE [analytics]")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta("columns query")).
		WithArgs("dbo", "widgets").
		WillReturnRows(sqlmock.NewRows([]string{"name", "type"}).AddRow("id", "int"))
	mock.ExpectClose()

	cols, err := conn.ColumnsIn(context.Background(), "analytics", TableRef{Schema: "dbo", Name: "widgets"})
	if err != nil {
		t.Fatalf("ColumnsIn: %v", err)
	}
	if len(cols) != 1 || cols[0].Name != "id" {
		t.Fatalf("ColumnsIn columns = %#v", cols)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	_ = sqlDB.Close()
}

func bracketedUse(database string) string {
	return "USE [" + database + "]"
}
