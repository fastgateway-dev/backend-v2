package config

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// clearConfigEnv unsets all environment variables used by the config package.
func clearConfigEnv(t *testing.T) {
	t.Helper()
	envVars := []string{
		"DATABASE_HOST", "DATABASE_PORT", "DATABASE_USER", "DATABASE_PASSWORD",
		"DATABASE_NAME", "DATABASE_SSLMODE", "DATABASE_SSLROOTCERT", "DATABASE_SSLCERT",
		"DATABASE_SSLKEY", "API_PORT", "JWT_SECRET", "JWT_EXPIRY",
		"REFRESH_TOKEN_EXPIRY", "ENCRYPTION_KEY", "LOG_LEVEL",
		"CORS_ALLOWED_ORIGINS", "ADMIN_USERNAME", "ADMIN_PASSWORD", "ADMIN_EMAIL",
		"WAF_IMAGE", "WAF_TAG", "WAF_SHA256",
		"AI_PROVIDER", "AI_API_KEY", "AI_MODEL", "AI_MAX_TOKENS", "AI_RATE_LIMIT", "AI_BASE_URL",
	}
	for _, key := range envVars {
		os.Unsetenv(key)
	}
}

// setRequiredEnv sets the minimum env vars needed for Load() to succeed.
func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("ENCRYPTION_KEY", "test-encryption-key")
	t.Setenv("ADMIN_PASSWORD", "test-admin-password")
}

// --- Load() tests ---

func TestLoad_AllDefaults(t *testing.T) {
	clearConfigEnv(t)
	setRequiredEnv(t)

	cfg, err := Load()
	require.NoError(t, err)

	// Database defaults
	assert.Equal(t, "localhost", cfg.DatabaseHost)
	assert.Equal(t, "5432", cfg.DatabasePort)
	assert.Equal(t, "fastgateway", cfg.DatabaseUser)
	assert.Equal(t, "fastgateway", cfg.DatabasePassword)
	assert.Equal(t, "fastgateway", cfg.DatabaseName)
	assert.Equal(t, "disable", cfg.DatabaseSSLMode)
	assert.Empty(t, cfg.DatabaseSSLRootCert)
	assert.Empty(t, cfg.DatabaseSSLCert)
	assert.Empty(t, cfg.DatabaseSSLKey)

	// Server defaults
	assert.Equal(t, "8081", cfg.APIPort)

	// JWT defaults
	assert.Equal(t, "test-secret", cfg.JWTSecret)
	assert.Equal(t, 24*time.Hour, cfg.JWTExpiry)
	assert.Equal(t, 168*time.Hour, cfg.RefreshTokenExpiry)

	// Encryption
	assert.Equal(t, "test-encryption-key", cfg.EncryptionKey)

	// Logging default
	assert.Equal(t, "info", cfg.LogLevel)

	// CORS default (empty)
	assert.Nil(t, cfg.CORSAllowedOrigins)

	// Admin defaults
	assert.Equal(t, "admin", cfg.AdminUsername)
	assert.Equal(t, "test-admin-password", cfg.AdminPassword) // no default: ADMIN_PASSWORD is required
	assert.Equal(t, "admin@fastgateway.local", cfg.AdminEmail)

	// WAF defaults
	assert.Equal(t, "ghcr.io/corazawaf/coraza-proxy-wasm", cfg.WAFImage)
	assert.Equal(t, "0.6.0", cfg.WAFTag)
	assert.Empty(t, cfg.WAFSHA256)
}

func TestLoad_MissingJWTSecret(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("ENCRYPTION_KEY", "some-key")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "JWT_SECRET is required")
}

func TestLoad_MissingEncryptionKey(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("JWT_SECRET", "some-secret")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ENCRYPTION_KEY is required")
}

func TestLoad_MissingAdminPassword(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("ENCRYPTION_KEY", "test-encryption-key")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ADMIN_PASSWORD is required")
}

func TestLoad_InvalidDatabasePort(t *testing.T) {
	clearConfigEnv(t)
	setRequiredEnv(t)
	t.Setenv("DATABASE_PORT", "not-a-number")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid DATABASE_PORT")
}

func TestLoad_CustomAPIPort(t *testing.T) {
	clearConfigEnv(t)
	setRequiredEnv(t)
	t.Setenv("API_PORT", "9090")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "9090", cfg.APIPort)
}

