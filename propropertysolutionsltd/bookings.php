<?php
include 'db.php';

$sql = "SELECT b.*, c.name AS client_name, c.phone, c.email, p.plot_number, e.name AS estate_name
        FROM prop_bookings b
        JOIN prop_clients c ON c.id = b.client_id
        JOIN prop_plots p ON p.id = b.plot_id
        JOIN prop_estates e ON e.id = p.estate_id
        WHERE b.status = 'active'";

$result = $conn->query($sql);

$bookings = [];
while ($row = $result->fetch_assoc()) {
    $bookings[] = $row;
}

header('Content-Type: application/json');
echo json_encode($bookings);
?>
