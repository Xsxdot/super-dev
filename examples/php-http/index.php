<?php
/**
 * PHP HTTP pipeline example.
 *
 * Responsibilities:
 *   - Expose /health for deployment health checks.
 *   - Expose /info with language and app metadata.
 *
 * Boundaries:
 *   - Uses PHP built-in server routing.
 *   - Does not depend on a reverse proxy.
 */

$path = parse_url($_SERVER['REQUEST_URI'], PHP_URL_PATH);

if ($path === '/health') {
    http_response_code(200);
    echo 'ok';
    return;
}

if ($path === '/info') {
    header('content-type: application/json');
    echo json_encode([
        'app' => 'php-http',
        'language' => 'php',
        'version' => getenv('APP_VERSION') ?: '',
    ]);
    return;
}

echo 'php-http';
