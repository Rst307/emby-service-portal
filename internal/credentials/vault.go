package credentials

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
)

const ciphertextPrefix = "v2:"

type Vault struct{ keys [][32]byte }

// New creates a vault whose first key encrypts new values. Fallback secrets
// are accepted only for decryption, which permits a staged key rotation and
// migration from the historical API-key-derived vault.
func New(primarySecret string, fallbackSecrets ...string) *Vault {
	secrets := append([]string{primarySecret}, fallbackSecrets...)
	keys := make([][32]byte, 0, len(secrets))
	seen := make(map[string]struct{}, len(secrets))
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		if _, duplicate := seen[secret]; duplicate {
			continue
		}
		seen[secret] = struct{}{}
		// The derivation context is a historical compatibility constant: changing
		// it would make previously encrypted credentials undecryptable.
		keys = append(keys, sha256.Sum256([]byte("emby-user-manager/password-vault/v1/"+secret)))
	}
	return &Vault{keys: keys}
}

func (v *Vault) Seal(username, password string) (string, error) {
	if len(v.keys) == 0 {
		return "", fmt.Errorf("password vault has no encryption key")
	}
	encoded, err := seal(v.keys[0], username, password)
	if err != nil {
		return "", err
	}
	return ciphertextPrefix + encoded, nil
}

// Fingerprint returns a keyed, non-reversible comparison value for a request.
// It lets idempotency records reject a reused key with different input without
// adding a password verifier to the database.
func (v *Vault) Fingerprint(values ...string) (string, error) {
	if len(v.keys) == 0 {
		return "", fmt.Errorf("password vault has no encryption key")
	}
	mac := hmac.New(sha256.New, v.keys[0][:])
	for _, value := range values {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = mac.Write(length[:])
		_, _ = mac.Write([]byte(value))
	}
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (v *Vault) Open(username, value string) (string, error) {
	encoded := strings.TrimPrefix(value, ciphertextPrefix)
	raw, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	for _, key := range v.keys {
		password, err := open(key, username, raw)
		if err == nil {
			return password, nil
		}
	}
	return "", fmt.Errorf("cannot decrypt password with configured credential keys")
}

func seal(key [32]byte, username, password string) (string, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(append(nonce, gcm.Seal(nil, nonce, []byte(password), []byte(username))...)), nil
}

func open(key [32]byte, username string, raw []byte) (string, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", fmt.Errorf("invalid encrypted password")
	}
	password, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], []byte(username))
	return string(password), err
}
