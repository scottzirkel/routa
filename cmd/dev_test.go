package cmd

import (
	"os"
	"strings"
	"testing"

	"github.com/scottzirkel/routa/internal/site"
)

func TestStreamDevOutputDetectsCommonLocalURLs(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "dev-output-*")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()

	ports := make(chan int, 8)
	input := strings.Join([]string{
		"Local:   http://localhost:5173/",
		"Network: http://127.0.0.1:3000/dashboard",
		"Listening on http://0.0.0.0:8080",
		"IPv6:    http://[::1]:4000/",
		"ignored: http://localhost:65536/",
	}, "\n")

	streamDevOutput(strings.NewReader(input), out, ports)
	close(ports)

	var got []int
	for port := range ports {
		got = append(got, port)
	}
	want := []int{5173, 3000, 8080, 4000}
	if len(got) != len(want) {
		t.Fatalf("ports = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ports = %#v, want %#v", got, want)
		}
	}
}

func TestRestoreDevLinkPutsBackTheReplacedLink(t *testing.T) {
	previous := site.Link{Name: "app", Path: "/code/app", Root: "public", PHP: "8.3", Secure: true}
	s := &site.State{Links: []site.Link{previous}}
	site.AddLink(s, site.Link{Name: "app", Target: "127.0.0.1:5173", Secure: true})

	if _, changed := restoreDevLink(s, "app", "127.0.0.1:5173", &previous); !changed {
		t.Fatal("expected the link to be restored")
	}
	if len(s.Links) != 1 || s.Links[0] != previous {
		t.Fatalf("links = %#v, want %#v", s.Links, previous)
	}
}

func TestRestoreDevLinkRemovesAProxyThatReplacedNothing(t *testing.T) {
	s := &site.State{}
	site.AddLink(s, site.Link{Name: "app", Target: "127.0.0.1:5173", Secure: true})

	if _, changed := restoreDevLink(s, "app", "127.0.0.1:5173", nil); !changed {
		t.Fatal("expected the proxy to be removed")
	}
	if len(s.Links) != 0 {
		t.Fatalf("links = %#v, want none", s.Links)
	}
}

func TestRestoreDevLinkLeavesLaterChangesAlone(t *testing.T) {
	changed := site.Link{Name: "app", Target: "127.0.0.1:3000", Secure: true}
	s := &site.State{Links: []site.Link{changed}}

	if _, ok := restoreDevLink(s, "app", "127.0.0.1:5173", nil); ok {
		t.Fatal("restored over a link that changed while the dev server ran")
	}
	if len(s.Links) != 1 || s.Links[0] != changed {
		t.Fatalf("links = %#v", s.Links)
	}
}
