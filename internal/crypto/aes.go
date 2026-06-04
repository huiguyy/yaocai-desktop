package crypto

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"crypto/aes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
)

// DecryptAESECB decrypts AES-ECB encrypted data (YSB search API response)
// Tries gzip → zlib → raw deflate → UTF-8 fallback, matching Python behavior
func DecryptAESECB(ciphertextB64 string, key string) (map[string]interface{}, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(ciphertextB64)
	if err != nil {
		return nil, fmt.Errorf("base64 decode failed: %w", err)
	}

	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		return nil, fmt.Errorf("AES cipher init failed: %w", err)
	}

	if len(ciphertext)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("ciphertext not multiple of block size")
	}

	plaintext := make([]byte, len(ciphertext))
	for i := 0; i < len(ciphertext); i += aes.BlockSize {
		block.Decrypt(plaintext[i:i+aes.BlockSize], ciphertext[i:i+aes.BlockSize])
	}

	// PKCS7 unpad - check if plaintext looks like valid JSON/gzip first
	padLen := int(plaintext[len(plaintext)-1])
	if padLen > 0 && padLen <= aes.BlockSize {
		// Verify padding is consistent
		valid := true
		for i := len(plaintext) - padLen; i < len(plaintext); i++ {
			if int(plaintext[i]) != padLen {
				valid = false
				break
			}
		}
		if valid {
			plaintext = plaintext[:len(plaintext)-padLen]
		}
		// If padding is invalid, try without unpadding
	}

	// Triple fallback decompress
	decompressed, err := tryDecompress(plaintext)
	if err != nil {
		// Log first bytes for debugging
		preview := len(plaintext)
		if preview > 20 {
			preview = 20
		}
		return nil, fmt.Errorf("all decompress methods failed (first bytes: %x): %w", plaintext[:preview], err)
	}

	return decompressed, nil
}

func tryDecompress(data []byte) (map[string]interface{}, error) {
	var lastErr error

	// 1. Try gzip (1f8b header)
	if m, err := gzipDecompressAndParse(data); err == nil {
		return m, nil
	} else {
		lastErr = err
	}

	// 2. Try zlib (78xx header)
	if m, err := decompressAndParse(data, 15); err == nil {
		return m, nil
	} else {
		lastErr = err
	}

	// 3. Try raw deflate
	if m, err := decompressAndParse(data, -15); err == nil {
		return m, nil
	} else {
		lastErr = err
	}

	// 4. Direct UTF-8 parse
	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err == nil {
		return result, nil
	}

	return nil, fmt.Errorf("all methods failed, last: %w", lastErr)
}

func gzipDecompressAndParse(data []byte) (map[string]interface{}, error) {
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("gzip reader: %w", err)
	}
	defer r.Close()
	decompressed, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("gzip read: %w", err)
	}
	var result map[string]interface{}
	if err := json.Unmarshal(decompressed, &result); err != nil {
		return nil, fmt.Errorf("JSON parse after gzip: %w", err)
	}
	return result, nil
}

func decompressAndParse(data []byte, wbits int) (map[string]interface{}, error) {
	decompressed, err := zlibDecompress(data, wbits)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(decompressed, &result); err != nil {
		return nil, fmt.Errorf("JSON parse after decompress: %w", err)
	}
	return result, nil
}

func zlibDecompress(data []byte, wbits int) ([]byte, error) {
	var r io.ReadCloser
	var err error

	if wbits > 0 {
		r, err = zlib.NewReader(bytes.NewReader(data))
	} else {
		// raw deflate
		r = flate.NewReader(bytes.NewReader(data))
	}
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

// EncryptAESECB encrypts data with AES-ECB + PKCS7 padding
func EncryptAESECB(plaintext []byte, key string) (string, error) {
	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		return "", err
	}

	// PKCS7 pad
	padLen := aes.BlockSize - len(plaintext)%aes.BlockSize
	padded := make([]byte, len(plaintext)+padLen)
	copy(padded, plaintext)
	for i := len(plaintext); i < len(padded); i++ {
		padded[i] = byte(padLen)
	}

	encrypted := make([]byte, len(padded))
	for i := 0; i < len(padded); i += aes.BlockSize {
		block.Encrypt(encrypted[i:i+aes.BlockSize], padded[i:i+aes.BlockSize])
	}

	return base64.StdEncoding.EncodeToString(encrypted), nil
}

// GenerateOParam generates the 'o' parameter for YSB search API
// AES-ECB encrypt the current timestamp (ms) with key "1AA00F7BB06A4E25"
func GenerateOParam(timestampMs int64) (string, error) {
	ts := fmt.Sprintf("%d", timestampMs)
	return EncryptAESECB([]byte(ts), "1AA00F7BB06A4E25")
}
