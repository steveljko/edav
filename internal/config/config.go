// Package config loads server configuration from the environment.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
)

const minAdminPasswordLen = 8

// Config is the fully validated server configuration.
type Config struct {
	Addr     string
	DBPath   string
	BaseURL  string
	LogLevel slog.Level

	AdminUsername string
	AdminPassword string

	CalDAVEnabled  bool
	CardDAVEnabled bool
	WebDAVEnabled  bool
}

// Load reads configuration from the environment, applying defaults. It reports
// every problem it finds rather than stopping at the first, so a misconfigured
// deployment can be fixed in one pass.
func Load(getenv func(string) string) (*Config, error) {
	if getenv == nil {
		getenv = os.Getenv
	}

	var errs []error
	fail := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf(format, args...))
	}

	cfg := &Config{
		Addr:          str(getenv, "EDAV_ADDR", ":8080"),
		DBPath:        str(getenv, "EDAV_DB_PATH", "edav.db"),
		BaseURL:       strings.TrimRight(str(getenv, "EDAV_BASE_URL", ""), "/"),
		AdminUsername: str(getenv, "EDAV_ADMIN_USERNAME", "admin"),
		AdminPassword: getenv("EDAV_ADMIN_PASSWORD"),
	}

	if _, _, err := net.SplitHostPort(cfg.Addr); err != nil {
		fail("EDAV_ADDR %q is not a host:port address: %w", cfg.Addr, err)
	}
	if cfg.DBPath == "" {
		fail("EDAV_DB_PATH must not be empty")
	}
	if cfg.BaseURL != "" {
		u, err := url.Parse(cfg.BaseURL)
		switch {
		case err != nil:
			fail("EDAV_BASE_URL %q is not a valid URL: %w", cfg.BaseURL, err)
		case u.Scheme != "http" && u.Scheme != "https":
			fail("EDAV_BASE_URL %q must be an absolute http or https URL", cfg.BaseURL)
		case u.Host == "":
			fail("EDAV_BASE_URL %q is missing a host", cfg.BaseURL)
		}
	}
	if cfg.AdminUsername == "" {
		fail("EDAV_ADMIN_USERNAME must not be empty")
	}
	switch {
	case cfg.AdminPassword == "":
		fail("EDAV_ADMIN_PASSWORD is required")
	case len(cfg.AdminPassword) < minAdminPasswordLen:
		fail("EDAV_ADMIN_PASSWORD must be at least %d characters", minAdminPasswordLen)
	}

	level, err := logLevel(str(getenv, "EDAV_LOG_LEVEL", "info"))
	if err != nil {
		fail("EDAV_LOG_LEVEL: %w", err)
	}
	cfg.LogLevel = level

	for _, f := range []struct {
		key  string
		def  bool
		dest *bool
	}{
		{"EDAV_CALDAV_ENABLED", true, &cfg.CalDAVEnabled},
		{"EDAV_CARDDAV_ENABLED", true, &cfg.CardDAVEnabled},
		{"EDAV_WEBDAV_ENABLED", false, &cfg.WebDAVEnabled},
	} {
		v, err := boolean(getenv, f.key, f.def)
		if err != nil {
			fail("%s: %w", f.key, err)
		}
		*f.dest = v
	}

	if len(errs) > 0 {
		return nil, fmt.Errorf("invalid configuration: %w", errors.Join(errs...))
	}
	return cfg, nil
}

func str(getenv func(string) string, key, def string) string {
	if v := getenv(key); v != "" {
		return v
	}
	return def
}

func boolean(getenv func(string) string, key string, def bool) (bool, error) {
	v := getenv(key)
	if v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def, fmt.Errorf("%q is not a boolean: %w", v, err)
	}
	return b, nil
}

func logLevel(s string) (slog.Level, error) {
	var l slog.Level
	if err := l.UnmarshalText([]byte(s)); err != nil {
		return slog.LevelInfo, fmt.Errorf("%q is not a known level: %w", s, err)
	}
	return l, nil
}
