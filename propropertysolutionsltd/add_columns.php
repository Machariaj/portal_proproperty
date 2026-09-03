<?php
require_once 'db_connection.php';

$sql = "ALTER TABLE prop_estates ADD COLUMN latitude DECIMAL(10,8) NULL, ADD COLUMN longitude DECIMAL(11,8) NULL";

if ($conn->query($sql) === TRUE) {
    echo "Columns added successfully";
} else {
    echo "Error adding columns: " . $conn->error;
}

$conn->close();
?>