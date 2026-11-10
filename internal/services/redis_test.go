package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderRedisUnit(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	unit, err := RenderRedisUnit("/usr/bin/redis-server")
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"Description=routa Redis",
		"ExecStart=/usr/bin/redis-server " + filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "routa", "services", "redis", "redis.conf"),
		"Restart=on-failure",
		"WantedBy=default.target",
	} {
		if !strings.Contains(unit, want) {
			t.Fatalf("Redis unit missing %q:\n%s", want, unit)
		}
	}
}

func TestRenderRedisConfig(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	conf, err := RenderRedisConfig()
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"bind 127.0.0.1 ::1",
		"protected-mode yes",
		"port " + RedisDefaultPort,
		"daemonize no",
		"dir " + filepath.Join(os.Getenv("XDG_DATA_HOME"), "routa", "services", "redis"),
		"appendonly yes",
		"pidfile " + filepath.Join(os.Getenv("XDG_STATE_HOME"), "routa", "run", "redis.pid"),
	} {
		if !strings.Contains(conf, want) {
			t.Fatalf("Redis config missing %q:\n%s", want, conf)
		}
	}
}

func TestRenderRedisConfigWithCustomPort(t *testing.T) {
	conf, err := RenderRedisConfigWithPort("6380")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(conf, "port 6380") {
		t.Fatalf("Redis config missing custom port:\n%s", conf)
	}
}

func TestRedisConfiguredPortReadsConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := os.MkdirAll(filepath.Dir(RedisConfigPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(RedisConfigPath(), []byte("port 6380\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := RedisConfiguredPort()
	if err != nil {
		t.Fatal(err)
	}
	if got != "6380" {
		t.Fatalf("port = %q, want 6380", got)
	}
}

func TestEnsureWithBinaryWritesRedisFiles(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	def := Redis()
	def.WriteConfig = WriteRedisConfig
	if err := WriteFiles(def, "/usr/bin/redis-server"); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		RedisConfigPath(),
		filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "systemd", "user", RedisUnitName),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s: %v", path, err)
		}
	}
}

func TestRedisConfigOffersDatabasesForWorkspaces(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if got := RedisDatabaseCount("6380"); got != 16 {
		t.Fatalf("without a routa config: %d databases, want Redis's default 16", got)
	}
	if err := WriteRedisConfigWithPort("6380"); err != nil {
		t.Fatal(err)
	}
	if got := RedisDatabaseCount("6380"); got != RedisDatabases {
		t.Fatalf("routa's Redis: %d databases, want %d", got, RedisDatabases)
	}
	if got := RedisDatabaseCount("6379"); got != 16 {
		t.Fatalf("another server: %d databases, want 16", got)
	}
}
