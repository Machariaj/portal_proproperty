<?php
// Test database connection
include 'config.php';

echo "Testing database connection...<br>";

$conn = new mysqli(DB_HOST, DB_USER, DB_PASS, DB_NAME);

if ($conn->connect_error) {
    echo "❌ Connection failed: " . $conn->connect_error;
} else {
    echo "✅ Database connection successful!<br>";
    echo "Host: " . DB_HOST . "<br>";
    echo "Database: " . DB_NAME . "<br>";

    // Test a simple query
    $result = $conn->query("SELECT 1 as test");
    if ($result) {
        echo "✅ Query execution successful!<br>";
    } else {
        echo "❌ Query failed: " . $conn->error . "<br>";
    }

    $conn->close();
}
?>