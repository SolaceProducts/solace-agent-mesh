// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samapp

import (
	"fmt"
	"os"
	"strings"
)

func ResolveGatewayCertFiles(apps []AppEntry) []string {
	var paths []string
	seen := map[string]bool{}
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		paths = append(paths, p)
	}
	for _, a := range apps {
		s, _ := a.AppConfig["ssl_certfile"].(string)
		add(s)
	}
	if len(paths) == 0 {
		add(os.Getenv("SSL_CERTFILE"))
	}
	return paths
}

type HTTPAdapterConfig struct {
	Host           string
	Port           int
	HTTPSPort      int
	SSLCertFile    string
	SSLKeyFile     string
	SSLKeyPassword string
}

func FirstHTTPAdapterConfig(entries []AppEntry) *HTTPAdapterConfig {
	for _, e := range entries {
		if _, ok := e.AppConfig["fastapi_port"]; !ok {
			continue
		}
		return &HTTPAdapterConfig{
			Host:           getString(e.AppConfig, "fastapi_host"),
			Port:           getAppInt(e.AppConfig, "fastapi_port"),
			HTTPSPort:      getAppInt(e.AppConfig, "fastapi_https_port"),
			SSLCertFile:    getString(e.AppConfig, "ssl_certfile"),
			SSLKeyFile:     getString(e.AppConfig, "ssl_keyfile"),
			SSLKeyPassword: getString(e.AppConfig, "ssl_keyfile_password"),
		}
	}
	return nil
}

func (c *HTTPAdapterConfig) ListenAddr() string {
	host := c.Host
	if host == "" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("%s:%d", host, c.Port)
}

func (c *HTTPAdapterConfig) HTTPSAddr() string {
	if c.HTTPSPort <= 0 {
		return ""
	}
	host := c.Host
	if host == "" {
		host = "0.0.0.0"
	}
	return fmt.Sprintf("%s:%d", host, c.HTTPSPort)
}

func ResolveListenAddr(flagAddr string, httpCfg *HTTPAdapterConfig) string {
	if flagAddr != "" {
		return flagAddr
	}
	if httpCfg != nil {
		return httpCfg.ListenAddr()
	}
	return ":8080"
}

func ResolveProxyTLS(httpCfg *HTTPAdapterConfig) (certFile, keyFile, keyPassword, tlsAddr string) {
	if httpCfg != nil && httpCfg.SSLCertFile != "" && httpCfg.SSLKeyFile != "" {
		return httpCfg.SSLCertFile, httpCfg.SSLKeyFile, httpCfg.SSLKeyPassword, httpCfg.HTTPSAddr()
	}
	certFile = os.Getenv("SSL_CERTFILE")
	keyFile = os.Getenv("SSL_KEYFILE")
	if certFile == "" || keyFile == "" {
		return "", "", "", ""
	}
	keyPassword = os.Getenv("SSL_KEYFILE_PASSWORD")
	if p := os.Getenv("FASTAPI_HTTPS_PORT"); p != "" {
		tlsAddr = "0.0.0.0:" + p
	}
	return certFile, keyFile, keyPassword, tlsAddr
}

func getAppInt(m map[string]any, key string) int {
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	}
	return 0
}
