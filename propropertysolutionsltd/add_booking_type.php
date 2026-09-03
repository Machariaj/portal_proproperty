<?php
include 'db_connection.php';

$sql = "ALTER TABLE prop_bookings ADD COLUMN booking_type ENUM('reserve','deposit','sa') NOT NULL DEFAULT 'reserve' AFTER status";

if ($conn->query($sql) === TRUE) {
    echo "Column booking_type added successfully.";
} else {
    echo "Error adding column: " . $conn->error;
}

$conn->close();
?>