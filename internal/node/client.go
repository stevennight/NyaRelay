package node

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	neturl "net/url"
	"runtime"
	"strings"
	"time"

	"github.com/coder/websocket"

	"nyarelay/internal/shared/model"
)

type client struct {
	baseURL string
	nodeID  string
	token   string
	http    *http.Client
}

const maxControllerJSONBytes = 4 << 20

func newClient(cfg Config) *client {
	return &client{
		baseURL: strings.TrimRight(cfg.ControllerURL, "/"),
		nodeID:  cfg.NodeID,
		token:   cfg.NodeToken,
		http: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (c *client) config(ctx context.Context) (model.SignedConfig, error) {
	var cfg model.SignedConfig
	if err := c.doJSON(ctx, http.MethodGet, "/api/node/config", nil, &cfg); err != nil {
		return model.SignedConfig{}, err
	}
	return cfg, nil
}

func (c *client) metrics(ctx context.Context, report any) error {
	return c.doJSON(ctx, http.MethodPost, "/api/node/metrics", report, nil)
}

func (c *client) connectWS(ctx context.Context) (*websocket.Conn, error) {
	parsed, err := validateControllerURL(c.baseURL)
	if err != nil {
		return nil, err
	}
	switch parsed.Scheme {
	case "http":
		parsed.Scheme = "ws"
	case "https":
		parsed.Scheme = "wss"
	default:
		return nil, fmt.Errorf("unsupported controller scheme %q", parsed.Scheme)
	}
	parsed.Path = "/api/node/ws"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	hdr := http.Header{}
	hdr.Set("X-NyaRelay-Node-ID", c.nodeID)
	hdr.Set("X-NyaRelay-Node-Token", c.token)
	conn, resp, err := websocket.Dial(ctx, parsed.String(), &websocket.DialOptions{HTTPClient: c.http, HTTPHeader: hdr})
	if err != nil {
		if closeErr := closeResponseBody(resp); closeErr != nil {
			return nil, fmt.Errorf("%w; close response body: %v", err, closeErr)
		}
		return nil, err
	}
	return conn, nil
}

func (c *client) doJSON(ctx context.Context, method, path string, body any, dest any) (err error) {
	var reqBody *bytes.Reader
	if body == nil {
		reqBody = bytes.NewReader(nil)
	} else {
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reqBody = bytes.NewReader(payload)
	}
	requestURL, err := controllerURLWithPath(c.baseURL, path)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, requestURL, reqBody)
	if err != nil {
		return err
	}
	req.Header.Set("X-NyaRelay-Node-ID", c.nodeID)
	req.Header.Set("X-NyaRelay-Node-Token", c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := resp.Body.Close(); err == nil {
			err = closeErr
		}
	}()
	if resp.StatusCode >= 300 {
		var apiErr struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, maxControllerJSONBytes)).Decode(&apiErr)
		if apiErr.Error == "" {
			apiErr.Error = resp.Status
		}
		return fmt.Errorf("controller request failed: %s", apiErr.Error)
	}
	if dest == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, maxControllerJSONBytes)).Decode(dest)
}

func validateControllerURL(raw string) (*neturl.URL, error) {
	raw = strings.TrimSpace(raw)
	u, err := neturl.Parse(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("unsupported controller scheme %q", u.Scheme)
	}
	if u.Host == "" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("invalid controller URL")
	}
	if u.Path != "" && u.Path != "/" {
		return nil, fmt.Errorf("controller URL must not include a path")
	}
	if u.Scheme == "http" && !isLoopbackHost(u.Hostname()) {
		return nil, fmt.Errorf("unencrypted controller HTTP is only allowed on loopback")
	}
	u.Path = ""
	u.RawPath = ""
	return u, nil
}

func controllerURLWithPath(raw, path string) (string, error) {
	base, err := validateControllerURL(raw)
	if err != nil {
		return "", err
	}
	pathURL, err := neturl.Parse(path)
	if err != nil || pathURL.IsAbs() || pathURL.Host != "" {
		return "", fmt.Errorf("invalid controller request path")
	}
	base.Path = pathURL.Path
	base.RawPath = pathURL.RawPath
	base.RawQuery = pathURL.RawQuery
	return base.String(), nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func closeResponseBody(resp *http.Response) error {
	if resp == nil || resp.Body == nil {
		return nil
	}
	return resp.Body.Close()
}

func hostname() string {
	name, _ := osHostname()
	return name
}

func nodeSystem() model.NodeSystem {
	return model.NodeSystem{
		Hostname: hostname(),
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
		IP:       firstNonLoopbackIP(),
	}
}

func firstNonLoopbackIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	var fallback string
	for _, addr := range addrs {
		var ip net.IP
		switch value := addr.(type) {
		case *net.IPNet:
			ip = value.IP
		case *net.IPAddr:
			ip = value.IP
		}
		if ip == nil || ip.IsLoopback() || ip.IsMulticast() || ip.IsUnspecified() {
			continue
		}
		if ip4 := ip.To4(); ip4 != nil {
			return ip4.String()
		}
		if fallback == "" {
			fallback = ip.String()
		}
	}
	return fallback
}
