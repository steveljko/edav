package config

import (
	"log/slog"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(env(map[string]string{"EDAV_ADMIN_PASSWORD": "hunter2hunter2"}))
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}

	want := Config{
		Addr:           ":8080",
		DBPath:         "edav.db",
		LogLevel:       slog.LevelInfo,
		AdminUsername:  "admin",
		AdminPassword:  "hunter2hunter2",
		CalDAVEnabled:  true,
		CardDAVEnabled: true,
		WebDAVEnabled:  false,
	}
	if *cfg != want {
		t.Errorf("Load() = %+v, want %+v", *cfg, want)
	}
}

func TestLoadOverrides(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"EDAV_ADDR":            "127.0.0.1:9000",
		"EDAV_DB_PATH":         "/var/lib/edav/db.sqlite",
		"EDAV_BASE_URL":        "https://dav.example.com/",
		"EDAV_LOG_LEVEL":       "debug",
		"EDAV_ADMIN_USERNAME":  "root",
		"EDAV_ADMIN_PASSWORD":  "correct horse battery",
		"EDAV_CALDAV_ENABLED":  "false",
		"EDAV_CARDDAV_ENABLED": "0",
		"EDAV_WEBDAV_ENABLED":  "true",
	}))
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}

	want := Config{
		Addr:           "127.0.0.1:9000",
		DBPath:         "/var/lib/edav/db.sqlite",
		BaseURL:        "https://dav.example.com",
		LogLevel:       slog.LevelDebug,
		AdminUsername:  "root",
		AdminPassword:  "correct horse battery",
		CalDAVEnabled:  false,
		CardDAVEnabled: false,
		WebDAVEnabled:  true,
	}
	if *cfg != want {
		t.Errorf("Load() = %+v, want %+v", *cfg, want)
	}
}

func TestLoadInvalid(t *testing.T) {
	tests := []struct {
		name    string
		set     map[string]string
		wantErr string
	}{
		{"missing admin password", nil, "EDAV_ADMIN_PASSWORD is required"},
		{"short admin password", map[string]string{"EDAV_ADMIN_PASSWORD": "short"}, "at least 8 characters"},
		{"addr without port", map[string]string{"EDAV_ADDR": "localhost"}, "is not a host:port address"},
		{"relative base url", map[string]string{"EDAV_BASE_URL": "/dav"}, "must be an absolute http or https URL"},
		{"base url without host", map[string]string{"EDAV_BASE_URL": "https://"}, "missing a host"},
		{"bad log level", map[string]string{"EDAV_LOG_LEVEL": "chatty"}, "is not a known level"},
		{"bad bool", map[string]string{"EDAV_CALDAV_ENABLED": "yes please"}, "is not a boolean"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := map[string]string{}
			if tt.name != "missing admin password" {
				m["EDAV_ADMIN_PASSWORD"] = "hunter2hunter2"
			}
			for k, v := range tt.set {
				m[k] = v
			}

			_, err := Load(env(m))
			if err == nil {
				t.Fatalf("Load() = nil, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Load() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadReportsAllProblems(t *testing.T) {
	_, err := Load(env(map[string]string{
		"EDAV_ADDR":      "localhost",
		"EDAV_LOG_LEVEL": "chatty",
	}))
	if err == nil {
		t.Fatal("Load() = nil, want error")
	}

	for _, want := range []string{"EDAV_ADDR", "EDAV_LOG_LEVEL", "EDAV_ADMIN_PASSWORD"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Load() error %v does not mention %s", err, want)
		}
	}
}
