<?php
include 'db.php';

$now = date("Y-m-d H:i:s");

// Find expired bookings
$sql = "SELECT id, plot_id FROM prop_bookings 
        WHERE status = 'active' AND expiry_date <= '$now'";
$result = $conn->query($sql);

if ($result->num_rows > 0) {
    while ($row = $result->fetch_assoc()) {
        $booking_id = $row['id'];
        $plot_id = $row['plot_id'];

        // Mark booking expired
        $conn->query("UPDATE prop_bookings SET status = 'expired' WHERE id = $booking_id");

        // Make plot available again
        $conn->query("UPDATE prop_plots SET status = 'available' WHERE id = $plot_id");
    }
    echo json_encode(["success" => true, "expired_count" => $result->num_rows]);
} else {
    echo json_encode(["success" => true, "expired_count" => 0]);
}
?>