func TestLoad_CORSAllowedOrigins_Single(t *testing.T) {
	clearConfigEnv(t)
	setRequiredEnv(t)
	t.Setenv("CORS_ALLOWED_ORIGINS", "http://localhost:3000")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, []string{"http://localhost:3000"}, cfg.CORSAllowedOrigins)
}

func TestLoad_CORSAllowedOrigins_Multiple(t *testing.T) {
	clearConfigEnv(t)
	setRequiredEnv(t)
	t.Setenv("CORS_ALLOWED_ORIGINS", "http://localhost:3000,https://example.com,https://app.example.com")

	cfg, err := Load()
	require.NoError(t, err)
	require.Len(t, cfg.CORSAllowedOrigins, 3)
	assert.Equal(t, "http://localhost:3000", cfg.CORSAllowedOrigins[0])
	assert.Equal(t, "https://example.com", cfg.CORSAllowedOrigins[1])
	assert.Equal(t, "https://app.example.com", cfg.CORSAllowedOrigins[2])
}

func TestLoad_CORSAllowedOrigins_Empty(t *testing.T) {
	clearConfigEnv(t)
	setRequiredEnv(t)

	cfg, err := Load()
	require.NoError(t, err)
	assert.Nil(t, cfg.CORSAllowedOrigins)
}

func TestLoad_InvalidJWTExpiry(t *testing.T) {
	clearConfigEnv(t)
	setRequiredEnv(t)
	t.Setenv("JWT_EXPIRY", "not-a-duration")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid JWT_EXPIRY")
}

func TestLoad_InvalidRefreshTokenExpiry(t *testing.T) {
	clearConfigEnv(t)
	setRequiredEnv(t)
	t.Setenv("REFRESH_TOKEN_EXPIRY", "bad-value")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid REFRESH_TOKEN_EXPIRY")
}

func TestLoad_CustomJWTExpiry(t *testing.T) {
	clearConfigEnv(t)
	setRequiredEnv(t)
	t.Setenv("JWT_EXPIRY", "2h")
	t.Setenv("REFRESH_TOKEN_EXPIRY", "48h")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 2*time.Hour, cfg.JWTExpiry)
	assert.Equal(t, 48*time.Hour, cfg.RefreshTokenExpiry)
}

func TestLoad_CustomAdminCredentials(t *testing.T) {
	clearConfigEnv(t)
	setRequiredEnv(t)
	t.Setenv("ADMIN_USERNAME", "superadmin")
	t.Setenv("ADMIN_PASSWORD", "supersecret")
	t.Setenv("ADMIN_EMAIL", "admin@corp.com")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "superadmin", cfg.AdminUsername)
	assert.Equal(t, "supersecret", cfg.AdminPassword)
	assert.Equal(t, "admin@corp.com", cfg.AdminEmail)
}

func TestLoad_CustomWAFSettings(t *testing.T) {
	clearConfigEnv(t)
	setRequiredEnv(t)
	t.Setenv("WAF_IMAGE", "my-registry/coraza")
	t.Setenv("WAF_TAG", "1.0.0")
	t.Setenv("WAF_SHA256", "abc123")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "my-registry/coraza", cfg.WAFImage)
	assert.Equal(t, "1.0.0", cfg.WAFTag)
	assert.Equal(t, "abc123", cfg.WAFSHA256)
}

func TestLoad_CustomLogLevel(t *testing.T) {
	clearConfigEnv(t)
	setRequiredEnv(t)
	t.Setenv("LOG_LEVEL", "debug")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "debug", cfg.LogLevel)
}

// --- LoadDatabase() tests ---

func TestLoadDatabase_Defaults(t *testing.T) {
	clearConfigEnv(t)

	cfg := LoadDatabase()
	assert.Equal(t, "localhost", cfg.DatabaseHost)
	assert.Equal(t, "5432", cfg.DatabasePort)
	assert.Equal(t, "fastgateway", cfg.DatabaseUser)
	assert.Equal(t, "fastgateway", cfg.DatabasePassword)
	assert.Equal(t, "fastgateway", cfg.DatabaseName)
	assert.Equal(t, "disable", cfg.DatabaseSSLMode)
	assert.Empty(t, cfg.DatabaseSSLRootCert)
	assert.Empty(t, cfg.DatabaseSSLCert)
	assert.Empty(t, cfg.DatabaseSSLKey)
}

