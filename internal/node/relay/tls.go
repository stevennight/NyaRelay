package relay

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"

	"nyarelay/internal/shared/model"
)

func (s *Service) serverTLSConfig(link model.Link) (*tls.Config, error) {
	certPEM := link.Settings["server_cert"]
	keyPEM := link.Settings["server_key"]
	cert, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		return nil, err
	}
	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}
	if link.Type == model.LinkMTLS {
		pool, err := certPool(link.Settings["ca_cert"])
		if err != nil {
			return nil, err
		}
		cfg.ClientCAs = pool
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return cfg, nil
}

func (s *Service) clientTLSConfig(link model.Link, serverName string) (*tls.Config, error) {
	cfg := &tls.Config{
		ServerName:         serverName,
		InsecureSkipVerify: link.Settings["skip_verify"] == "true",
		MinVersion:         tls.VersionTLS12,
	}
	if link.Settings["ca_cert"] != "" && !cfg.InsecureSkipVerify {
		pool, err := certPool(link.Settings["ca_cert"])
		if err != nil {
			return nil, err
		}
		cfg.RootCAs = pool
	}
	if link.Type == model.LinkMTLS {
		cert, err := tls.X509KeyPair([]byte(link.Settings["client_cert"]), []byte(link.Settings["client_key"]))
		if err != nil {
			return nil, err
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}

func certPool(caPEM string) (*x509.CertPool, error) {
	pool := x509.NewCertPool()
	for {
		block, rest := pem.Decode([]byte(caPEM))
		if block == nil {
			break
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, err
		}
		pool.AddCert(cert)
		caPEM = string(rest)
	}
	return pool, nil
}
