<?php
include 'db_connection.php';

echo "<h1>Debug Bookings</h1>";

// Check prop_bookings table
$result = $conn->query("SELECT * FROM prop_bookings WHERE status = 'active'");
echo "<h2>Active Bookings:</h2>";
if ($result->num_rows > 0) {
    while ($row = $result->fetch_assoc()) {
        echo "ID: " . $row['id'] . " - Plot ID: " . $row['plot_id'] . " - Buyer: " . $row['buyer_name'] . " - Status: " . $row['status'] . "<br>";
    }
} else {
    echo "No active bookings found.<br>";
}

// Check the query used in admin_booked_plots.php
$sql = "SELECT b.id AS booking_id, b.plot_id, b.buyer_name AS client_name, b.buyer_phone AS phone, b.buyer_email AS email, b.notes, b.agent_name, p.plot_number, e.name AS estate_name, b.date_booked
        FROM prop_bookings b
        JOIN prop_plots p ON p.id = b.plot_id
        JOIN prop_estates e ON e.id = p.estate_id
        WHERE b.status = 'active'
        ORDER BY b.date_booked DESC";

$result = $conn->query($sql);
echo "<h2>Query Result:</h2>";
if ($result->num_rows > 0) {
    while ($row = $result->fetch_assoc()) {
        echo "Booking ID: " . $row['booking_id'] . " - Client: " . $row['client_name'] . " - Plot: " . $row['plot_number'] . " - Estate: " . $row['estate_name'] . "<br>";
    }
} else {
    echo "No results from the query.<br>";
}

$conn->close();
?>
