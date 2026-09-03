<?php
session_start();

if (!isset($_SESSION['user_id']) || $_SESSION['role'] !== 'admin') {
  header("Location: index.php");
  exit;
}

include 'db_connection.php';

$status = isset($_GET['status']) ? $_GET['status'] : 'available';

// No special handling for dates now

// Set headers for CSV download
$filename = ($status === 'all') ? 'all_plots_report.csv' : $status . '_plots_report.csv';
header('Content-Type: text/csv');
header('Content-Disposition: attachment; filename="' . $filename . '"');

// Open output stream
$output = fopen('php://output', 'w');

// Write CSV header
fputcsv($output, ['Estate', 'Plot Number', 'Status', 'Buyer Name', 'Buyer Phone', 'Buyer Email', 'Agent Name', 'Amount', 'Payment Plan', 'Date', 'Notes']);

if ($status === 'all') {
    $where = "";
    $params = [];
    $types = '';
    if (isset($_GET['from_date']) && isset($_GET['to_date'])) {
        $where = "WHERE COALESCE(s.date_sold, b.date_booked) BETWEEN ? AND ?";
        $params = [$_GET['from_date'], $_GET['to_date']];
        $types = 'ss';
    }
    $sql = "SELECT e.name AS estate, p.plot_number, p.status,
            COALESCE(s.buyer_name, b.buyer_name) AS buyer_name,
            COALESCE(s.buyer_phone, b.buyer_phone) AS buyer_phone,
            COALESCE(s.buyer_email, b.buyer_email) AS buyer_email,
            COALESCE(s.agent_name, b.agent_name) AS agent_name,
            s.amount, s.payment_plan, COALESCE(s.date_sold, b.date_booked) AS date, b.notes
            FROM prop_plots p
            JOIN prop_estates e ON e.id = p.estate_id
            LEFT JOIN prop_bookings b ON b.plot_id = p.id AND p.status = 'booked'
            LEFT JOIN prop_sales s ON s.plot_id = p.id AND (p.status = 'sold' OR p.status = 'sa_signed')
            $where
            ORDER BY e.name, p.plot_number";
    $stmt = $conn->prepare($sql);
    if (!empty($params)) {
        $stmt->bind_param($types, ...$params);
    }
    $stmt->execute();
    $result = $stmt->get_result();
    while ($row = $result->fetch_assoc()) {
        fputcsv($output, [$row['estate'], $row['plot_number'], $row['status'], $row['buyer_name'], $row['buyer_phone'], $row['buyer_email'], $row['agent_name'], $row['amount'], $row['payment_plan'], $row['date'], $row['notes']]);
    }
} elseif ($status === 'available') {
    $sql = "SELECT e.name AS estate, p.plot_number, p.status, NULL AS buyer_name, NULL AS buyer_phone, NULL AS buyer_email, NULL AS agent_name, NULL AS amount, NULL AS payment_plan, NULL AS date, NULL AS notes
            FROM prop_plots p
            JOIN prop_estates e ON e.id = p.estate_id
            WHERE p.status = 'available'
            ORDER BY e.name, p.plot_number";
    $stmt = $conn->prepare($sql);
    $stmt->execute();
    $result = $stmt->get_result();
    while ($row = $result->fetch_assoc()) {
        fputcsv($output, [$row['estate'], $row['plot_number'], $row['status'], $row['buyer_name'], $row['buyer_phone'], $row['buyer_email'], $row['agent_name'], $row['amount'], $row['payment_plan'], $row['date'], $row['notes']]);
    }
} elseif ($status === 'booked') {
    $where = "WHERE p.status = 'booked'";
    $params = [];
    $types = '';
    if (isset($_GET['from_date']) && isset($_GET['to_date'])) {
        $where .= " AND b.date_booked BETWEEN ? AND ?";
        $params[] = $_GET['from_date'];
        $params[] = $_GET['to_date'];
        $types = 'ss';
    }
    $sql = "SELECT e.name AS estate, p.plot_number, p.status, b.buyer_name, b.buyer_phone, b.buyer_email, b.agent_name, NULL AS amount, NULL AS payment_plan, b.date_booked AS date, b.notes
            FROM prop_plots p
            JOIN prop_estates e ON e.id = p.estate_id
            JOIN prop_bookings b ON b.plot_id = p.id
            $where
            ORDER BY e.name, p.plot_number";
    $stmt = $conn->prepare($sql);
    if (!empty($params)) {
        $stmt->bind_param($types, ...$params);
    }
    $stmt->execute();
    $result = $stmt->get_result();
    while ($row = $result->fetch_assoc()) {
        fputcsv($output, [$row['estate'], $row['plot_number'], $row['status'], $row['buyer_name'], $row['buyer_phone'], $row['buyer_email'], $row['agent_name'], $row['amount'], $row['payment_plan'], $row['date'], $row['notes']]);
    }
} elseif ($status === 'sold' || $status === 'sa_signed') {
    $where = "WHERE p.status = ?";
    $params = [$status];
    $types = 's';
    if (isset($_GET['from_date']) && isset($_GET['to_date'])) {
        $where .= " AND s.date_sold BETWEEN ? AND ?";
        $params[] = $_GET['from_date'];
        $params[] = $_GET['to_date'];
        $types .= 'ss';
    }
    $sql = "SELECT e.name AS estate, p.plot_number, p.status, s.buyer_name, s.buyer_phone, s.buyer_email, s.agent_name, s.amount, s.payment_plan, s.date_sold AS date, NULL AS notes
            FROM prop_plots p
            JOIN prop_estates e ON e.id = p.estate_id
            JOIN prop_sales s ON s.plot_id = p.id
            $where
            ORDER BY e.name, p.plot_number";
    $stmt = $conn->prepare($sql);
    $stmt->bind_param($types, ...$params);
    $stmt->execute();
    $result = $stmt->get_result();
    while ($row = $result->fetch_assoc()) {
        fputcsv($output, [$row['estate'], $row['plot_number'], $row['status'], $row['buyer_name'], $row['buyer_phone'], $row['buyer_email'], $row['agent_name'], $row['amount'], $row['payment_plan'], $row['date'], $row['notes']]);
    }
}

fclose($output);
$conn->close();
?>