package crypto

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := "my-secret-encryption-key-123456!"
	plaintexts := []string{
		"hello world",
		"some sensitive data with special chars: !@#$%^&*()",
		"unicode text: こんにちは世界",
		"a]longer string that spans multiple AES blocks to ensure the encryption handles arbitrary lengths properly",
	}

	for _, pt := range plaintexts {
		encrypted, err := Encrypt(pt, key)
		require.NoError(t, err, "Encrypt should not error for plaintext: %s", pt)
		assert.NotEmpty(t, encrypted)
		assert.NotEqual(t, pt, encrypted, "ciphertext should differ from plaintext")

		decrypted, err := Decrypt(encrypted, key)
		require.NoError(t, err, "Decrypt should not error")
		assert.Equal(t, pt, decrypted, "decrypted text should match original plaintext")
	}
}

func TestEncryptDecryptEmptyString(t *testing.T) {
	key := "test-key-for-empty-string"

	encrypted, err := Encrypt("", key)
	require.NoError(t, err)
	assert.NotEmpty(t, encrypted, "even empty plaintext should produce ciphertext")

	decrypted, err := Decrypt(encrypted, key)
	require.NoError(t, err)
	assert.Equal(t, "", decrypted, "decrypting encrypted empty string should return empty string")
}

func TestDecryptWithWrongKey(t *testing.T) {
	correctKey := "correct-key-abcdefghijklmnop"
	wrongKey := "wrong-key-zyxwvutsrqponmlkj"

	encrypted, err := Encrypt("secret message", correctKey)
	require.NoError(t, err)

	_, err = Decrypt(encrypted, wrongKey)
	assert.Error(t, err, "decrypting with wrong key should produce an error")
}

func TestDecryptInvalidCiphertext(t *testing.T) {
	key := "some-key-for-invalid-tests"

	t.Run("not valid base64", func(t *testing.T) {
		_, err := Decrypt("not-valid-base64!!!", key)
		assert.Error(t, err)
	})

	t.Run("valid base64 but too short", func(t *testing.T) {
		short := base64.StdEncoding.EncodeToString([]byte("tiny"))
		_, err := Decrypt(short, key)
		assert.Error(t, err, "ciphertext shorter than nonce size should error")
	})

	t.Run("valid base64 but corrupted ciphertext", func(t *testing.T) {
		// Create valid ciphertext first, then corrupt it
		encrypted, err := Encrypt("test data", key)
		require.NoError(t, err)

		data, err := base64.StdEncoding.DecodeString(encrypted)
		require.NoError(t, err)

		// Flip some bytes in the ciphertext portion (after the nonce)
		if len(data) > 15 {
			data[14] ^= 0xFF
			data[15] ^= 0xFF
		}
		corrupted := base64.StdEncoding.EncodeToString(data)

		_, err = Decrypt(corrupted, key)
		assert.Error(t, err, "corrupted ciphertext should produce an error")
	})
}

func TestSamePlaintextProducesDifferentCiphertexts(t *testing.T) {
	key := "deterministic-key-for-iv-test!!"
	plaintext := "same input every time"

	encrypted1, err := Encrypt(plaintext, key)
	require.NoError(t, err)

	encrypted2, err := Encrypt(plaintext, key)
	require.NoError(t, err)

	assert.NotEqual(t, encrypted1, encrypted2,
		"encrypting the same plaintext twice should produce different ciphertexts due to random nonce")

	// Both should still decrypt to the same plaintext
	dec1, err := Decrypt(encrypted1, key)
	require.NoError(t, err)
	dec2, err := Decrypt(encrypted2, key)
	require.NoError(t, err)
	assert.Equal(t, dec1, dec2)
	assert.Equal(t, plaintext, dec1)
}

func TestPrepareKeyNormalization(t *testing.T) {
	// prepareKey pads short keys and truncates long keys to 32 bytes.
	// This means any key length works, but we should verify that
	// encrypt/decrypt still functions correctly with various key sizes.

	t.Run("short key padded to 32 bytes", func(t *testing.T) {
		shortKey := "abc"
		encrypted, err := Encrypt("data", shortKey)
		require.NoError(t, err)

		decrypted, err := Decrypt(encrypted, shortKey)
		require.NoError(t, err)
		assert.Equal(t, "data", decrypted)
	})

	t.Run("exact 32-byte key", func(t *testing.T) {
		exactKey := "12345678901234567890123456789012" // exactly 32 bytes
		encrypted, err := Encrypt("data", exactKey)
		require.NoError(t, err)

		decrypted, err := Decrypt(encrypted, exactKey)
		require.NoError(t, err)
		assert.Equal(t, "data", decrypted)
	})

	t.Run("long key truncated to 32 bytes", func(t *testing.T) {
		longKey := "this-key-is-definitely-longer-than-thirty-two-bytes-long"
		encrypted, err := Encrypt("data", longKey)
		require.NoError(t, err)

		decrypted, err := Decrypt(encrypted, longKey)
		require.NoError(t, err)
		assert.Equal(t, "data", decrypted)
	})

	t.Run("empty key still works due to padding", func(t *testing.T) {
		encrypted, err := Encrypt("data", "")
		require.NoError(t, err)

		decrypted, err := Decrypt(encrypted, "")
		require.NoError(t, err)
		assert.Equal(t, "data", decrypted)
	})

	t.Run("keys differing only beyond 32 bytes produce same result", func(t *testing.T) {
		key1 := "12345678901234567890123456789012-suffix-a"
		key2 := "12345678901234567890123456789012-suffix-b"

		encrypted, err := Encrypt("secret", key1)
		require.NoError(t, err)

		// key2 should also decrypt since first 32 bytes match
		decrypted, err := Decrypt(encrypted, key2)
		require.NoError(t, err)
		assert.Equal(t, "secret", decrypted)
	})
}
