package db

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestOpenWithRejectsUnsupportedTransportBeforePreparationOrOpen(t *testing.T) {
	prepared := false
	opened := false
	p := Profile{
		Name:                "restricted",
		SupportedTransports: []string{"supported"},
		PrepareConfig: func(context.Context, Config) (Config, error) {
			prepared = true
			return Config{}, nil
		},
	}
	tpt := Transport{
		Name: "unsupported",
		Open: func(context.Context, Config) (*sql.DB, func() error, error) {
			opened = true
			return nil, nil, nil
		},
	}

	_, err := OpenWith(context.Background(), p, tpt, Config{})
	if err == nil || !strings.Contains(err.Error(), `profile "restricted" does not support transport "unsupported"`) {
		t.Fatalf("OpenWith error = %v", err)
	}
	if prepared || opened {
		t.Fatalf("PrepareConfig called = %v, Transport.Open called = %v", prepared, opened)
	}
}

func TestOpenWithPreparesConfigExactlyOnce(t *testing.T) {
	sqlDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	mock.ExpectPing()
	mock.ExpectClose()

	want := Config{User: "prepared", Options: map[string]string{"auth": "ready"}}
	prepareCalls := 0
	openCalls := 0
	p := Profile{
		Name:                "prepared-profile",
		SupportedTransports: []string{"capture"},
		PrepareConfig: func(context.Context, Config) (Config, error) {
			prepareCalls++
			return want, nil
		},
	}
	tpt := Transport{
		Name: "capture",
		Open: func(_ context.Context, got Config) (*sql.DB, func() error, error) {
			openCalls++
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Transport.Open config = %#v, want %#v", got, want)
			}
			return sqlDB, nil, nil
		},
	}

	conn, err := OpenWith(context.Background(), p, tpt, Config{User: "original"})
	if err != nil {
		t.Fatalf("OpenWith: %v", err)
	}
	if prepareCalls != 1 || openCalls != 1 {
		t.Fatalf("PrepareConfig calls = %d, Transport.Open calls = %d", prepareCalls, openCalls)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenWithPreparationFailureDoesNotOpenTransport(t *testing.T) {
	wantErr := errors.New("credentials unavailable")
	opened := false
	p := Profile{
		Name: "auth-profile",
		PrepareConfig: func(context.Context, Config) (Config, error) {
			return Config{}, wantErr
		},
	}
	tpt := Transport{
		Name: "ad-hoc",
		Open: func(context.Context, Config) (*sql.DB, func() error, error) {
			opened = true
			return nil, nil, nil
		},
	}

	_, err := OpenWith(context.Background(), p, tpt, Config{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("OpenWith error = %v, want wrapped %v", err, wantErr)
	}
	if opened {
		t.Fatal("Transport.Open called after preparation failure")
	}
}

func TestNilSupportedTransportsAllowsAdHocComposition(t *testing.T) {
	p := Profile{Name: "ad-hoc-profile"}
	if err := ValidateProfileTransport(p, Transport{Name: "ad-hoc-transport"}); err != nil {
		t.Fatalf("ValidateProfileTransport: %v", err)
	}
}

func TestRegisteredTransportsForProfileFiltersCompatibility(t *testing.T) {
	const (
		profileName      = "profile-test-compatible"
		compatibleName   = "profile-test-transport-compatible"
		incompatibleName = "profile-test-transport-incompatible"
	)
	RegisterTransport(Transport{Name: compatibleName})
	RegisterTransport(Transport{Name: incompatibleName})
	RegisterProfile(Profile{Name: profileName, SupportedTransports: []string{compatibleName}})

	got, err := RegisteredTransportsForProfile(profileName)
	if err != nil {
		t.Fatalf("RegisteredTransportsForProfile: %v", err)
	}
	if !reflect.DeepEqual(got, []string{compatibleName}) {
		t.Fatalf("registered transports = %v, want [%s]", got, compatibleName)
	}
}
