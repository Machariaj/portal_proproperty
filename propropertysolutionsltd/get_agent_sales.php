<?php
include 'db_connection.php';

$month = isset($_GET['month']) ? $_GET['month'] : '';

if ($month) {
    $stmt = $conn->prepare("
        SELECT agent_name, COUNT(*) AS total_sales
        FROM prop_sales
        WHERE DATE_FORMAT(sale_date, '%Y-%m') = ?
        GROUP BY agent_name
        ORDER BY total_sales DESC
    ");
    $stmt->bind_param("s", $month);
} else {
    $stmt = $conn->prepare("
        SELECT agent_name, COUNT(*) AS total_sales
        FROM prop_sales
        GROUP BY agent_name
        ORDER BY total_sales DESC
    ");
}

$stmt->execute();
$result = $stmt->get_result();

$labels = $values = [];
while ($row = $result->fetch_assoc()) {
    $labels[] = $row['agent_name'];
    $values[] = (int)$row['total_sales'];
}

echo json_encode(['labels' => $labels, 'values' => $values]);
?>
