package site

import (
	"os"
	"path/filepath"

	"github.com/scottzirkel/routa/internal/paths"
)

const valetRouterFileName = "routa-valet-router.php"

const valetRouterPHP = `<?php

namespace Laravel\Valet\Drivers {
    if (! class_exists(__NAMESPACE__.'\ValetDriver', false)) {
        abstract class ValetDriver
        {
            public function serves(string $sitePath, string $siteName, string $uri): bool
            {
                return false;
            }

            public function isStaticFile(string $sitePath, string $siteName, string $uri)
            {
                return false;
            }

            public function frontControllerPath(string $sitePath, string $siteName, string $uri): string
            {
                return $sitePath.'/public/index.php';
            }
        }
    }

    if (! class_exists(__NAMESPACE__.'\BasicValetDriver', false)) {
        class BasicValetDriver extends ValetDriver
        {
            public function serves(string $sitePath, string $siteName, string $uri): bool
            {
                return is_file($sitePath.'/public/index.php') || is_file($sitePath.'/index.php');
            }

            public function isStaticFile(string $sitePath, string $siteName, string $uri)
            {
                $docroot = getenv('ROUTA_DOCROOT') ?: $sitePath.'/public';
                $root = realpath($docroot);
                $path = realpath($docroot.'/'.ltrim($uri, '/'));

                if ($root !== false && $path !== false && is_file($path) && str_starts_with($path, $root.DIRECTORY_SEPARATOR)) {
                    return $path;
                }

                return false;
            }

            public function frontControllerPath(string $sitePath, string $siteName, string $uri): string
            {
                $docroot = getenv('ROUTA_DOCROOT') ?: $sitePath.'/public';

                if (is_file($docroot.'/index.php')) {
                    return $docroot.'/index.php';
                }

                return $sitePath.'/index.php';
            }
        }
    }

    if (! class_exists(__NAMESPACE__.'\LaravelValetDriver', false)) {
        class LaravelValetDriver extends BasicValetDriver {}
    }
}

namespace {
    if (! class_exists('ValetDriver', false)) {
        abstract class ValetDriver extends \Laravel\Valet\Drivers\ValetDriver {}
    }

    if (! class_exists('BasicValetDriver', false)) {
        class BasicValetDriver extends \Laravel\Valet\Drivers\BasicValetDriver {}
    }

    if (! class_exists('LaravelValetDriver', false)) {
        class LaravelValetDriver extends \Laravel\Valet\Drivers\LaravelValetDriver {}
    }

    $sitePath = getenv('ROUTA_SITE_PATH') ?: getcwd();
    $siteName = getenv('ROUTA_SITE_NAME') ?: '';
    $uri = parse_url($_SERVER['REQUEST_URI'] ?? '/', PHP_URL_PATH) ?: '/';
    $uri = '/'.ltrim(rawurldecode($uri), '/');

    foreach (routa_valet_drivers($sitePath) as $driver) {
        if (! $driver->serves($sitePath, $siteName, $uri)) {
            continue;
        }

        if ($uri !== '/') {
            $staticFile = $driver->isStaticFile($sitePath, $siteName, $uri);
            if (is_string($staticFile) && is_file($staticFile)) {
                routa_valet_serve_static($staticFile);
                return;
            }
        }

        $frontController = $driver->frontControllerPath($sitePath, $siteName, $uri);
        routa_valet_require_front_controller($frontController);
        return;
    }

    http_response_code(404);
    echo 'routa: no Valet driver served '.$siteName.'.test';

    function routa_valet_drivers(string $sitePath): array
    {
        $drivers = [];
        $local = $sitePath.'/LocalValetDriver.php';

        if (is_file($local)) {
            $driver = routa_valet_driver_from_file($local);
            if ($driver !== null) {
                $drivers[] = $driver;
            }
        }

        $dirs = array_filter(explode(PATH_SEPARATOR, getenv('ROUTA_VALET_DRIVER_DIRS') ?: ''));
        foreach ($dirs as $dir) {
            foreach (glob($dir.'/*ValetDriver.php') ?: [] as $file) {
                if (basename($file) === 'LocalValetDriver.php') {
                    continue;
                }
                $driver = routa_valet_driver_from_file($file);
                if ($driver !== null) {
                    $drivers[] = $driver;
                }
            }
        }

        $drivers[] = new \Laravel\Valet\Drivers\BasicValetDriver();

        return $drivers;
    }

    function routa_valet_driver_from_file(string $file)
    {
        $before = get_declared_classes();
        require_once $file;
        $after = array_values(array_diff(get_declared_classes(), $before));

        foreach (array_reverse($after) as $class) {
            if (! class_exists($class)) {
                continue;
            }
            $reflection = new \ReflectionClass($class);
            if (! $reflection->isInstantiable()) {
                continue;
            }
            $candidate = $reflection->newInstance();
            if (
                method_exists($candidate, 'serves') &&
                method_exists($candidate, 'isStaticFile') &&
                method_exists($candidate, 'frontControllerPath')
            ) {
                return $candidate;
            }
        }

        return null;
    }

    function routa_valet_serve_static(string $file): void
    {
        if (! headers_sent()) {
            $type = function_exists('mime_content_type') ? mime_content_type($file) : false;
            if (is_string($type) && $type !== '') {
                header('Content-Type: '.$type);
            }
            header('Content-Length: '.filesize($file));
        }

        readfile($file);
    }

    function routa_valet_require_front_controller(string $frontController): void
    {
        if (! is_file($frontController)) {
            http_response_code(404);
            echo 'routa: front controller not found';
            return;
        }

        $_SERVER['SCRIPT_FILENAME'] = $frontController;
        $_SERVER['SCRIPT_NAME'] = '/index.php';
        $_SERVER['PHP_SELF'] = '/index.php';

        chdir(dirname($frontController));
        require $frontController;
    }
}
`

func writeValetRouter() error {
	if err := os.MkdirAll(paths.DataDir(), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(paths.DataDir(), valetRouterFileName), []byte(valetRouterPHP), 0o644)
}
