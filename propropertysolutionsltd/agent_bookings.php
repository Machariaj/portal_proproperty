<?php
session_start();
include 'db_connection.php';

if (!isset($_SESSION['user_id']) || $_SESSION['role'] != 'agent') {
  header("Location: index.php");
  exit;
}

$agentName = $_SESSION['user_name'] ?? '';

// Get filters
$estate_filter = isset($_GET['estate_id']) ? intval($_GET['estate_id']) : 0;
$search = isset($_GET['search']) ? trim($_GET['search']) : '';

// Pagination setup
$limit = 10; // Records per page
$page = isset($_GET['page']) && $_GET['page'] > 0 ? intval($_GET['page']) : 1;
$offset = ($page - 1) * $limit;

// Fetch estates for dropdown
$estates_query = $conn->query("SELECT id, name FROM prop_estates ORDER BY name");
$estates = [];
while ($row = $estates_query->fetch_assoc()) {
  $estates[] = $row;
}

// Handle make available request
if (isset($_GET['make_available']) && is_numeric($_GET['make_available'])) {
  $plot_id = intval($_GET['make_available']);

  // Verify the plot belongs to this agent
  $verify_stmt = $conn->prepare("SELECT b.id FROM prop_bookings b WHERE b.plot_id = ? AND b.agent_name = ? AND b.status = 'active'");
  $verify_stmt->bind_param("is", $plot_id, $agentName);
  $verify_stmt->execute();
  if ($verify_stmt->get_result()->num_rows > 0) {
    // Update booking status to inactive
    $update_booking = $conn->prepare("UPDATE prop_bookings SET status = 'cancelled' WHERE plot_id = ? AND agent_name = ? AND status = 'active'");
    $update_booking->bind_param("is", $plot_id, $agentName);
    $update_booking->execute();

    // Update plot status to available
    $update_plot = $conn->prepare("UPDATE prop_plots SET status = 'available' WHERE id = ?");
    $update_plot->bind_param("i", $plot_id);
    $update_plot->execute();

    echo "<script>alert('Plot made available successfully!'); window.location.href='agent_bookings.php';</script>";
    exit;
  }
}

// Build query
$sql = "SELECT b.buyer_name AS client_name, b.buyer_phone AS phone, b.buyer_email AS email, b.notes, p.plot_number, e.name AS estate_name, b.date_booked, p.id AS plot_id
        FROM prop_bookings b
        JOIN prop_plots p ON p.id = b.plot_id
        JOIN prop_estates e ON e.id = p.estate_id
        WHERE b.status = 'active' AND b.agent_name = ?";

$params = [$agentName];
$types = "s";

if ($estate_filter > 0) {
  $sql .= " AND p.estate_id = ?";
  $params[] = $estate_filter;
  $types .= "i";
}

if (!empty($search)) {
  $sql .= " AND (b.buyer_name LIKE ? OR p.plot_number LIKE ?)";
  $params[] = "%" . $search . "%";
  $params[] = "%" . $search . "%";
  $types .= "ss";
}

$sql .= " ORDER BY b.date_booked DESC LIMIT ?, ?";

$params[] = $offset;
$params[] = $limit;
$types .= "ii";

$stmt = $conn->prepare($sql);
$stmt->bind_param($types, ...$params);
$stmt->execute();
$result = $stmt->get_result();

// Count total records for pagination
$count_sql = "SELECT COUNT(*) as total FROM prop_bookings b
              JOIN prop_plots p ON p.id = b.plot_id
              JOIN prop_estates e ON e.id = p.estate_id
              WHERE b.status = 'active' AND b.agent_name = ?";

$count_params = [$agentName];
$count_types = "s";

if ($estate_filter > 0) {
  $count_sql .= " AND p.estate_id = ?";
  $count_params[] = $estate_filter;
  $count_types .= "i";
}

if (!empty($search)) {
  $count_sql .= " AND (b.buyer_name LIKE ? OR p.plot_number LIKE ?)";
  $count_params[] = "%" . $search . "%";
  $count_params[] = "%" . $search . "%";
  $count_types .= "ss";
}

