package workspace

import (
	"os"
	"strings"
	"testing"
)

func TestParseEnvHandlesQuotesCommentsAndExport(t *testing.T) {
	env := ParseEnv(strings.Join([]string{
		"# comment",
		"APP_NAME=\"My App\"",
		"export DB_DATABASE=shop",
		"DB_PASSWORD='p#ss'",
		"DB_HOST=127.0.0.1 # local",
		"EMPTY=",
		"",
	}, "\n"))
	want := map[string]string{
		"APP_NAME":    "My App",
		"DB_DATABASE": "shop",
		"DB_PASSWORD": "p#ss",
		"DB_HOST":     "127.0.0.1",
		"EMPTY":       "",
	}
	for k, v := range want {
		if env[k] != v {
			t.Errorf("%s = %q, want %q", k, env[k], v)
		}
	}
}

func TestReplaceHostRenamesTheSiteAndItsSubdomainsOnly(t *testing.T) {
	cases := map[string]string{
		"https://shop.test":                  "https://shop-5194.test",
		"app.shop.test":                      "app.shop-5194.test",
		".shop.test":                         ".shop-5194.test",
		"shop.test,api.shop.test":            "shop-5194.test,api.shop-5194.test",
		"https://shop.test/path?x=shop.test": "https://shop-5194.test/path?x=shop-5194.test",
		"my-shop.test":                       "my-shop.test",
		"shop.testing":                       "shop.testing",
		"workshop.test":                      "workshop.test",
		"outclick.test":                      "outclick.test",
	}
	for in, want := range cases {
		if got := ReplaceHost(in, "shop", "shop-5194"); got != want {
			t.Errorf("ReplaceHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRewriteEnvRenamesHostsSetsKeysAndKeepsComments(t *testing.T) {
	in := strings.Join([]string{
		"APP_URL=https://shop.test",
		"SESSION_DOMAIN=.shop.test",
		"#AWS_URL=https://minio.shop.test",
		"DB_DATABASE=shop",
		"",
	}, "\n")
	got := RewriteEnv(in, "shop", "shop-5194", map[string]string{"DB_DATABASE": "shop_5194", "NEW_KEY": "a b"})
	want := strings.Join([]string{
		"APP_URL=https://shop-5194.test",
		"SESSION_DOMAIN=.shop-5194.test",
		"#AWS_URL=https://minio.shop.test",
		"DB_DATABASE=shop_5194",
		`NEW_KEY="a b"`,
		"",
	}, "\n")
	if got != want {
		t.Fatalf("RewriteEnv =\n%s\nwant\n%s", got, want)
	}
}

func TestDatabaseFromEnvDefaults(t *testing.T) {
	d := DatabaseFromEnv(map[string]string{"DB_CONNECTION": "pgsql", "DB_HOST": "localhost", "DB_DATABASE": "shop"})
	if d.Host != "127.0.0.1" || d.Port != "5432" || d.Engine() != "postgres" {
		t.Fatalf("database = %#v", d)
	}
	if d := DatabaseFromEnv(map[string]string{"DB_CONNECTION": "mysql"}); d.Port != "3306" || d.Engine() != "mysql" {
		t.Fatalf("database = %#v", d)
	}
}

func TestCopyName(t *testing.T) {
	for parent, want := range map[string]string{"aff_main": "aff_fix_login", "shop": "shop_fix_login", "main": "main_fix_login"} {
		if got, err := CopyName(parent, "fix-login"); err != nil || got != want {
			t.Errorf("CopyName(%q) = %q, %v; want %q", parent, got, err, want)
		}
	}
	if _, err := CopyName(strings.Repeat("d", 60), "long-name"); err == nil {
		t.Fatal("expected an error for a name over 63 characters")
	}
}

func TestQuotedIdentifiers(t *testing.T) {
	if got := mysqlIdent("we`ird"); got != "`we``ird`" {
		t.Fatalf("mysqlIdent = %s", got)
	}
	if got := postgresIdent(`we"ird`); got != `"we""ird"` {
		t.Fatalf("postgresIdent = %s", got)
	}
}

func TestProjectDatabaseFor(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		env  map[string]string
		kind string
		file string
	}{
		{"server", map[string]string{"DB_CONNECTION": "mysql", "DB_DATABASE": "shop"}, "server", ""},
		{"url wins", map[string]string{"DB_CONNECTION": "mysql", "DB_URL": "mysql://u@h/shop"}, "url", ""},
		{"sqlite default file", map[string]string{"DB_CONNECTION": "sqlite"}, "sqlite", dir + "/database/database.sqlite"},
		{"sqlite relative", map[string]string{"DB_CONNECTION": "sqlite", "DB_DATABASE": "data/app.db"}, "sqlite", dir + "/data/app.db"},
		{"sqlite absolute", map[string]string{"DB_CONNECTION": "sqlite", "DB_DATABASE": "/srv/app.db"}, "sqlite", "/srv/app.db"},
		{"sqlite in memory", map[string]string{"DB_CONNECTION": "sqlite", "DB_DATABASE": ":memory:"}, "none", ""},
		{"no settings", map[string]string{"APP_NAME": "shop"}, "none", ""},
		{"settings without connection", map[string]string{"DB_DATABASE": "shop"}, "unknown", ""},
		{"unsupported", map[string]string{"DB_CONNECTION": "sqlsrv"}, "unknown", ""},
	}
	for _, c := range cases {
		p := ProjectDatabaseFor(dir, c.env)
		if p.Kind != c.kind || p.File != c.file {
			t.Errorf("%s: kind=%q file=%q, want %q %q", c.name, p.Kind, p.File, c.kind, c.file)
		}
	}
}

func TestProjectDatabaseForUsesLaravelDefaultSQLiteFile(t *testing.T) {
	dir := t.TempDir()
	file := dir + "/database/database.sqlite"
	if err := os.MkdirAll(dir+"/database", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if p := ProjectDatabaseFor(dir, map[string]string{"APP_NAME": "shop"}); p.Kind != "sqlite" || p.File != file {
		t.Fatalf("database = %#v", p)
	}
}

func TestProjectDatabaseSame(t *testing.T) {
	main := ProjectDatabaseFor("/code/shop", map[string]string{"DB_CONNECTION": "mysql", "DB_PORT": "3310", "DB_DATABASE": "shop"})
	same := ProjectDatabaseFor("/code/shop-1", map[string]string{"DB_CONNECTION": "mysql", "DB_HOST": "localhost", "DB_PORT": "3310", "DB_DATABASE": "shop"})
	own := ProjectDatabaseFor("/code/shop-1", map[string]string{"DB_CONNECTION": "mysql", "DB_PORT": "3310", "DB_DATABASE": "shop_1"})
	otherServer := ProjectDatabaseFor("/code/shop-1", map[string]string{"DB_CONNECTION": "mysql", "DB_PORT": "3311", "DB_DATABASE": "shop"})
	if !main.Same(same) || main.Same(own) || main.Same(otherServer) || !main.SameServer(own) {
		t.Fatal("server comparison is wrong")
	}
	sqliteMain := ProjectDatabaseFor("/code/shop", map[string]string{"DB_CONNECTION": "sqlite"})
	sqliteCopy := ProjectDatabaseFor("/code/shop-1", map[string]string{"DB_CONNECTION": "sqlite"})
	sqliteShared := ProjectDatabaseFor("/code/shop-1", map[string]string{"DB_CONNECTION": "sqlite", "DB_DATABASE": "/code/shop/database/database.sqlite"})
	if sqliteMain.Same(sqliteCopy) || !sqliteMain.Same(sqliteShared) {
		t.Fatal("sqlite comparison is wrong")
	}
}
