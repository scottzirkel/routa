package php

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/scottzirkel/routa/internal/paths"
)

// ManagedExtension is a shared extension routa builds and publishes itself.
// The upstream gnu-bulk PHP builds ship neither Xdebug nor pcov — static-php-cli
// can only compile either one as a shared object — so routa distributes a
// matching .so per PHP version and loads it from the per-version php.ini.
type ManagedExtension struct {
	Name string
	// Zend is true when php.ini must load the extension with zend_extension=
	// rather than extension=.
	Zend bool
}

var (
	XdebugExtension = ManagedExtension{Name: "xdebug", Zend: true}
	PcovExtension   = ManagedExtension{Name: "pcov"}
)

// ManagedExtensions is the set routa installs alongside every PHP build.
func ManagedExtensions() []ManagedExtension {
	return []ManagedExtension{XdebugExtension, PcovExtension}
}

// LoadKey is the php.ini key that loads the extension.
func (e ManagedExtension) LoadKey() string {
	if e.Zend {
		return ZendExtensionKey
	}
	return ExtensionKey
}

func (e ManagedExtension) fileName() string { return e.Name + ".so" }

// Path is where InstallExtension writes the extension for a version spec.
func (e ManagedExtension) Path(spec string) string {
	return filepath.Join(paths.PHPDir(), spec, "extensions", e.fileName())
}

// Available reports whether the extension has been downloaded for spec.
func (e ManagedExtension) Available(spec string) bool {
	info, err := os.Stat(e.Path(spec))
	return err == nil && !info.IsDir()
}

// loadedBy reports whether an ini value points at this extension's .so.
func (e ManagedExtension) loadedBy(value string) bool {
	value = strings.Trim(strings.TrimSpace(value), `"'`)
	return strings.EqualFold(filepath.Base(value), e.fileName())
}

// InstallExtension downloads the routa-built .so for version. It reports false
// with no error when the artifact has not been published for that version yet,
// so a PHP install can continue without it.
func InstallExtension(ctx context.Context, ext ManagedExtension, version string, out io.Writer) (bool, error) {
	if out == nil {
		out = io.Discard
	}
	exact := version
	if _, err := ParseVersion(version); err != nil {
		resolved, err := resolveInstalledSpec(version)
		if err != nil {
			return false, err
		}
		exact = resolved
	}
	v, err := ParseVersion(exact)
	if err != nil {
		return false, err
	}
	url, err := extensionArchiveURL(ext, v)
	if err != nil {
		return false, err
	}
	dest := ext.Path(exact)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return false, err
	}

	fmt.Fprintf(out, "  ↓ %-8s %s\n", ext.Name, exact)
	if err := downloadAndExtract(ctx, url, dest, out); err != nil {
		var status downloadStatusError
		if errors.As(err, &status) && status.Code == http.StatusNotFound {
			_ = os.Remove(dest)
			return false, nil
		}
		_ = os.Remove(dest)
		return false, err
	}
	return true, nil
}

func extensionArchiveURL(ext ManagedExtension, version Version) (string, error) {
	arch, err := routaAssetArch()
	if err != nil {
		return "", err
	}
	base := strings.TrimRight(os.Getenv("ROUTA_PHP_EXT_BASE_URL"), "/")
	if base == "" {
		base = "https://github.com/scottzirkel/routa/releases/download/php-extensions"
	}
	name := fmt.Sprintf("routa_php_%s_%s_linux_%s.tar.gz", ext.Name, version, arch)
	return base + "/" + name, nil
}
