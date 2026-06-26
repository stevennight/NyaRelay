package crypto

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
)

func GenerateSigningKey() (publicKey, privateKey string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	return EncodeKey(pub), EncodeKey(priv), nil
}

func EncodeKey(key []byte) string {
	return base64.RawURLEncoding.EncodeToString(key)
}

func DecodePublicKey(encoded string) (ed25519.PublicKey, error) {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, errors.New("invalid public key length")
	}
	return ed25519.PublicKey(raw), nil
}

func DecodePrivateKey(encoded string) (ed25519.PrivateKey, error) {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	if len(raw) != ed25519.PrivateKeySize {
		return nil, errors.New("invalid private key length")
	}
	return ed25519.PrivateKey(raw), nil
}

func SignJSON(privateKey string, value any) (string, error) {
	priv, err := DecodePrivateKey(privateKey)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sig := ed25519.Sign(priv, payload)
	return EncodeKey(sig), nil
}

func VerifyJSON(publicKey string, value any, signature string) error {
	pub, err := DecodePublicKey(publicKey)
	if err != nil {
		return err
	}
	sig, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if !ed25519.Verify(pub, payload, sig) {
		return errors.New("signature verification failed")
	}
	return nil
}
