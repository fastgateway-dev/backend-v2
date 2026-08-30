package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestJWTAudiences_ScanValue(t *testing.T) {
	// Value nil
	var nilAud JWTAudiences
	val, err := nilAud.Value()
	assert.NoError(t, err)
	assert.Nil(t, val)

	// Value non-nil
	aud := JWTAudiences{"aud1", "aud2"}
	val, err = aud.Value()
	assert.NoError(t, err)
	assert.NotNil(t, val)

	// Scan bytes
	var scanned JWTAudiences
	err = scanned.Scan(val.([]byte))
	assert.NoError(t, err)
	assert.Equal(t, JWTAudiences{"aud1", "aud2"}, scanned)

	// Scan string
	var scanned2 JWTAudiences
	err = scanned2.Scan(`["aud3"]`)
	assert.NoError(t, err)
	assert.Equal(t, JWTAudiences{"aud3"}, scanned2)

	// Scan nil
	var scanned3 JWTAudiences
	err = scanned3.Scan(nil)
	assert.NoError(t, err)
	assert.Nil(t, scanned3)

	// Scan wrong type
	err = scanned3.Scan(123)
	assert.Error(t, err)
}

func TestJWTRequiredClaims_ScanValue(t *testing.T) {
	// Value nil
	var nilClaims JWTRequiredClaims
	val, err := nilClaims.Value()
	assert.NoError(t, err)
	assert.Nil(t, val)

	// Value non-nil
	claims := JWTRequiredClaims{{Name: "role", Values: []string{"admin"}}}
	val, err = claims.Value()
	assert.NoError(t, err)
	assert.NotNil(t, val)

	// Scan bytes
	var scanned JWTRequiredClaims
	err = scanned.Scan(val.([]byte))
	assert.NoError(t, err)
	assert.Len(t, scanned, 1)
	assert.Equal(t, "role", scanned[0].Name)

	// Scan string
	var scanned2 JWTRequiredClaims
	err = scanned2.Scan(`[{"name":"scope","values":["read"]}]`)
	assert.NoError(t, err)
	assert.Len(t, scanned2, 1)

	// Scan nil
	var scanned3 JWTRequiredClaims
	err = scanned3.Scan(nil)
	assert.NoError(t, err)
	assert.Nil(t, scanned3)

	// Scan wrong type
	err = scanned3.Scan(123)
	assert.Error(t, err)
}

func TestJWTClaimToHeaders_ScanValue(t *testing.T) {
	// Value nil
	var nilHeaders JWTClaimToHeaders
	val, err := nilHeaders.Value()
	assert.NoError(t, err)
	assert.Nil(t, val)

	// Value non-nil
	headers := JWTClaimToHeaders{{Claim: "sub", Header: "x-user"}}
	val, err = headers.Value()
	assert.NoError(t, err)
	assert.NotNil(t, val)

	// Scan bytes
	var scanned JWTClaimToHeaders
	err = scanned.Scan(val.([]byte))
	assert.NoError(t, err)
	assert.Len(t, scanned, 1)

	// Scan string
	var scanned2 JWTClaimToHeaders
	err = scanned2.Scan(`[{"claim":"sub","header":"x-user"}]`)
	assert.NoError(t, err)
	assert.Len(t, scanned2, 1)

	// Scan nil
	var scanned3 JWTClaimToHeaders
	err = scanned3.Scan(nil)
	assert.NoError(t, err)
	assert.Nil(t, scanned3)

	// Scan wrong type
	err = scanned3.Scan(123)
	assert.Error(t, err)
}

