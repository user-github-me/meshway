package state

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

var vaultHeader = []byte("meshway-vault-v1\x00")

// KeyFromEnvironment supports native daemon launchers without putting the
// storage key in JSON configuration, command arguments, or logs.
func KeyFromEnvironment() ([]byte, error) {
	value := os.Getenv("MESHWAY_STATE_KEY")
	if value == "" {
		return nil, nil
	}
	key, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		key, err = base64.RawURLEncoding.DecodeString(value)
	}
	if err != nil || len(key) != 32 {
		return nil, errors.New("MESHWAY_STATE_KEY must encode a 32-byte storage key")
	}
	return key, nil
}

func stateCipher(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, errors.New("storage key must have 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func Seal(path string, data, key []byte) ([]byte, error) {
	if len(key) == 0 {
		return append([]byte(nil), data...), nil
	}
	aead, err := stateCipher(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	sealed := append(append([]byte(nil), vaultHeader...), nonce...)
	return aead.Seal(sealed, nonce, data, []byte("meshway-state:"+filepath.Base(path))), nil
}

// ReadPrivate decrypts vault files and migrates legacy files atomically when an
// OS-protected key is supplied. Authentication failures never use plaintext.
func ReadPrivate(path string, limit int, key []byte) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > int64(limit+128) {
		return nil, errors.New("private state must be a bounded regular file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("private state permissions must be 0600")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if bytes.HasPrefix(data, vaultHeader) {
		aead, err := stateCipher(key)
		if err != nil {
			return nil, errors.New("encrypted state requires its device storage key")
		}
		body := data[len(vaultHeader):]
		if len(body) < aead.NonceSize()+aead.Overhead() {
			return nil, errors.New("invalid encrypted state")
		}
		data, err = aead.Open(nil, body[:aead.NonceSize()], body[aead.NonceSize():], []byte("meshway-state:"+filepath.Base(path)))
		if err != nil {
			return nil, errors.New("state authentication failed; storage key is wrong or file was changed")
		}
	} else if len(key) != 0 {
		if len(data) > limit {
			return nil, errors.New("private state exceeds size limit")
		}
		if err = SavePrivate(path, data, key); err != nil {
			return nil, err
		}
	}
	if len(data) > limit {
		return nil, errors.New("private state exceeds size limit")
	}
	return data, nil
}

func SavePrivate(path string, data, key []byte) error {
	sealed, err := Seal(path, data, key)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".meshway-state-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(sealed)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(name, path); err != nil {
		return err
	}
	return SyncDirectory(filepath.Dir(path))
}
