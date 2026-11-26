// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samapp

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
)

func CreateBrokerFromConfig(brokerCfg map[string]any, logger *slog.Logger, clientName string) (broker.Broker, error) {
	if isDevMode(brokerCfg) {
		b, err := createDevBroker(logger, clientName)
		if err != nil {
			return nil, err
		}
		return observability.NewInstrumentedBroker(b), nil
	}

	brokerURL := getString(brokerCfg, "broker_url")
	if brokerURL == "" {
		return nil, fmt.Errorf("broker_url is required when dev_mode is not enabled")
	}

	logger.Info("using Solace broker", "url", brokerURL)
	solaceCfg := broker.SolaceBrokerConfig{
		BrokerURL:      brokerURL,
		VPN:            getStringDefault(brokerCfg, "broker_vpn", "default"),
		Username:       getStringDefault(brokerCfg, "broker_username", "default"),
		Password:       getString(brokerCfg, "broker_password"),
		TrustStorePath: getString(brokerCfg, "trust_store_path"),
		TLSSkipVerify:  getBool(brokerCfg, "tls_skip_verify"),
	}
	applyReconnectionConfig(&solaceCfg, brokerCfg, logger)
	solace := broker.NewSolaceBroker(solaceCfg, logger)
	return observability.NewInstrumentedBroker(solace), nil
}

func CreateBroker(logger *slog.Logger, clientName string) (broker.Broker, error) {
	brokerCfg := map[string]any{}

	if url := os.Getenv("SOLACE_BROKER_URL"); url != "" {
		brokerCfg["broker_url"] = url
		brokerCfg["broker_vpn"] = EnvOrDefault("SOLACE_BROKER_VPN", "default")
		brokerCfg["broker_username"] = EnvOrDefault("SOLACE_BROKER_USERNAME", "default")
		brokerCfg["broker_password"] = os.Getenv("SOLACE_BROKER_PASSWORD")
		brokerCfg["dev_mode"] = false
	} else {
		brokerCfg["dev_mode"] = true
	}

	return CreateBrokerFromConfig(brokerCfg, logger, clientName)
}

func isDevMode(brokerCfg map[string]any) bool {
	if len(brokerCfg) == 0 {
		return true
	}

	devMode, exists := brokerCfg["dev_mode"]
	if !exists {
		if url := getString(brokerCfg, "broker_url"); url != "" {
			return false
		}
		return true
	}

	switch v := devMode.(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(v, "true")
	default:
		return false
	}
}

func createDevBroker(logger *slog.Logger, clientName string) (broker.Broker, error) {
	host := os.Getenv("SAM_DEV_BROKER_HOST")
	port := os.Getenv("SAM_DEV_BROKER_PORT")

	if host != "" && port != "" {
		addr := host + ":" + port
		clientID := clientName + "-" + idgen.NewRandom()[:8]
		logger.Info("using network dev broker", "addr", addr, "clientID", clientID)
		return broker.NewNetworkDevBroker(addr, clientID, clientID, logger), nil
	}

	return nil, fmt.Errorf("dev mode enabled but no dev broker available: set SAM_DEV_BROKER_HOST and SAM_DEV_BROKER_PORT to a running dev broker, or set SOLACE_BROKER_URL for a Solace broker. When SAM runs in embedded mode its dev broker port is chosen at startup and logged as \"dev broker server started\" with its addr — point SAM_DEV_BROKER_HOST/PORT at that address")
}

func IsDevMode(brokerCfg map[string]any) bool {
	return isDevMode(brokerCfg)
}

func getString(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func getStringDefault(m map[string]any, key, def string) string {
	if v := getString(m, key); v != "" {
		return v
	}
	return def
}

func getBool(m map[string]any, key string) bool {
	switch v := m[key].(type) {
	case bool:
		return v
	case string:
		b, _ := strconv.ParseBool(v)
		return b
	default:
		return false
	}
}

func intField(m map[string]any, key string, logger *slog.Logger) (int, bool) {
	raw, present := m[key]
	if !present {
		return 0, false
	}
	switch v := raw.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n, true
		}
	}
	logger.Warn("ignoring non-numeric broker config value; keeping default",
		"key", key, "value", raw)
	return 0, false
}

func applyReconnectionConfig(cfg *broker.SolaceBrokerConfig, brokerCfg map[string]any, logger *slog.Logger) {
	switch strategy := getString(brokerCfg, "reconnection_strategy"); strategy {
	case "", "forever_retry":
		cfg.ReconnectForever = true
	case "parametrized_retry":
		cfg.ReconnectForever = false
	default:
		cfg.ReconnectForever = true
		logger.Warn("unrecognized reconnection_strategy; using forever_retry",
			"reconnectionStrategy", strategy)
	}
	if n, ok := intField(brokerCfg, "retry_count", logger); ok && n > 0 {
		if cfg.ReconnectForever {
			logger.Warn("retry_count is ignored under reconnection_strategy=forever_retry; set reconnection_strategy: parametrized_retry to bound retries",
				"retryCount", n)
		} else {
			cfg.ReconnectRetries = n
		}
	}
	if ms, ok := intField(brokerCfg, "retry_interval", logger); ok && ms > 0 {
		cfg.ReconnectDelay = time.Duration(ms) * time.Millisecond
	}
}

func EnvOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func FirstBrokerConfig(entries []AppEntry) map[string]any {
	for _, e := range entries {
		if e.BrokerConfig != nil {
			return e.BrokerConfig
		}
	}
	return nil
}

func ResolveBrokerConfig(entries []AppEntry, fullCfg config.Config, logger *slog.Logger) map[string]any {
	if bc := FirstBrokerConfig(entries); bc != nil {
		if _, hasTopLevel := fullCfg.GetRaw("broker").(map[string]any); hasTopLevel {
			logger.Warn("top-level broker: block ignored because an app declares its own broker block; broker config is not merged — move shared tuning into the app's broker block")
		}
		return bc
	}
	return ExtractBrokerConfigFromRaw(fullCfg)
}

func ExtractBrokerConfigFromRaw(cfg config.Config) map[string]any {
	if apps := cfg.GetRaw("apps"); apps != nil {
		if list, ok := apps.([]any); ok && len(list) > 0 {
			if first, ok := list[0].(map[string]any); ok {
				if bc, ok := first["broker"].(map[string]any); ok {
					return bc
				}
			}
		}
	}
	if bc, ok := cfg.GetRaw("broker").(map[string]any); ok {
		return bc
	}
	return nil
}
