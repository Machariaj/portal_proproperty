<?php
session_start();
include 'activity_log.php';
include 'db_connection.php';
include 'zoho_functions.php';

// Log page access
logPageAccess('bulk_sell');

$role = $_SESSION['role'] ?? null;
$user_id = $_SESSION['user_id'] ?? null;
$agentName = $_SESSION['user_name'] ?? '';

if (!$user_id || !in_array($role, ['agent', 'admin'])) {
  header("Location: index.php");
  exit;
}

if ($_SERVER['REQUEST_METHOD'] !== 'POST' || !isset($_POST['selected_plots']) || empty($_POST['selected_plots']) || $_POST['bulk_action'] !== 'sell') {
  header("Location: index.php");
  exit;
}

$selected_plots = array_map('intval', $_POST['selected_plots']);
$estate_id = intval($_POST['estate_id']);
$status = $_POST['status'];

// Validate plots are available/booked and on same estate
$allowed_statuses = $status === 'available' ? ['available'] : ['booked'];
$status_condition = $status === 'available' ? "'available'" : "'booked'";

$placeholders = str_repeat('?,', count($selected_plots) - 1) . '?';
$types = str_repeat('i', count($selected_plots));
$sql = "SELECT id, plot_number FROM prop_plots WHERE id IN ($placeholders) AND estate_id = ? AND status = $status_condition";
$params = array_merge($selected_plots, [$estate_id]);
$types .= 'i';

$stmt = $conn->prepare($sql);
$stmt->bind_param($types, ...$params);
$stmt->execute();
$valid_plots = $stmt->get_result()->fetch_all(MYSQLI_ASSOC);

if (count($valid_plots) !== count($selected_plots)) {
  echo "<script>alert('Some selected plots are not in the correct status.'); window.history.back();</script>";
  exit;
}

// Fetch estate name
$estate = $conn->query("SELECT name FROM prop_estates WHERE id = $estate_id")->fetch_assoc();
$estate_name = $estate['name'];

