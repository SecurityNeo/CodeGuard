package encrypt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"sync"
)

var (
	globalKey []byte
	once      sync.Once
)

func Init(key string) {
	once.Do(func() {
		if key == "" {
			// fallback: generate a random key for dev (NOT for production)
			b := make([]byte, 32)
			_, _ = rand.Read(b)
			globalKey = b
			return
		}
		globalKey = []byte(key)
		if len(globalKey) < 32 {
			// pad to 32 bytes
			padded := make([]byte, 32)
			copy(padded, globalKey)
			globalKey = padded
		}
	})
}

func getKey() []byte {
	if globalKey == nil {
		Init(os.Getenv("ENCRYPTION_KEY"))
	}
	return globalKey[:32]
}

func Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	block, err := aes.NewCipher(getKey())
	if err != nil {
		return "", err
	}
	nonce := make([]byte, 12)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func Decrypt(ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}
	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return ciphertext, nil
	}
	if len(data) < 12 {
		return "", fmt.Errorf("invalid ciphertext")
	}
	block, err := aes.NewCipher(getKey())
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce, cipherdata := data[:12], data[12:]
	plaintext, err := gcm.Open(nil, nonce, cipherdata, nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}
