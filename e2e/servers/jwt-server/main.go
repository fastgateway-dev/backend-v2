package main

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var (
	privateKey *rsa.PrivateKey
	keyID      = "test-key-1"
)

type JWK struct {
	Kty string `json:"kty"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type JWKS struct {
	Keys []JWK `json:"keys"`
}

type TokenRequest struct {
	Sub          string         `json:"sub"`
	Iss          string         `json:"iss"`
	Aud          interface{}    `json:"aud"`
	Exp          string         `json:"exp"`
	CustomClaims map[string]any `json:"claims"`
}

func init() {
	var err error
	privateKey, err = rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Fatalf("Failed to generate RSA key pair: %v", err)
	}
	log.Println("RSA-2048 key pair generated successfully")
}

func getBaseURL() string {
	host := os.Getenv("JWT_SERVER_HOST")
	if host == "" {
		host = "http://jwt-server:9000"
	}
	return host
}

func handleJWKS(w http.ResponseWriter, r *http.Request) {
	pub := &privateKey.PublicKey

	jwks := JWKS{
		Keys: []JWK{
			{
				Kty: "RSA",
				Use: "sig",
				Alg: "RS256",
				Kid: keyID,
				N:   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
				E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
			},
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(jwks)
}

func handleToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req TokenRequest
	if r.Body != nil && r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
			return
		}
	}

	issuer := req.Iss
	if issuer == "" {
		issuer = getBaseURL()
	}

	sub := req.Sub
	if sub == "" {
		sub = "test-user"
	}

	expDuration := time.Hour
	if req.Exp != "" {
		parsed, err := time.ParseDuration(req.Exp)
		if err != nil {
			http.Error(w, fmt.Sprintf("Invalid exp duration: %v", err), http.StatusBadRequest)
			return
		}
		expDuration = parsed
	}

	now := time.Now()
	claims := jwt.MapClaims{
		"iss": issuer,
		"sub": sub,
		"iat": now.Unix(),
		"exp": now.Add(expDuration).Unix(),
	}

	if req.Aud != nil {
		claims["aud"] = req.Aud
	}

	for k, v := range req.CustomClaims {
		claims[k] = v
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = keyID

	tokenString, err := token.SignedString(privateKey)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to sign token: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"token":      tokenString,
		"expires_at": now.Add(expDuration).UTC().Format(time.RFC3339),
		"claims":     claims,
	})
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "9000"
	}

	http.HandleFunc("/jwks", handleJWKS)
	http.HandleFunc("/token", handleToken)
	http.HandleFunc("/health", handleHealth)

	log.Printf("JWT test server starting on :%s", port)
	log.Printf("  JWKS endpoint:   http://localhost:%s/jwks", port)
	log.Printf("  Token endpoint:  http://localhost:%s/token (POST)", port)
	log.Printf("  Health endpoint: http://localhost:%s/health", port)
	log.Printf("  Default issuer:  %s", getBaseURL())

	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
