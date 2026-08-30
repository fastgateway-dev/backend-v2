package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all configuration for the application
type Config struct {
	// Database
	DatabaseHost        string
	DatabasePort        string
	DatabaseUser        string
	DatabasePassword    string
	DatabaseName        string
	DatabaseSSLMode     string
	DatabaseSSLRootCert string
	DatabaseSSLCert     string
	DatabaseSSLKey      string

	// Server
	APIPort string

	// JWT
	JWTSecret          string
	JWTExpiry          time.Duration
	RefreshTokenExpiry time.Duration

	// Encryption
	EncryptionKey string

	// Logging
	LogLevel string

	// CORS
	CORSAllowedOrigins []string

	// Default Admin
	AdminUsername string
	AdminPassword string
	AdminEmail    string

	// WAF (coraza-proxy-wasm)
	WAFImage  string
	WAFTag    string
	WAFSHA256 string

	// AI (optional; AI features are disabled unless AI_PROVIDER is set)
	AIProvider  string
	AIAPIKey    string
	AIModel     string
	AIMaxTokens int
	AIRateLimit int
	AIBaseURL   string
}

// Load loads configuration from environment variables
func Load() (*Config, error) {
	cfg := LoadDatabase()

	if _, err := strconv.Atoi(cfg.DatabasePort); err != nil {
		return nil, fmt.Errorf("invalid DATABASE_PORT: %w", err)
	}

	// Server
	cfg.APIPort = getEnv("API_PORT", "8081")

	// JWT
	cfg.JWTSecret = getEnv("JWT_SECRET", "")
	if cfg.JWTSecret == "" {
		return nil, fmt.Errorf("JWT_SECRET is required")
	}

	jwtExpiry, err := time.ParseDuration(getEnv("JWT_EXPIRY", "24h"))
	if err != nil {
		return nil, fmt.Errorf("invalid JWT_EXPIRY: %w", err)
	}
	cfg.JWTExpiry = jwtExpiry

	refreshExpiry, err := time.ParseDuration(getEnv("REFRESH_TOKEN_EXPIRY", "168h"))
	if err != nil {
		return nil, fmt.Errorf("invalid REFRESH_TOKEN_EXPIRY: %w", err)
	}
	cfg.RefreshTokenExpiry = refreshExpiry

	// Encryption
	cfg.EncryptionKey = getEnv("ENCRYPTION_KEY", "")
	if cfg.EncryptionKey == "" {
		return nil, fmt.Errorf("ENCRYPTION_KEY is required")
	}

	// Logging
	cfg.LogLevel = getEnv("LOG_LEVEL", "info")

	// CORS
	corsOrigins := getEnv("CORS_ALLOWED_ORIGINS", "")
	if corsOrigins != "" {
		cfg.CORSAllowedOrigins = strings.Split(corsOrigins, ",")
	}

	// Default Admin
	cfg.AdminUsername = getEnv("ADMIN_USERNAME", "admin")

	// ADMIN_PASSWORD has no default on purpose. The seeded admin account is a
	// full-privilege login on a service that manages internet-facing gateways,
	// so a well-known fallback password would be a published credential.
	cfg.AdminPassword = getEnv("ADMIN_PASSWORD", "")
	if cfg.AdminPassword == "" {
		return nil, fmt.Errorf("ADMIN_PASSWORD is required (no default is provided; set it to a strong secret)")
	}
	cfg.AdminEmail = getEnv("ADMIN_EMAIL", "admin@fastgateway.local")

	// WAF (coraza-proxy-wasm)
	cfg.WAFImage = getEnv("WAF_IMAGE", "ghcr.io/corazawaf/coraza-proxy-wasm")
	cfg.WAFTag = getEnv("WAF_TAG", "0.6.0")
	cfg.WAFSHA256 = getEnv("WAF_SHA256", "") // Optional, for pinning

	// AI (optional)
	cfg.AIProvider = getEnv("AI_PROVIDER", "")
	cfg.AIAPIKey = getEnv("AI_API_KEY", "")
	cfg.AIModel = getEnv("AI_MODEL", "")
	cfg.AIBaseURL = getEnv("AI_BASE_URL", "")

	aiMaxTokens, err := strconv.Atoi(getEnv("AI_MAX_TOKENS", "4096"))
	if err != nil {
		return nil, fmt.Errorf("invalid AI_MAX_TOKENS: %w", err)
	}
	cfg.AIMaxTokens = aiMaxTokens

	aiRateLimit, err := strconv.Atoi(getEnv("AI_RATE_LIMIT", "20"))
	if err != nil {
		return nil, fmt.Errorf("invalid AI_RATE_LIMIT: %w", err)
	}
	cfg.AIRateLimit = aiRateLimit

	return cfg, nil
}

// LoadDatabase loads only database-related configuration from environment variables.
// Use this when you only need a database connection (e.g., migrations CLI).
func LoadDatabase() *Config {
	cfg := &Config{}
	cfg.DatabaseHost = getEnv("DATABASE_HOST", "localhost")
	cfg.DatabasePort = getEnv("DATABASE_PORT", "5432")
	cfg.DatabaseUser = getEnv("DATABASE_USER", "fastgateway")
	cfg.DatabasePassword = getEnv("DATABASE_PASSWORD", "fastgateway")
	cfg.DatabaseName = getEnv("DATABASE_NAME", "fastgateway")
	cfg.DatabaseSSLMode = getEnv("DATABASE_SSLMODE", "disable")
	cfg.DatabaseSSLRootCert = getEnv("DATABASE_SSLROOTCERT", "")
	cfg.DatabaseSSLCert = getEnv("DATABASE_SSLCERT", "")
	cfg.DatabaseSSLKey = getEnv("DATABASE_SSLKEY", "")
	return cfg
}

// BuildDatabaseURL assembles a PostgreSQL connection string from individual config fields
func (c *Config) BuildDatabaseURL() string {
	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(c.DatabaseUser, c.DatabasePassword),
		Host:   net.JoinHostPort(c.DatabaseHost, c.DatabasePort),
		Path:   c.DatabaseName,
	}

	q := url.Values{}
	q.Set("sslmode", c.DatabaseSSLMode)
	if c.DatabaseSSLRootCert != "" {
		q.Set("sslrootcert", c.DatabaseSSLRootCert)
	}
	if c.DatabaseSSLCert != "" {
		q.Set("sslcert", c.DatabaseSSLCert)
	}
	if c.DatabaseSSLKey != "" {
		q.Set("sslkey", c.DatabaseSSLKey)
	}
	u.RawQuery = q.Encode()

	return u.String()
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
