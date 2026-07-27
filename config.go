package main

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// settings are the endpoint and token after flag, environment, and config file
// have been merged, in that order of precedence.
type settings struct {
	endpoint string
	token    string
	warnings []string
}

// resolve merges the three sources. The config file is only read when a flag
// or environment variable has not already supplied both values.
func (g *Globals) resolve() (*settings, error) {
	s := &settings{endpoint: g.Endpoint, token: g.Token}

	if s.endpoint == "" || s.token == "" {
		path, explicit := configPath(g.Config)
		if path != "" {
			file, warnings, err := loadConfigFile(path, explicit)
			if err != nil {
				return nil, err
			}

			s.warnings = warnings

			if file != nil {
				if s.endpoint == "" {
					s.endpoint = file.endpoint
				}

				if s.token == "" {
					s.token = file.token
				}
			}
		}
	}

	if s.endpoint == "" {
		return nil, usageError("no endpoint configured: pass --endpoint, set ARTIFACT_ENDPOINT, or add endpoint to the config file")
	}

	if s.token == "" {
		return nil, usageError("no token configured: pass --token, set ARTIFACT_TOKEN, or add token to the config file")
	}

	parsed, err := url.Parse(s.endpoint)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, usageError(fmt.Sprintf("endpoint must be an absolute http(s) URL, got %q", s.endpoint))
	}

	return s, nil
}

// configPath returns the file to read and whether the user named it. A missing
// file is only an error when it was named explicitly.
func configPath(flag string) (path string, explicit bool) {
	if flag != "" {
		return flag, true
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}

	return filepath.Join(home, ".config", "artifact", "config.toml"), false
}

type fileConfig struct {
	endpoint string
	token    string
}

// loadConfigFile reads the two-key subset of TOML the config file needs. A
// file holding a token must be 0600; anything looser is a warning, not a
// refusal, so a wrong mode never blocks an upload.
func loadConfigFile(path string, explicit bool) (*fileConfig, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) && !explicit {
			return nil, nil, nil
		}

		return nil, nil, fmt.Errorf("read config: %w", err)
	}

	cfg := parseConfig(string(data))

	var warnings []string

	if cfg.token != "" {
		if info, err := os.Stat(path); err == nil && info.Mode().Perm()&0o077 != 0 {
			warnings = append(warnings, fmt.Sprintf(
				"warning: %s contains a token but is mode %04o; run: chmod 600 %s",
				path, info.Mode().Perm(), path))
		}
	}

	return &cfg, warnings, nil
}

func parseConfig(data string) fileConfig {
	var cfg fileConfig

	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}

		switch strings.TrimSpace(key) {
		case "endpoint":
			cfg.endpoint = unquote(strings.TrimSpace(value))
		case "token":
			cfg.token = unquote(strings.TrimSpace(value))
		}
	}

	return cfg
}

func unquote(s string) string {
	if len(s) >= 2 {
		if q := s[0]; (q == '"' || q == '\'') && s[len(s)-1] == q {
			return s[1 : len(s)-1]
		}
	}

	return s
}
