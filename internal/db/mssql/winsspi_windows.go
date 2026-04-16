package mssql

// Blank import registers the "winsspi" integrated-auth provider on
// Windows, where go-mssqldb calls into secur32.dll for SSPI. On other
// platforms the import is absent and authenticator=winsspi returns
// an "unknown authenticator" error at connect time. The _windows.go
// filename suffix is the GOOS auto build-constraint.
import _ "github.com/microsoft/go-mssqldb/integratedauth/winsspi"
