<?php
include 'db.php';

if ($_SERVER['REQUEST_METHOD'] == 'POST') {
    $plot_id   = intval($_POST['plot_id']);
    $name      = $_POST['name'];
    $phone     = $_POST['phone'];
    $email     = $_POST['email'];
    $notes     = $_POST['notes'] ?? '';

    // Validate plot exists and is available
    $plot = $conn->query("SELECT * FROM prop_plots WHERE id = $plot_id AND status = 'available'")->fetch_assoc();
    if (!$plot) {
        http_response_code(400);
        echo json_encode(["error" => "Plot not available for booking"]);
        exit;
    }

    // Insert client (or find existing by phone/email)
    $client = $conn->query("SELECT id FROM prop_clients WHERE phone = '$phone' OR email = '$email'")->fetch_assoc();
    if ($client) {
        $client_id = $client['id'];
    } else {
        $stmt = $conn->prepare("INSERT INTO prop_clients (name, phone, email) VALUES (?, ?, ?)");
        $stmt->bind_param("sss", $name, $phone, $email);
        $stmt->execute();
        $client_id = $stmt->insert_id;
    }

    // Dates
    $booking_date = date("Y-m-d H:i:s");
    $expiry_date  = date("Y-m-d H:i:s", strtotime("+14 days"));

    // Insert booking
    $stmt = $conn->prepare("INSERT INTO prop_bookings (plot_id, client_id, booking_date, expiry_date, notes, status) VALUES (?, ?, ?, ?, ?, 'active')");
    $stmt->bind_param("iisss", $plot_id, $client_id, $booking_date, $expiry_date, $notes);
    $stmt->execute();

    // Update plot to booked
    $conn->query("UPDATE prop_plots SET status = 'booked' WHERE id = $plot_id");

    echo json_encode(["success" => true, "booking_id" => $stmt->insert_id, "expiry" => $expiry_date]);
}
?>
