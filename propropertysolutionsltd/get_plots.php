<?php
include 'db.php';

$estate_id = $_GET['estate_id'];
$status = $_GET['status'];

$sql = "SELECT * FROM prop_plots WHERE estate_id=? AND status=?";
$stmt = $conn->prepare($sql);
$stmt->bind_param("is", $estate_id, $status);
$stmt->execute();
$result = $stmt->get_result();

if ($result->num_rows > 0) {
    echo "<ul class='list-group'>";
    while ($row = $result->fetch_assoc()) {
        echo "<li class='list-group-item d-flex justify-content-between'>
                Plot " . $row['plot_number'] . " 
                <button class='btn btn-info btn-sm plot-details' data-id='" . $row['id'] . "'>Details</button>
              </li>";
    }
    echo "</ul>";
} else {
    echo "<p>No plots found.</p>";
}
?>