if ($_SERVER['REQUEST_METHOD'] === 'POST' && isset($_POST['buyer_name'])) {
  // Process selling
  $buyer_name = $conn->real_escape_string($_POST['buyer_name']);
  $buyer_phone = $conn->real_escape_string($_POST['buyer_phone']);
  $buyer_email = $conn->real_escape_string($_POST['buyer_email']);
  $amount = floatval($_POST['amount']);

  $conn->begin_transaction();
  try {
    $stmt = $conn->prepare("INSERT INTO prop_sales (plot_id, estate_id, buyer_name, buyer_phone, buyer_email, agent_name, amount, date_sold, sale_date) VALUES (?, ?, ?, ?, ?, ?, ?, NOW(), NOW())");

    foreach ($valid_plots as $plot) {
      $stmt->bind_param("iissssdi", $plot['id'], $estate_id, $buyer_name, $buyer_phone, $buyer_email, $agentName, $amount);

      // If selling booked plot, delete booking first
      if ($status === 'booked') {
        $delete_booking = $conn->prepare("DELETE FROM prop_bookings WHERE plot_id = ? AND status = 'active'");
        $delete_booking->bind_param("i", $plot['id']);
        $delete_booking->execute();
      }

      $stmt->execute();

      // Update plot status
      $update_stmt = $conn->prepare("UPDATE prop_plots SET status = 'sold' WHERE id = ?");
      $update_stmt->bind_param("i", $plot['id']);
      $update_stmt->execute();

      // Log
      logDataModification('prop_sales', 'INSERT', 'new', [
        'buyer_name' => $buyer_name,
        'buyer_phone' => $buyer_phone,
        'buyer_email' => $buyer_email,
        'amount' => $amount,
        'plot_id' => $plot['id'],
        'agent_id' => $user_id
      ]);
      logDataModification('prop_plots', 'UPDATE', $plot['id'], ['status' => 'sold']);
      if ($status === 'booked') {
        logDataModification('prop_bookings', 'DELETE', $plot['id'], ['bulk_sell' => true]);
      }
    }

    // Handle payment proof uploads
    if (isset($_FILES['payment_proof'])) {
      $uploaded_files = [];
      $files = $_FILES['payment_proof'];
      $file_count = count($files['name']);
      for ($i = 0; $i < $file_count; $i++) {
        if ($files['error'][$i] == 0) {
          $file_name = uniqid() . '_' . basename($files['name'][$i]);
          $target_path = UPLOAD_DIR . $file_name;
          if (move_uploaded_file($files['tmp_name'][$i], $target_path)) {
            $uploaded_files[] = $file_name;
          } else {
            error_log("Failed to upload payment proof: $file_name");
          }
        }
      }
      if (!empty($uploaded_files)) {
        logDataModification('uploads', 'UPLOAD', 'payment_proofs', ['files' => implode(',', $uploaded_files), 'plots' => implode(',', array_column($valid_plots, 'id'))]);
      }
    }

    $conn->commit();

    // Create customer in Zoho Books
    $customerData = [
        'contact_name' => $buyer_name,
        'contact_type' => 'customer',
        'email' => $buyer_email,
        'phone' => $buyer_phone
    ];

    // Prepare notes with plot details
    $plotNumbers = array_column($valid_plots, 'plot_number');
    $plotList = implode(', ', $plotNumbers);
    $notes = "Estate: $estate_name, Plots: $plotList, Sale Date: " . date('Y-m-d');

    // Prepare contact persons with buyer details (primary) and agent
    $contactPersons = [];
    // Primary contact person with buyer details
    $contactPersons[] = [
        'first_name' => explode(' ', $buyer_name)[0] ?? $buyer_name,
        'last_name' => explode(' ', $buyer_name, 2)[1] ?? '',
        'salutation' => '',
        'email' => $buyer_email,
        'mobile' => $buyer_phone,
        'is_primary_contact' => true
    ];
    // Agent as additional contact person
    $nameParts = explode(' ', $agentName, 2);
    $firstName = $nameParts[0] ?? '';
    $lastName = $nameParts[1] ?? '';
    $contactPersons[] = [
        'first_name' => $firstName,
        'last_name' => $lastName,
        'salutation' => '',
        'email' => '', // Agent email not available
        'mobile' => ''  // Agent phone not available
    ];

    $customer_id = createZohoBooksCustomer($customerData, $notes, $contactPersons);
    if ($customer_id) {
        logZohoActivity('customer_create', 'success', "Customer ID: $customer_id, Buyer: $buyer_name");
    } else {
        logZohoActivity('customer_create', 'failed', "Buyer: $buyer_name");
    }

    echo "<script>alert('Plots sold successfully!'); window.location.href='plots.php?estate_id=$estate_id&status=$status';</script>";
    exit;
  } catch (Exception $e) {
    $conn->rollback();
    echo "<script>alert('Error selling plots: " . $e->getMessage() . "'); window.history.back();</script>";
    exit;
  }
}

$page_title = "Bulk Sell Plots - " . htmlspecialchars($estate_name);

ob_start();
?>
<div class="top-bar">
  <h1>Bulk Sell Plots in <?php echo htmlspecialchars($estate_name); ?></h1>
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
  <form method="POST" enctype="multipart/form-data">
    <input type="hidden" name="bulk_action" value="sell">
    <input type="hidden" name="estate_id" value="<?php echo $estate_id; ?>">
    <input type="hidden" name="status" value="<?php echo $status; ?>">
    <?php foreach ($selected_plots as $plot_id): ?>
      <input type="hidden" name="selected_plots[]" value="<?php echo $plot_id; ?>">
    <?php endforeach; ?>

    <label>Buyer Name:</label>
    <input type="text" name="buyer_name" required>

    <label>Buyer Phone:</label>
    <input type="text" name="buyer_phone" required>

    <label>Buyer Email:</label>
    <input type="email" name="buyer_email">

    <label>Sale Amount (Ksh):</label>
    <input type="number" name="amount" step="0.01" required>

    <label>Payment Proof (upload multiple files if needed):</label>
    <input type="file" name="payment_proof[]" accept="image/*" multiple required>

    <button type="submit">Confirm Bulk Sale</button>
  </form>
</div>

<a href="plots.php?estate_id=<?php echo $estate_id; ?>&status=<?php echo $status; ?>" class="edit-btn">← Back to Plots</a>
<?php
$page_content = ob_get_clean();
include 'layout.php';
?>