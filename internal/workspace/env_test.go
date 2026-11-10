package workspace

import (
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
	if got, err := CopyName("aff_main", "fix-login"); err != nil || got != "aff_main_fix_login" {
		t.Fatalf("CopyName = %q, %v", got, err)
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
