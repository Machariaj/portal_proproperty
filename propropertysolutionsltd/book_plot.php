<?php
session_start();
include 'activity_log.php'; // Include activity logging
include 'db_connection.php'; // Include database connection

// Log page access
logPageAccess('book_plot');

if (!isset($_SESSION['user_id']) || !in_array($_SESSION['role'], ['agent', 'admin'])) {
  header("Location: index.php");
  exit;
}

$agentName = $_SESSION['user_name'] ?? '';

if (!isset($_GET['plot_id'])) {
  die("Invalid request: plot_id missing.");
}

$plot_id = intval($_GET['plot_id']);
$plot_query = $conn->query("SELECT p.*, e.name AS estate_name FROM prop_plots p
                            JOIN prop_estates e ON p.estate_id = e.id
                            WHERE p.id = $plot_id");
$plot = $plot_query ? $plot_query->fetch_assoc() : null;

if (!$plot) {
  die("Plot not found or invalid ID.");
}

if ($_SERVER["REQUEST_METHOD"] === "POST") {
  $buyer_name = $conn->real_escape_string($_POST['buyer_name']);
  $buyer_phone = $conn->real_escape_string($_POST['buyer_phone']);
  $buyer_email = $conn->real_escape_string($_POST['buyer_email']);
  $notes = $conn->real_escape_string($_POST['notes']);
  $estate_id = $plot['estate_id'];

  // Reserve a plot - status is 'active' and plot status is 'booked' (yellow grid)
  $status = 'active';
  $plot_status = 'booked';

  $stmt = $conn->prepare("INSERT INTO prop_bookings (plot_id, estate_id, buyer_name, buyer_phone, buyer_email, agent_name, notes, status)
                          VALUES (?, ?, ?, ?, ?, ?, ?, ?)");
  $stmt->bind_param("iissssss", $plot_id, $estate_id, $buyer_name, $buyer_phone, $buyer_email, $agentName, $notes, $status);
  $stmt->execute();

  $conn->query("UPDATE prop_plots SET status = '$plot_status' WHERE id = $plot_id");

  // Log plot booking activity
  logDataModification('prop_bookings', 'INSERT', 'new', [
    'buyer_name' => $buyer_name,
    'buyer_phone' => $buyer_phone,
    'buyer_email' => $buyer_email,
    'notes' => $notes,
    'plot_id' => $plot_id,
    'agent_id' => $_SESSION['user_id']
  ]);
  logDataModification('prop_plots', 'UPDATE', $plot_id, ['status' => 'booked']);

  $redirect_page = ($_SESSION['role'] === 'admin') ? 'admin_booked_plots.php' : 'agent_bookings.php';
  echo "<script>alert('Plot reserved successfully!'); window.location.href='$redirect_page';</script>";
  exit;
}

$page_title = "Book Plot " . htmlspecialchars($plot['plot_number']);

ob_start();
?>
  <div class="card">
    <h2>Reserve Plot <?php echo htmlspecialchars($plot['plot_number']); ?> - <?php echo htmlspecialchars($plot['estate_name']); ?></h2>
    <form method="POST" onsubmit="return confirm('Confirm reserving this plot?');">
      <label>Buyer Name:</label>
      <input type="text" name="buyer_name" required>

      <label>Buyer Phone:</label>
      <input type="text" name="buyer_phone" required>

      <label>Buyer Email:</label>
      <input type="email" name="buyer_email">

      <label>Notes:</label>
      <textarea name="notes" rows="4" placeholder="Optional notes about the reservation"></textarea>

      <button type="submit">Confirm Reservation</button>
    </form>
  </div>
<?php
$page_content = ob_get_clean();
include "layout.php";
?>
