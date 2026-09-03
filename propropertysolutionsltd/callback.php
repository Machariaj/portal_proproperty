<?php
// Simple callback script to capture Zoho OAuth authorization code

if (isset($_GET['code'])) {
    $code = $_GET['code'];
    echo "<h1>Authorization Code Received</h1>";
    echo "<p><strong>Code:</strong> " . htmlspecialchars($code) . "</p>";
    echo "<p>Copy this code and paste it into <code>get_zoho_books_tokens.php</code></p>";
    echo "<p>Then run: <code>php get_zoho_books_tokens.php</code></p>";
} elseif (isset($_GET['error'])) {
    echo "<h1>Authorization Error</h1>";
    echo "<p><strong>Error:</strong> " . htmlspecialchars($_GET['error']) . "</p>";
    if (isset($_GET['error_description'])) {
        echo "<p><strong>Description:</strong> " . htmlspecialchars($_GET['error_description']) . "</p>";
    }
} else {
    echo "<h1>Zoho OAuth Callback</h1>";
    echo "<p>Waiting for authorization code...</p>";
}
?>