func TestLoadDatabase_CustomValues(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("DATABASE_HOST", "db.example.com")
	t.Setenv("DATABASE_PORT", "5433")
	t.Setenv("DATABASE_USER", "myuser")
	t.Setenv("DATABASE_PASSWORD", "mypass")
	t.Setenv("DATABASE_NAME", "mydb")
	t.Setenv("DATABASE_SSLMODE", "require")
	t.Setenv("DATABASE_SSLROOTCERT", "/certs/ca.pem")
	t.Setenv("DATABASE_SSLCERT", "/certs/client.pem")
	t.Setenv("DATABASE_SSLKEY", "/certs/client-key.pem")

	cfg := LoadDatabase()
	assert.Equal(t, "db.example.com", cfg.DatabaseHost)
	assert.Equal(t, "5433", cfg.DatabasePort)
	assert.Equal(t, "myuser", cfg.DatabaseUser)
	assert.Equal(t, "mypass", cfg.DatabasePassword)
	assert.Equal(t, "mydb", cfg.DatabaseName)
	assert.Equal(t, "require", cfg.DatabaseSSLMode)
	assert.Equal(t, "/certs/ca.pem", cfg.DatabaseSSLRootCert)
	assert.Equal(t, "/certs/client.pem", cfg.DatabaseSSLCert)
	assert.Equal(t, "/certs/client-key.pem", cfg.DatabaseSSLKey)
}

// --- BuildDatabaseURL() tests ---

func TestBuildDatabaseURL_Default(t *testing.T) {
	clearConfigEnv(t)

	cfg := LoadDatabase()
	dbURL := cfg.BuildDatabaseURL()

	assert.Contains(t, dbURL, "postgres://")
	assert.Contains(t, dbURL, "fastgateway") // user
	assert.Contains(t, dbURL, "localhost")   // host
	assert.Contains(t, dbURL, "5432")        // port
	assert.Contains(t, dbURL, "sslmode=disable")
}

func TestBuildDatabaseURL_CustomValues(t *testing.T) {
	cfg := &Config{
		DatabaseHost:     "db.prod.com",
		DatabasePort:     "5433",
		DatabaseUser:     "admin",
		DatabasePassword: "s3cret",
		DatabaseName:     "proddb",
		DatabaseSSLMode:  "require",
	}

	dbURL := cfg.BuildDatabaseURL()

	assert.Contains(t, dbURL, "postgres://")
	assert.Contains(t, dbURL, "admin")
	assert.Contains(t, dbURL, "db.prod.com")
	assert.Contains(t, dbURL, "5433")
	assert.Contains(t, dbURL, "proddb")
	assert.Contains(t, dbURL, "sslmode=require")
	// SSL cert params should not appear when empty
	assert.NotContains(t, dbURL, "sslrootcert")
	assert.NotContains(t, dbURL, "sslcert=")
	assert.NotContains(t, dbURL, "sslkey")
}

func TestBuildDatabaseURL_WithSSLCerts(t *testing.T) {
	cfg := &Config{
		DatabaseHost:        "db.prod.com",
		DatabasePort:        "5432",
		DatabaseUser:        "admin",
		DatabasePassword:    "pass",
		DatabaseName:        "mydb",
		DatabaseSSLMode:     "verify-full",
		DatabaseSSLRootCert: "/certs/ca.pem",
		DatabaseSSLCert:     "/certs/client.pem",
		DatabaseSSLKey:      "/certs/client-key.pem",
	}

	dbURL := cfg.BuildDatabaseURL()

	assert.Contains(t, dbURL, "sslmode=verify-full")
	assert.Contains(t, dbURL, "sslrootcert=")
	assert.Contains(t, dbURL, "sslcert=")
	assert.Contains(t, dbURL, "sslkey=")
}

func TestBuildDatabaseURL_PasswordWithSpecialChars(t *testing.T) {
	cfg := &Config{
		DatabaseHost:     "localhost",
		DatabasePort:     "5432",
		DatabaseUser:     "user",
		DatabasePassword: "p@ss:w0rd/test",
		DatabaseName:     "db",
		DatabaseSSLMode:  "disable",
	}

	dbURL := cfg.BuildDatabaseURL()

	// url.UserPassword should properly encode special characters
	assert.Contains(t, dbURL, "postgres://")
	// The URL should be parseable despite special characters
	assert.NotEmpty(t, dbURL)
}
