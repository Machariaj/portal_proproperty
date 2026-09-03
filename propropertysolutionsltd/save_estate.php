<?php
include 'db_connection.php';

// Get form data
$name = $_POST['name'];
$prop_plotinfo = $_POST['prop_plotinfo'];
$num_plots = (int)$_POST['num_plots'];

// Handle image upload
$imageName = $_FILES['image']['name'];
$imageTmp = $_FILES['image']['tmp_name'];
$imagePath = "uploads/" . basename($imageName);
move_uploaded_file($imageTmp, $imagePath);

// Insert new estate
$stmt = $conn->prepare("INSERT INTO prop_estates (name, image, prop_plotinfo) VALUES (?, ?, ?)");
$stmt->bind_param("sss", $name, $imagePath, $prop_plotinfo);
$stmt->execute();

$estateId = $conn->insert_id; // Get the new estate ID

// Auto-insert plots
$stmt_plot = $conn->prepare("INSERT INTO prop_plots (estate_id, plot_number, status) VALUES (?, ?, 'available')");

for ($i = 1; $i <= $num_plots; $i++) {
    $stmt_plot->bind_param("ii", $estateId, $i);
    $stmt_plot->execute();
}

$stmt_plot->close();
$stmt->close();
$conn->close();

echo "<h3>✅ Estate and $num_plots plots added successfully!</h3>";
echo "<a href='estates.php'>Back to Estates</a>";
?>
