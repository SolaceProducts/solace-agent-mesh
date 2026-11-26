// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

const (
	defaultMaxFrameBytes          = 8 << 20
	defaultMaxInflight            = 16
	defaultMaxConnectionsPerAgent = 8
	defaultMaxConnectsPerMinute   = 60
	defaultTaskTTL                = 2 * time.Hour
	defaultWakeTimeout            = 15 * time.Minute
)

const maxRegistrationField = 256

var agentNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

const maxAgentNameBytes = 128 - len(instanceSuffix)

type Config struct {
	MaxFrameBytes          int64
	MaxInflight            int
	MaxConnectionsPerAgent int
	MaxConnectsPerMinute   int
	TaskTTL                time.Duration
	PlatformManaged        bool
	Agents                 map[string]*AgentConfig
}

type AgentConfig struct {
	Name             string
	TokenSHA256      []byte
	Peers            []string
	Enabled          bool
	Wake             *WakeConfig
	Owner            string
	Team             string
	DisplayName      string
	ShareUserProfile bool
}

type WakeConfig struct {
	URL          string
	TokenURL     string
	ClientID     string
	ClientSecret string
	Scope        string
	Headers      map[string]string
	Timeout      time.Duration
	OnStart      bool
}

func (a *AgentConfig) allowsPeer(name string) bool {
	for _, p := range a.Peers {
		if p == name {
			return true
		}
	}
	return false
}