$count_stmt = $conn->prepare($count_sql);
$count_stmt->bind_param($count_types, ...$count_params);
$count_stmt->execute();
$total_records = $count_stmt->get_result()->fetch_assoc()['total'];
$total_pages = ceil($total_records / $limit);

$page_title = 'Your Booked Plots - ProProperty';

ob_start();
?>
  <div class="top-bar">
    <h1>Your Booked Plots</h1>
  </div>

  <div class="card" style="max-width: 800px;">
    <form method="GET" style="display: flex; gap: 10px; align-items: center;">
      <label for="estate_id">Filter by Estate:</label>
      <select name="estate_id" id="estate_id" onchange="this.form.submit()">
        <option value="0">All Estates</option>
        <?php foreach ($estates as $estate): ?>
          <option value="<?php echo $estate['id']; ?>" <?php echo ($estate_filter == $estate['id']) ? 'selected' : ''; ?>>
            <?php echo htmlspecialchars($estate['name']); ?>
          </option>
        <?php endforeach; ?>
      </select>

      <label for="search">Search by Client Name or Plot Number:</label>
      <input type="text" name="search" id="search" placeholder="Search..." value="<?php echo htmlspecialchars($search); ?>">
      <button type="submit">🔍 Search</button>
    </form>
  </div>

  <div class="card">
    <?php if ($result->num_rows > 0): ?>
      <div class="table-responsive">
        <table>
          <thead>
            <tr>
              <th>Client Name</th>
              <th>Phone</th>
              <th>Email</th>
              <th>Notes</th>
              <th>Plot Number</th>
              <th>Estate</th>
              <th>Date Booked</th>
              <th>Actions/Make</th>
            </tr>
          </thead>
          <tbody>
            <?php while ($row = $result->fetch_assoc()): ?>
              <tr>
                <td><?php echo htmlspecialchars($row['client_name']); ?></td>
                <td><?php echo htmlspecialchars($row['phone']); ?></td>
                <td><?php echo htmlspecialchars($row['email']); ?></td>
                <td><?php echo htmlspecialchars($row['notes']); ?></td>
                <td><?php echo htmlspecialchars($row['plot_number']); ?></td>
                <td><?php echo htmlspecialchars($row['estate_name']); ?></td>
                <td><?php echo htmlspecialchars($row['date_booked']); ?></td>
                <td>
                  <a href="?make_available=<?php echo htmlspecialchars($row['plot_id']); ?>" class="delete-btn" onclick="return confirm('Are you sure you want to make this plot available?')">Available</a>
                  <a href="select_booking_type.php?plot_id=<?php echo htmlspecialchars($row['plot_id']); ?>" class="edit-btn">KYC</a>
                </td>
              </tr>
            <?php endwhile; ?>
          </tbody>
        </table>
      </div>
    <?php else: ?>
      <p>No booked plots found.</p>
    <?php endif; ?>
  </div>

  <?php if ($total_pages > 1): ?>
    <div class="pagination">
      <?php if ($page > 1): ?>
        <a href="?page=<?= $page - 1 ?>&estate_id=<?= $estate_filter ?>&search=<?= urlencode($search) ?>">⬅ Prev</a>
      <?php endif; ?>

      <?php for ($i = 1; $i <= $total_pages; $i++): ?>
        <a href="?page=<?= $i ?>&estate_id=<?= $estate_filter ?>&search=<?= urlencode($search) ?>" class="<?= ($i == $page ? 'active' : '') ?>"><?= $i ?></a>
      <?php endfor; ?>

      <?php if ($page < $total_pages): ?>
        <a href="?page=<?= $page + 1 ?>&estate_id=<?= $estate_filter ?>&search=<?= urlencode($search) ?>">Next ➡</a>
      <?php endif; ?>
    </div>
  <?php endif; ?>

  <a href="agent_dashboard.php" class="edit-btn">← Back to Dashboard</a>
<?php
$page_content = ob_get_clean();
include 'layout.php';
?>
