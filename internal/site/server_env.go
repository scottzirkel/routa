package site

import (
	"os"
	"path/filepath"

	"github.com/scottzirkel/routa/internal/paths"
)

const serverEnvPrependerFileName = "routa-server-env.php"

const serverEnvPrependerPHP = `<?php

(function (): void {
    $sitePath = getenv('ROUTA_SITE_PATH') ?: '';
    $siteName = getenv('ROUTA_SITE_NAME') ?: '';

    if ($sitePath === '') {
        return;
    }

    $serverEnvFile = static function (string $sitePath): ?string {
        $native = $sitePath.'/.routa-env.php';
        if (is_file($native)) {
            return $native;
        }

        $valet = $sitePath.'/.valet-env.php';
        if (is_file($valet)) {
            return $valet;
        }

        return null;
    };

    $serverEnvValues = static function (array $config, string $siteName): array {
        $values = [];

        foreach (['*', $siteName] as $name) {
            if (isset($config[$name]) && is_array($config[$name])) {
                $values = array_merge($values, $config[$name]);
            }
        }

        if ($values !== []) {
            return $values;
        }

        foreach ($config as $key => $value) {
            if (! is_array($value)) {
                $values[$key] = $value;
            }
        }

        return $values;
    };

    $file = $serverEnvFile($sitePath);
    if ($file === null) {
        return;
    }

    $config = require $file;
    if (! is_array($config)) {
        return;
    }

    foreach ($serverEnvValues($config, $siteName) as $key => $value) {
        $_SERVER[(string) $key] = $value;
    }
})();
`

func serverEnvPrependerPath() string {
	return filepath.Join(paths.DataDir(), serverEnvPrependerFileName)
}

func writeServerEnvPrepender() error {
	if err := os.MkdirAll(paths.DataDir(), 0o755); err != nil {
		return err
	}
	return os.WriteFile(serverEnvPrependerPath(), []byte(serverEnvPrependerPHP), 0o644)
}
