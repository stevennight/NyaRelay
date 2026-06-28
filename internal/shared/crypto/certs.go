package crypto

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"strings"
	"time"
)

type TunnelCertificates struct {
	CACert     string
	ServerCert string
	ServerKey  string
	ClientCert string
	ClientKey  string
}

func GenerateTunnelCertificates(name string, serverName string) (TunnelCertificates, error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return TunnelCertificates{}, err
	}
	now := time.Now().UTC()
	caTemplate := x509.Certificate{
		SerialNumber:          serialNumber(),
		Subject:               pkix.Name{CommonName: name + " ca"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(3 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, &caTemplate, &caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return TunnelCertificates{}, err
	}
	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return TunnelCertificates{}, err
	}
	serverTemplate := leafTemplate(name+" server", serverName, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
	serverDER, err := x509.CreateCertificate(rand.Reader, &serverTemplate, &caTemplate, &serverKey.PublicKey, caKey)
	if err != nil {
		return TunnelCertificates{}, err
	}
	clientKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return TunnelCertificates{}, err
	}
	clientTemplate := leafTemplate(name+" client", "", []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
	clientDER, err := x509.CreateCertificate(rand.Reader, &clientTemplate, &caTemplate, &clientKey.PublicKey, caKey)
	if err != nil {
		return TunnelCertificates{}, err
	}
	serverKeyDER, err := x509.MarshalECPrivateKey(serverKey)
	if err != nil {
		return TunnelCertificates{}, err
	}
	clientKeyDER, err := x509.MarshalECPrivateKey(clientKey)
	if err != nil {
		return TunnelCertificates{}, err
	}
	return TunnelCertificates{
		CACert:     pemString("CERTIFICATE", caDER),
		ServerCert: pemString("CERTIFICATE", serverDER),
		ServerKey:  pemString("EC PRIVATE KEY", serverKeyDER),
		ClientCert: pemString("CERTIFICATE", clientDER),
		ClientKey:  pemString("EC PRIVATE KEY", clientKeyDER),
	}, nil
}

func leafTemplate(commonName string, serverName string, usages []x509.ExtKeyUsage) x509.Certificate {
	now := time.Now().UTC()
	template := x509.Certificate{
		SerialNumber: serialNumber(),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  usages,
	}
	if serverName != "" {
		host := strings.Split(serverName, ":")[0]
		if ip := net.ParseIP(host); ip != nil {
			template.IPAddresses = []net.IP{ip}
		} else {
			template.DNSNames = []string{host}
		}
	}
	return template
}

func serialNumber() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return big.NewInt(time.Now().UnixNano())
	}
	return n
}

func pemString(blockType string, der []byte) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der}))
}
