<?php
include 'db_connection.php';

$sql = "ALTER TABLE prop_bookings ADD COLUMN notes TEXT DEFAULT NULL";

if ($conn->query($sql) === TRUE) {
    echo "Column notes added successfully to prop_bookings table.";
} else {
    echo "Error adding column: " . $conn->error;
}

$conn->close();
?>
