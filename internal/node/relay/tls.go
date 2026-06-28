package relay

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"

	"nyarelay/internal/shared/model"
)

func (s *Service) serverTLSConfig(tunnel model.TunnelRuntime, node model.TunnelRuntimeNode) (*tls.Config, error) {
	certPEM := node.Settings["server_cert"]
	keyPEM := node.Settings["server_key"]
	cert, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		return nil, err
	}
	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}
	if tunnel.Transport == model.TunnelTransportMTLS {
		pool, err := certPool(node.Settings["ca_cert"])
		if err != nil {
			return nil, err
		}
		cfg.ClientCAs = pool
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return cfg, nil
}

func (s *Service) clientTLSConfig(tunnel model.TunnelRuntime, node model.TunnelRuntimeNode, serverName string) (*tls.Config, error) {
	cfg := &tls.Config{
		ServerName:         serverName,
		InsecureSkipVerify: node.Settings["skip_verify"] == "true",
		MinVersion:         tls.VersionTLS12,
	}
	if node.Settings["ca_cert"] != "" && !cfg.InsecureSkipVerify {
		pool, err := certPool(node.Settings["ca_cert"])
		if err != nil {
			return nil, err
		}
		cfg.RootCAs = pool
	}
	if tunnel.Transport == model.TunnelTransportMTLS {
		cert, err := tls.X509KeyPair([]byte(node.Settings["client_cert"]), []byte(node.Settings["client_key"]))
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