func ParseConfig(raw map[string]any) (*Config, error) {
	cfg := &Config{
		MaxFrameBytes:          defaultMaxFrameBytes,
		MaxInflight:            defaultMaxInflight,
		MaxConnectionsPerAgent: defaultMaxConnectionsPerAgent,
		MaxConnectsPerMinute:   defaultMaxConnectsPerMinute,
		TaskTTL:                defaultTaskTTL,
		Agents:                 map[string]*AgentConfig{},
	}
	var errs []error
	if v, ok, err := positiveInt(raw, "max_frame_bytes"); err != nil {
		errs = append(errs, err)
	} else if ok {
		cfg.MaxFrameBytes = int64(v)
	}
	if v, ok, err := positiveInt(raw, "max_inflight"); err != nil {
		errs = append(errs, err)
	} else if ok {
		cfg.MaxInflight = v
	}
	if v, ok, err := positiveInt(raw, "max_connections_per_agent"); err != nil {
		errs = append(errs, err)
	} else if ok {
		cfg.MaxConnectionsPerAgent = v
	}
	if v, ok, err := positiveInt(raw, "max_connects_per_minute"); err != nil {
		errs = append(errs, err)
	} else if ok {
		cfg.MaxConnectsPerMinute = v
	}
	if v, ok, err := positiveInt(raw, "task_ttl_seconds"); err != nil {
		errs = append(errs, err)
	} else if ok {
		cfg.TaskTTL = time.Duration(v) * time.Second
	}

	platformManaged, err := optionalBool(raw, "platform_managed")
	if err != nil {
		errs = append(errs, err)
	}
	cfg.PlatformManaged = platformManaged

	agents, _ := raw["agents"].([]any)
	if len(agents) == 0 && !platformManaged {
		errs = append(errs, errors.New("agents: at least one agent is required"))
	}
	tokenOwners := map[string]string{}
	for i, item := range agents {
		m, ok := item.(map[string]any)
		if !ok {
			errs = append(errs, fmt.Errorf("agents[%d]: must be a mapping", i))
			continue
		}
		agent, err := parseAgent(m, platformManaged)
		if err != nil {
			errs = append(errs, fmt.Errorf("agents[%d]: %w", i, err))
			continue
		}
		if _, dup := cfg.Agents[agent.Name]; dup {
			errs = append(errs, fmt.Errorf("agents[%d]: duplicate agent %q", i, agent.Name))
			continue
		}
		if owner, shared := tokenOwners[string(agent.TokenSHA256)]; shared {
			errs = append(errs, fmt.Errorf("agents[%d]: agent %q has the same token_sha256 as agent %q: a token must identify one agent", i, agent.Name, owner))
			continue
		}
		tokenOwners[string(agent.TokenSHA256)] = agent.Name
		cfg.Agents[agent.Name] = agent
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return cfg, nil
}

var agentKeys = []string{"name", "token_sha256", "enabled", "owner", "team", "display_name", "peers", "share_user_profile", "wake"}

var wakeKeys = []string{"url", "token_url", "client_id", "client_secret", "scope", "headers", "timeout_seconds", "on_start"}

func nameIsReserved(name string, platformManaged bool) bool {
	_, kind, minted := a2a.IDFromInstanceName(name)
	return minted && (!platformManaged || kind != a2a.KindAgent)
}

func parseAgent(m map[string]any, platformManaged bool) (*AgentConfig, error) {
	name, ok := m["name"].(string)
	if !ok || !agentNamePattern.MatchString(name) {
		return nil, fmt.Errorf("name %q: letters, digits, '-' and '_', starting with a letter or digit", name)
	}
	if len(name) > maxAgentNameBytes {
		return nil, fmt.Errorf("name %q: longer than %d characters, the longest a card of the link can be listed under", name, maxAgentNameBytes)
	}
	if nameIsReserved(name, platformManaged) {
		return nil, fmt.Errorf("name %q: the agent_<uuid> and workflow_<uuid> names are reserved for agents and workflows the platform deploys", name)
	}
	var errs []error
	problem := func(err error) { errs = append(errs, fmt.Errorf("agent %q: %w", name, err)) }
	for _, key := range unknownKeys(m, agentKeys) {
		problem(fmt.Errorf("unknown key %q", key))
	}
	digest, ok := m["token_sha256"].(string)
	sum, err := hex.DecodeString(strings.TrimSpace(digest))
	if !ok || err != nil || len(sum) != 32 {
		problem(errors.New("token_sha256 must be the 64-character hex SHA-256 of the agent's token"))
		return nil, errors.Join(errs...)
	}
	if emptyDigest := sha256.Sum256(nil); bytes.Equal(sum, emptyDigest[:]) {
		problem(errors.New("token_sha256 is the digest of an empty token"))
		return nil, errors.Join(errs...)
	}
	agent := &AgentConfig{Name: name, TokenSHA256: sum, Enabled: true}
	agent.Enabled, err = requiredBool(m, "enabled", true)
	if err != nil {
		problem(err)
	}
	agent.ShareUserProfile, err = optionalBool(m, "share_user_profile")
	if err != nil {
		problem(err)
	}
	for key, dst := range map[string]*string{"owner": &agent.Owner, "team": &agent.Team, "display_name": &agent.DisplayName} {
		v, typeErr := optionalString(m, key)
		if typeErr != nil {
			problem(typeErr)
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) > maxRegistrationField {
			problem(fmt.Errorf("%s is longer than %d characters", key, maxRegistrationField))
			continue
		}
		*dst = v
	}
	peers, err := parsePeers(name, m)
	if err != nil {
		errs = append(errs, err)
	}
	agent.Peers = peers
	if raw, present := m["wake"]; present && raw != nil {
		w, isMapping := raw.(map[string]any)
		if !isMapping {
			problem(errors.New("wake: must be a mapping"))
		} else if wake, err := parseWake(w); err != nil {
			problem(fmt.Errorf("wake: %w", err))
		} else {
			agent.Wake = wake
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return agent, nil
}

func parsePeers(name string, m map[string]any) ([]string, error) {
	raw, present := m["peers"]
	if !present || raw == nil {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("agent %q: peers: must be a list of agent names", name)
	}
	var peers []string
	for _, p := range items {
		peer, isString := p.(string)
		if !isString || !agentNamePattern.MatchString(peer) || peer == name {
			return nil, fmt.Errorf("agent %q: peer %v is not a valid agent name", name, p)
		}
		peers = append(peers, peer)
	}
	return peers, nil
}

func unknownKeys(m map[string]any, known []string) []string {
	var unknown []string
	for key := range m {
		if !slices.Contains(known, key) {
			unknown = append(unknown, key)
		}
	}
	slices.Sort(unknown)
	return unknown
}

func requiredBool(m map[string]any, key string, def bool) (bool, error) {
	v, present := m[key]
	if !present {
		return def, nil
	}
	b, ok := v.(bool)
	if !ok {
		return def, fmt.Errorf("%s: must be true or false", key)
	}
	return b, nil
}

func optionalBool(m map[string]any, key string) (bool, error) {
	v, present := m[key]
	if !present || v == nil {
		return false, nil
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("%s: must be true or false", key)
	}
	return b, nil
}

func optionalString(m map[string]any, key string) (string, error) {
	v, present := m[key]
	if !present || v == nil {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s: must be a string", key)
	}
	return s, nil
}

func parseWake(m map[string]any) (*WakeConfig, error) {
	if unknown := unknownKeys(m, wakeKeys); len(unknown) > 0 {
		return nil, fmt.Errorf("unknown key %q", unknown[0])
	}
	w := &WakeConfig{Headers: map[string]string{}, Timeout: defaultWakeTimeout}
	for key, dst := range map[string]*string{"url": &w.URL, "token_url": &w.TokenURL, "client_id": &w.ClientID,
		"client_secret": &w.ClientSecret, "scope": &w.Scope} {
		v, err := optionalString(m, key)
		if err != nil {
			return nil, err
		}
		*dst = v
	}
	if err := requireSecureURL("url", w.URL); err != nil {
		return nil, err
	}
	if w.TokenURL != "" || w.ClientID != "" || w.ClientSecret != "" {
		if err := requireSecureURL("token_url", w.TokenURL); err != nil {
			return nil, err
		}
		if w.ClientID == "" || w.ClientSecret == "" {
			return nil, errors.New("client_id and client_secret are required with token_url")
		}
	}
	if raw, present := m["headers"]; present && raw != nil {
		headers, ok := raw.(map[string]any)
		if !ok {
			return nil, errors.New("headers: must be a mapping of header names to strings")
		}
		for k, v := range headers {
			s, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("headers.%s: must be a string", k)
			}
			w.Headers[k] = s
		}
	}
	if v, ok, err := positiveInt(m, "timeout_seconds"); err != nil {
		return nil, err
	} else if ok {
		w.Timeout = time.Duration(v) * time.Second
	}
	onStart, err := requiredBool(m, "on_start", true)
	if err != nil {
		return nil, err
	}
	w.OnStart = onStart
	return w, nil
}

func requireSecureURL(field, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("%s: must be an absolute URL", field)
	}
	if u.Scheme == "https" {
		return nil
	}
	host := u.Hostname()
	if u.Scheme == "http" && (host == "localhost" || net.ParseIP(host).IsLoopback()) {
		return nil
	}
	return fmt.Errorf("%s: must use https", field)
}

func positiveInt(m map[string]any, key string) (int, bool, error) {
	v, present := m[key]
	if !present || v == nil {
		return 0, false, nil
	}
	var n int
	switch x := v.(type) {
	case int:
		n = x
	case int64:
		n = int(x)
	case float64:
		if x != float64(int(x)) {
			return 0, false, fmt.Errorf("%s: must be a whole number", key)
		}
		n = int(x)
	default:
		return 0, false, fmt.Errorf("%s: must be a number", key)
	}
	if n <= 0 {
		return 0, false, fmt.Errorf("%s: must be positive", key)
	}
	return n, true, nil
}
