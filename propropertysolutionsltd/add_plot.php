<?php
session_start();
include 'activity_log.php'; // Include activity logging
include 'db_connection.php'; // Include database connection

// Log page access
logPageAccess('add_plot');

if (!isset($_SESSION['user_id']) || !in_array($_SESSION['role'], ['admin'])) {
  header("Location: index.php");
  exit;
}

// Fetch estates
$estates = $conn->query("SELECT id, name FROM prop_estates");

$message = "";
if ($_SERVER['REQUEST_METHOD'] === 'POST') {
  $estate_id = intval($_POST['estate_id']);
  $plot_numbers_input = trim($_POST['plot_numbers']);
  $booked_plots = trim($_POST['booked_plots']);
  $sold_plots = trim($_POST['sold_plots']);

  if ($estate_id > 0 && !empty($plot_numbers_input)) {

    // Parse plot numbers (supports digits, letters, ranges like 1-5, a-c)
    function parse_plot_list($input) {
      $plots = [];
      $parts = explode(',', $input);
      foreach ($parts as $part) {
        $part = trim($part);
        if (preg_match('/^([a-zA-Z0-9]+)-([a-zA-Z0-9]+)$/', $part, $m)) {
          // Range: expand if possible
          $start = $m[1];
          $end = $m[2];
          if (is_numeric($start) && is_numeric($end)) {
            $plots = array_merge($plots, range($start, $end));
          } else {
            // For letters, assume sequential ASCII
            $start_ord = ord(strtoupper($start));
            $end_ord = ord(strtoupper($end));
            if ($start_ord <= $end_ord) {
              for ($i = $start_ord; $i <= $end_ord; $i++) {
                $plots[] = chr($i);
              }
            }
          }
        } else {
          $plots[] = $part;
        }
      }
      return array_unique($plots);
    }

    $all_plots = parse_plot_list($plot_numbers_input);
    $booked_list = parse_plot_list($booked_plots);
    $sold_list = parse_plot_list($sold_plots);

    $inserted = 0;
    $skipped = 0;

    $check_stmt = $conn->prepare("SELECT COUNT(*) FROM prop_plots WHERE estate_id = ? AND plot_number = ?");
    $insert_stmt = $conn->prepare("INSERT INTO prop_plots (estate_id, plot_number, status) VALUES (?, ?, ?)");

    // Prepared statements for creating booking/sale rows for imported statuses
    $bulkAgent = $_SESSION['user_name'] ?? 'System';
    $bkStmt = $conn->prepare("INSERT INTO prop_bookings (plot_id, estate_id, buyer_name, buyer_phone, buyer_email, agent_name, notes, status, date_booked) VALUES (?, ?, ?, ?, ?, ?, ?, 'active', NOW())");
    $saleStmt = $conn->prepare("INSERT INTO prop_sales (plot_id, estate_id, agent_name, buyer_name, buyer_phone, buyer_email, amount, payment_plan, deposit_timing, date_sold) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NOW())");

    foreach ($all_plots as $plot_number) {
      // Check if plot already exists
      $check_stmt->bind_param("is", $estate_id, $plot_number);
      $check_stmt->execute();
      $check_stmt->bind_result($exists);
      $check_stmt->fetch();
      $check_stmt->free_result();

      if ($exists > 0) {
        $skipped++;
        continue;
      }

      // Determine status
      if (in_array($plot_number, $booked_list)) {
        $status = "booked";
      } elseif (in_array($plot_number, $sold_list)) {
        $status = "sold";
      } else {
        $status = "available";
      }

      // Insert new plot
      $insert_stmt->bind_param("iss", $estate_id, $plot_number, $status);
      $insert_stmt->execute();
      $plot_id = $conn->insert_id;
      $inserted++;

      // Mirror to bookings/sales for admin pages if imported as booked/sold
      if ($status === 'booked') {
        $buyer_name = 'N/A';
        $buyer_phone = '';
        $buyer_email = '';
        $notes = 'Imported during estate creation';
        $bkStmt->bind_param("iisssss", $plot_id, $estate_id, $buyer_name, $buyer_phone, $buyer_email, $bulkAgent, $notes);
        $bkStmt->execute();
      } elseif ($status === 'sold') {
        $buyer_name = 'N/A';
        $buyer_phone = '';
        $buyer_email = '';
        $amount = 0;
        $payment_plan = 'N/A';
        $deposit_timing = 'import';
        $saleStmt->bind_param("iissssiss", $plot_id, $estate_id, $bulkAgent, $buyer_name, $buyer_phone, $buyer_email, $amount, $payment_plan, $deposit_timing);
        $saleStmt->execute();
      }
    }

    $total_processed = count($all_plots);
    $message = "✅ $total_processed plots processed. Added: $inserted, Skipped (already existed): $skipped.";
  } else {
    $message = "⚠️ Please select an estate and enter plot numbers.";
  }
}

$page_title = 'Add Multiple Plots';

ob_start();
?>
  <div class="top-bar">
    <h1>Add Multiple Plots</h1>
  </div>

  <?php if ($message): ?>
    <div class="card"><p class="message"><?php echo htmlspecialchars($message); ?></p></div>
  <?php endif; ?>

  <div class="card" style="max-width: 640px;">
    <form method="POST">
      <label for="estate_id">Select Estate</label>
      <select id="estate_id" name="estate_id" required>
        <option value="">-- Select Estate --</option>
        <?php while ($row = $estates->fetch_assoc()): ?>
          <option value="<?= $row['id'] ?>"><?= htmlspecialchars($row['name']) ?></option>
        <?php endwhile; ?>
      </select>

      <label for="plot_numbers">Plot Numbers</label>
      <input type="text" id="plot_numbers" name="plot_numbers" placeholder="e.g. 1,2,3,a,b,c,10-15" required>
      <small>Use commas or ranges. Supports digits and letters. Example: 1,2,3,a,b,c,10-15</small>

      <label for="booked_plots">Booked Plot Numbers</label>
      <input type="text" id="booked_plots" name="booked_plots" placeholder="e.g. 2, b, 10-12">
      <small>Use commas or ranges. Example: 2, b, 10-12</small>

      <label for="sold_plots">Sold Plot Numbers</label>
      <input type="text" id="sold_plots" name="sold_plots" placeholder="e.g. 5, c, 20-22">
      <small>Use commas or ranges. Example: 5, c, 20-22</small>

      <button type="submit">Add Plots</button>
    </form>
  </div>
<?php
$page_content = ob_get_clean();
include 'layout.php';
?>
