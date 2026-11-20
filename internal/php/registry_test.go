package php

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	sumA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	sumB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestParseChecksumsPairsCLIAndFPMForArch(t *testing.T) {
	index := strings.Join([]string{
		sumA + "  php-8.5.13-cli-linux-x86_64.tar.gz",
		sumB + " *php-8.5.13-fpm-linux-x86_64.tar.gz",
		sumA + "  php-8.5.13-cli-linux-aarch64.tar.gz",
		sumB + "  php-8.5.13-fpm-linux-aarch64.tar.gz",
		// No FPM archive, so no release.
		sumA + "  php-8.5.12-cli-linux-x86_64.tar.gz",
		// Not a checksum, so the archive it names is not trusted.
		"not-a-sum  php-8.5.11-cli-linux-x86_64.tar.gz",
		sumB + "  php-8.5.11-fpm-linux-x86_64.tar.gz",
		sumA + "  routa_php_xdebug_8.5.13_linux_amd64.tar.gz",
		"",
	}, "\n")

	rels, err := parseChecksums(strings.NewReader(index), "https://example.test/builds", "x86_64")
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 1 {
		t.Fatalf("got %d releases, want 1: %#v", len(rels), rels)
	}
	want := Release{
		Version:   Version{8, 5, 13},
		CLIURL:    "https://example.test/builds/php-8.5.13-cli-linux-x86_64.tar.gz",
		FPMURL:    "https://example.test/builds/php-8.5.13-fpm-linux-x86_64.tar.gz",
		CLISHA256: sumA,
		FPMSHA256: sumB,
		Source:    SourceRouta,
	}
	if rels[0] != want {
		t.Fatalf("release = %#v\nwant %#v", rels[0], want)
	}
}

func TestMergeReleasesPrefersFirstListAndSorts(t *testing.T) {
	own := []Release{{Version: Version{8, 5, 8}, Source: SourceRouta}, {Version: Version{8, 5, 13}, Source: SourceRouta}}
	static := []Release{{Version: Version{8, 4, 20}, Source: SourceStaticPHP}, {Version: Version{8, 5, 8}, Source: SourceStaticPHP}}

	got := mergeReleases(own, static)
	want := []Release{
		{Version: Version{8, 4, 20}, Source: SourceStaticPHP},
		{Version: Version{8, 5, 8}, Source: SourceRouta},
		{Version: Version{8, 5, 13}, Source: SourceRouta},
	}
	if len(got) != len(want) {
		t.Fatalf("got %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("release %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}

// fakeSources serves a routa SHA256SUMS and a static-php.dev listing. A nil
// value for either makes that source answer 404.
func fakeSources(t *testing.T, sums []string, listing []string) {
	t.Helper()
	arch, err := staticPHPArch()
	if err != nil {
		t.Skip(err)
	}
	routa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sums == nil || r.URL.Path != "/"+routaBuildsIndex {
			http.NotFound(w, r)
			return
		}
		for _, line := range sums {
			fmt.Fprintln(w, strings.ReplaceAll(line, "ARCH", arch))
		}
	}))
	t.Cleanup(routa.Close)
	static := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if listing == nil {
			http.NotFound(w, r)
			return
		}
		var entries []string
		for _, name := range listing {
			entries = append(entries, fmt.Sprintf(`{"name":%q,"is_dir":false}`, strings.ReplaceAll(name, "ARCH", arch)))
		}
		fmt.Fprintf(w, "[%s]", strings.Join(entries, ","))
	}))
	t.Cleanup(static.Close)

	t.Setenv("ROUTA_PHP_BUILDS_BASE_URL", routa.URL)
	oldList, oldBase := bulkListURL, bulkBaseURL
	bulkListURL, bulkBaseURL = static.URL+"/?format=json", static.URL
	t.Cleanup(func() { bulkListURL, bulkBaseURL = oldList, oldBase })
}

func TestFetchReleasesMergesBothSources(t *testing.T) {
	fakeSources(t,
		[]string{sumA + "  php-8.5.13-cli-linux-ARCH.tar.gz", sumB + "  php-8.5.13-fpm-linux-ARCH.tar.gz"},
		[]string{"php-8.5.8-cli-linux-ARCH.tar.gz", "php-8.5.8-fpm-linux-ARCH.tar.gz"},
	)
	var warn bytes.Buffer
	rels, err := FetchReleases(context.Background(), &warn)
	if err != nil {
		t.Fatal(err)
	}
	if warn.Len() != 0 {
		t.Fatalf("unexpected warning: %s", warn.String())
	}
	if len(rels) != 2 || rels[0].Source != SourceStaticPHP || rels[1].Source != SourceRouta {
		t.Fatalf("releases = %#v", rels)
	}
	latest, err := Resolve("8.5", rels)
	if err != nil {
		t.Fatal(err)
	}
	if latest.Version != (Version{8, 5, 13}) || latest.CLISHA256 != sumA {
		t.Fatalf("8.5 resolved to %#v", latest)
	}
}

func TestFetchReleasesWarnsAndFallsBackWhenRoutaBuildsAreUnreachable(t *testing.T) {
	fakeSources(t, nil, []string{"php-8.5.8-cli-linux-ARCH.tar.gz", "php-8.5.8-fpm-linux-ARCH.tar.gz"})
	var warn bytes.Buffer
	rels, err := FetchReleases(context.Background(), &warn)
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 1 || rels[0].Version != (Version{8, 5, 8}) {
		t.Fatalf("releases = %#v", rels)
	}
	if !strings.Contains(warn.String(), "could not read routa's PHP builds") {
		t.Fatalf("missing warning, got %q", warn.String())
	}
}

func TestFetchReleasesIgnoresAMissingStaticPHPChannel(t *testing.T) {
	fakeSources(t, []string{sumA + "  php-8.5.13-cli-linux-ARCH.tar.gz", sumB + "  php-8.5.13-fpm-linux-ARCH.tar.gz"}, nil)
	var warn bytes.Buffer
	rels, err := FetchReleases(context.Background(), &warn)
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 1 || warn.Len() != 0 {
		t.Fatalf("releases = %#v, warning = %q", rels, warn.String())
	}
}

func TestFetchReleasesErrorsWhenNoSourceAnswers(t *testing.T) {
	fakeSources(t, nil, nil)
	if _, err := FetchReleases(context.Background(), nil); err == nil {
		t.Fatal("expected an error when neither source answers")
	}
}
