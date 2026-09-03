<?php
session_start();
include 'activity_log.php';
include 'db_connection.php';

// Log page access
logPageAccess('bulk_book');

$role = $_SESSION['role'] ?? null;
$user_id = $_SESSION['user_id'] ?? null;
$agentName = $_SESSION['user_name'] ?? '';

if (!$user_id || !in_array($role, ['agent', 'admin'])) {
  header("Location: index.php");
  exit;
}

if ($_SERVER['REQUEST_METHOD'] !== 'POST' || !isset($_POST['selected_plots']) || empty($_POST['selected_plots']) || $_POST['bulk_action'] !== 'book') {
  header("Location: index.php");
  exit;
}

$selected_plots = array_map('intval', $_POST['selected_plots']);
$estate_id = intval($_POST['estate_id']);
$status = $_POST['status'];

// Validate plots are available and on same estate
$placeholders = str_repeat('?,', count($selected_plots) - 1) . '?';
$types = str_repeat('i', count($selected_plots));
$sql = "SELECT id, plot_number FROM prop_plots WHERE id IN ($placeholders) AND estate_id = ? AND status = 'available'";
$params = array_merge($selected_plots, [$estate_id]);
$types .= 'i';

$stmt = $conn->prepare($sql);
$stmt->bind_param($types, ...$params);
$stmt->execute();
$valid_plots = $stmt->get_result()->fetch_all(MYSQLI_ASSOC);

// Check if all selected plots are still available
$valid_plot_ids = array_column($valid_plots, 'id');
$unavailable_plots = array_diff($selected_plots, $valid_plot_ids);

if (!empty($unavailable_plots)) {
  $unavailable_numbers = [];
  foreach ($unavailable_plots as $plot_id) {
    // Get plot number for unavailable plots
    $plot_sql = "SELECT plot_number FROM prop_plots WHERE id = ?";
    $plot_stmt = $conn->prepare($plot_sql);
    $plot_stmt->bind_param("i", $plot_id);
    $plot_stmt->execute();
    $plot_result = $plot_stmt->get_result();
    if ($plot_row = $plot_result->fetch_assoc()) {
      $unavailable_numbers[] = $plot_row['plot_number'];
    }
  }
  $message = "The following plots are no longer available: " . implode(', ', $unavailable_numbers);
  echo "<script>alert('$message'); window.history.back();</script>";
  exit;
}

// Fetch estate name
$estate = $conn->query("SELECT name FROM prop_estates WHERE id = $estate_id")->fetch_assoc();
$estate_name = $estate['name'];

if ($_SERVER['REQUEST_METHOD'] === 'POST' && isset($_POST['buyer_name'])) {
  // Process booking
  $buyer_name = $conn->real_escape_string($_POST['buyer_name']);
  $buyer_phone = $conn->real_escape_string($_POST['buyer_phone']);
  $buyer_email = $conn->real_escape_string($_POST['buyer_email']);
  $notes = $conn->real_escape_string($_POST['notes']);

  $conn->begin_transaction();
  try {
    $stmt = $conn->prepare("INSERT INTO prop_bookings (plot_id, estate_id, buyer_name, buyer_phone, buyer_email, agent_name, notes, status) VALUES (?, ?, ?, ?, ?, ?, ?, 'active')");

    foreach ($valid_plots as $plot) {
      $stmt->bind_param("iisssss", $plot['id'], $estate_id, $buyer_name, $buyer_phone, $buyer_email, $agentName, $notes);
      $stmt->execute();

      // Update plot status
      $update_stmt = $conn->prepare("UPDATE prop_plots SET status = 'booked' WHERE id = ?");
      $update_stmt->bind_param("i", $plot['id']);
      $update_stmt->execute();

      // Log
      logDataModification('prop_bookings', 'INSERT', 'new', [
        'buyer_name' => $buyer_name,
        'buyer_phone' => $buyer_phone,
        'buyer_email' => $buyer_email,
        'notes' => $notes,
        'plot_id' => $plot['id'],
        'agent_id' => $user_id
      ]);
      logDataModification('prop_plots', 'UPDATE', $plot['id'], ['status' => 'booked']);
    }

    $conn->commit();
    $redirect_url = ($role === 'agent') ? 'agent_bookings.php' : 'admin_booked_plots.php';
    echo "<script>alert('Plots booked successfully!'); window.location.href='$redirect_url';</script>";
    exit;
  } catch (Exception $e) {
    $conn->rollback();
    echo "<script>alert('Error booking plots: " . $e->getMessage() . "'); window.history.back();</script>";
    exit;
  }
}

$page_title = "Bulk Book Plots - " . htmlspecialchars($estate_name);

ob_start();
?>
<div class="top-bar">
  <h1>Bulk Book Plots in <?php echo htmlspecialchars($estate_name); ?></h1>
</div>

<div class="card">
  <h3>Selected Plots:</h3>
  <ul>
    <?php foreach ($valid_plots as $plot): ?>
      <li><?php echo htmlspecialchars($plot['plot_number']); ?></li>
    <?php endforeach; ?>
  </ul>
</div>

<div class="card">
  <form method="POST">
    <input type="hidden" name="bulk_action" value="book">
    <input type="hidden" name="estate_id" value="<?php echo $estate_id; ?>">
    <?php foreach ($selected_plots as $plot_id): ?>
      <input type="hidden" name="selected_plots[]" value="<?php echo $plot_id; ?>">
    <?php endforeach; ?>

    <label>Buyer Name:</label>
    <input type="text" name="buyer_name" required>

    <label>Buyer Phone:</label>
    <input type="text" name="buyer_phone" required>

    <label>Buyer Email:</label>
    <input type="email" name="buyer_email">

    <label>Notes:</label>
    <textarea name="notes" rows="4" placeholder="Optional notes about the booking"></textarea>

    <button type="submit">Confirm Bulk Booking</button>
  </form>
</div>

<a href="plots.php?estate_id=<?php echo $estate_id; ?>&status=available" class="edit-btn">← Back to Plots</a>
<?php
$page_content = ob_get_clean();
include 'layout.php';
?>