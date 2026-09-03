<?php
session_start();
include 'activity_log.php'; // Include activity logging
include 'db_connection.php'; // Include database connection

// Log page access
logPageAccess('add_estate');

if (!isset($_SESSION['user_id']) || !in_array($_SESSION['role'], ['admin'])) {
  header("Location: index.php");
  exit;
}

$message = "";

if ($_SERVER['REQUEST_METHOD'] === 'POST') {
  $name = trim($_POST['name']);
  $prop_plotinfo = trim($_POST['prop_plotinfo']);

  // Handle image upload
  $image = "";
  if (!empty($_FILES['image']['name'])) {
    $target_dir = "uploads/";
    if (!is_dir($target_dir)) mkdir($target_dir, 0777, true);

    $image = basename($_FILES["image"]["name"]);
    $target_file = $target_dir . $image;

    move_uploaded_file($_FILES["image"]["tmp_name"], $target_file);
  }

  // Insert estate
  $stmt = $conn->prepare("INSERT INTO prop_estates (name, image, prop_plotinfo) VALUES (?, ?, ?)");
  $stmt->bind_param("sss", $name, $image, $prop_plotinfo);

  if ($stmt->execute()) {
    $message = "Estate added successfully!";
  } else {
    $message = "Error adding estate: " . $conn->error;
  }
}

$page_title = 'Add New Estate';

ob_start();
?>
  <div class="top-bar">
    <h1>Add New Estate</h1>
  </div>

  <?php if (!empty($message)): ?>
    <div class="card"><div class="message"><?php echo htmlspecialchars($message); ?></div></div>
  <?php endif; ?>

  <div class="card" style="max-width: 640px;">
    <form method="POST" enctype="multipart/form-data">
      <label for="name">Estate Name</label>
      <input type="text" id="name" name="name" required>

      <label for="prop_plotinfo">Estate Details / Plot Info</label>
      <textarea id="prop_plotinfo" name="prop_plotinfo" rows="5" required></textarea>

      <label for="image">Upload Image</label>
      <input type="file" id="image" name="image" accept="image/*" required>

      <button type="submit">Add Estate</button>
    </form>
  </div>
<?php
$page_content = ob_get_clean();
include 'layout.php';
?>
