<?php
include 'db_connection.php';

if (!isset($_GET['id'])) {
  die("Invalid estate ID.");
}

$id = intval($_GET['id']);
$estate = $conn->query("SELECT * FROM prop_estates WHERE id = $id")->fetch_assoc();

if (!$estate) {
  die("Estate not found.");
}

$message = "";

if ($_SERVER["REQUEST_METHOD"] === "POST") {
  $name = $conn->real_escape_string($_POST['name']);
  $prop_plotinfo = $conn->real_escape_string($_POST['prop_plotinfo']);
  $image = $estate['image']; // Keep existing image by default

  // Handle image upload
  if (!empty($_FILES['image']['name'])) {
    $target_dir = "uploads/";
    if (!is_dir($target_dir)) mkdir($target_dir, 0777, true);

    $target_file = $target_dir . basename($_FILES["image"]["name"]);
    move_uploaded_file($_FILES["image"]["tmp_name"], $target_file);
    $image = $_FILES["image"]["name"];
  }

  // Update record
  $stmt = $conn->prepare("UPDATE prop_estates SET name = ?, prop_plotinfo = ?, image = ? WHERE id = ?");
  $stmt->bind_param("sssi", $name, $prop_plotinfo, $image, $id);
  $stmt->execute();

  $message = "Estate updated successfully!";
  // Refresh estate data
  $estate = $conn->query("SELECT * FROM prop_estates WHERE id = $id")->fetch_assoc();
}

$page_title = 'Edit Estate - ' . htmlspecialchars($estate['name']);

ob_start();
?>
  <div class="top-bar">
    <h1>Edit Estate - <?= htmlspecialchars($estate['name']) ?></h1>
  </div>

  <?php if ($message): ?>
    <div class="card"><div class="message"><?php echo htmlspecialchars($message); ?></div></div>
  <?php endif; ?>

  <div class="card" style="max-width: 760px;">
    <form method="POST" enctype="multipart/form-data">
      <label for="name">Estate Name</label>
      <input type="text" id="name" name="name" value="<?= htmlspecialchars($estate['name']) ?>" required>

      <label for="prop_plotinfo">Estate Info</label>
      <textarea id="prop_plotinfo" name="prop_plotinfo" rows="5" required><?= htmlspecialchars($estate['prop_plotinfo']) ?></textarea>

      <label>Current Image</label>
      <img src="uploads/<?= htmlspecialchars($estate['image']) ?>" alt="Estate Image" style="max-width: 100%; border-radius: 10px; margin-bottom: 12px;">

      <label for="image">Change Image (optional)</label>
      <input type="file" id="image" name="image" accept="image/*">

      <button type="submit">💾 Update Estate</button>
    </form>
  </div>

  <a href="estates.php" class="edit-btn">← Back to Estates</a>
<?php
$page_content = ob_get_clean();
include 'layout.php';
?>