func TestMTLSSANList_ScanValue(t *testing.T) {
	// Value nil
	var nilSans MTLSSANList
	val, err := nilSans.Value()
	assert.NoError(t, err)
	assert.Nil(t, val)

	// Value non-nil
	sans := MTLSSANList{{Type: "DNS", Value: "example.com"}}
	val, err = sans.Value()
	assert.NoError(t, err)
	assert.NotNil(t, val)

	// Scan bytes
	var scanned MTLSSANList
	err = scanned.Scan(val.([]byte))
	assert.NoError(t, err)
	assert.Len(t, scanned, 1)

	// Scan string
	var scanned2 MTLSSANList
	err = scanned2.Scan(`[{"type":"DNS","value":"test.com"}]`)
	assert.NoError(t, err)
	assert.Len(t, scanned2, 1)

	// Scan nil
	var scanned3 MTLSSANList
	err = scanned3.Scan(nil)
	assert.NoError(t, err)
	assert.Nil(t, scanned3)

	// Scan wrong type
	err = scanned3.Scan(123)
	assert.Error(t, err)
}

func TestStringList_ScanValue(t *testing.T) {
	// Value nil
	var nilList StringList
	val, err := nilList.Value()
	assert.NoError(t, err)
	assert.Nil(t, val)

	// Value non-nil
	list := StringList{"a", "b"}
	val, err = list.Value()
	assert.NoError(t, err)
	assert.NotNil(t, val)

	// Scan bytes
	var scanned StringList
	err = scanned.Scan(val.([]byte))
	assert.NoError(t, err)
	assert.Equal(t, StringList{"a", "b"}, scanned)

	// Scan string
	var scanned2 StringList
	err = scanned2.Scan(`["c","d"]`)
	assert.NoError(t, err)
	assert.Equal(t, StringList{"c", "d"}, scanned2)

	// Scan nil
	var scanned3 StringList
	err = scanned3.Scan(nil)
	assert.NoError(t, err)
	assert.Nil(t, scanned3)

	// Scan wrong type
	err = scanned3.Scan(123)
	assert.Error(t, err)
}

func TestClient_ValidateMTLSConfig(t *testing.T) {
	validHash := "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"

	tests := []struct {
		name    string
		client  Client
		wantErr bool
		errMsg  string
	}{
		{
			name:    "mTLS disabled",
			client:  Client{MTLSEnabled: false},
			wantErr: false,
		},
		{
			name:    "enabled without CA secret",
			client:  Client{MTLSEnabled: true},
			wantErr: true,
			errMsg:  "CA certificate is required",
		},
		{
			name: "enabled without SANs or hashes",
			client: Client{
				MTLSEnabled:  true,
				MTLSCASecret: "my-ca",
			},
			wantErr: true,
			errMsg:  "at least one SAN or certificate hash",
		},
		{
			name: "valid with SAN",
			client: Client{
				MTLSEnabled:  true,
				MTLSCASecret: "my-ca",
				MTLSSANs:     MTLSSANList{{Type: "DNS", Value: "client.example.com"}},
			},
			wantErr: false,
		},
		{
			name: "valid with hash",
			client: Client{
				MTLSEnabled:  true,
				MTLSCASecret: "my-ca",
				MTLSHashes:   StringList{validHash},
			},
			wantErr: false,
		},
		{
			name: "valid with both SAN and hash",
			client: Client{
				MTLSEnabled:  true,
				MTLSCASecret: "my-ca",
				MTLSSANs:     MTLSSANList{{Type: "URI", Value: "spiffe://example.com/client"}},
				MTLSHashes:   StringList{validHash},
			},
			wantErr: false,
		},
		{
			name: "invalid SAN type",
			client: Client{
				MTLSEnabled:  true,
				MTLSCASecret: "my-ca",
				MTLSSANs:     MTLSSANList{{Type: "IP", Value: "10.0.0.1"}},
			},
			wantErr: true,
			errMsg:  "SAN type must be 'DNS' or 'URI'",
		},
		{
			name: "empty SAN value",
			client: Client{
				MTLSEnabled:  true,
				MTLSCASecret: "my-ca",
				MTLSSANs:     MTLSSANList{{Type: "DNS", Value: ""}},
			},
			wantErr: true,
			errMsg:  "SAN value cannot be empty",
		},
		{
			name: "invalid hash length",
			client: Client{
				MTLSEnabled:  true,
				MTLSCASecret: "my-ca",
				MTLSHashes:   StringList{"tooshort"},
			},
			wantErr: true,
			errMsg:  "certificate hash must be 64 hex characters",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.client.ValidateMTLSConfig()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
