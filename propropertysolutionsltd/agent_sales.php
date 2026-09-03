<?php
session_start();
include 'db_connection.php'; // Include database connection

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

// Build query
$sql = "SELECT s.buyer_name AS client_name, s.buyer_phone AS phone, s.buyer_email AS email, p.plot_number, e.name AS estate_name, s.amount, s.date_sold
        FROM prop_sales s
        JOIN prop_plots p ON p.id = s.plot_id
        JOIN prop_estates e ON e.id = p.estate_id
        WHERE s.agent_name = ?";

$params = [$agentName];
$types = "s";

if ($estate_filter > 0) {
  $sql .= " AND p.estate_id = ?";
  $params[] = $estate_filter;
  $types .= "i";
}

if (!empty($search)) {
  $sql .= " AND (s.buyer_name LIKE ? OR p.plot_number LIKE ?)";
  $params[] = "%" . $search . "%";
  $params[] = "%" . $search . "%";
  $types .= "ss";
}

$sql .= " ORDER BY s.date_sold DESC LIMIT ?, ?";

$params[] = $offset;
$params[] = $limit;
$types .= "ii";

$stmt = $conn->prepare($sql);
$stmt->bind_param($types, ...$params);
$stmt->execute();
$result = $stmt->get_result();

// Count total records for pagination
$count_sql = "SELECT COUNT(*) as total FROM prop_sales s
              JOIN prop_plots p ON p.id = s.plot_id
              JOIN prop_estates e ON e.id = p.estate_id
              WHERE s.agent_name = ?";

$count_params = [$agentName];
$count_types = "s";

if ($estate_filter > 0) {
  $count_sql .= " AND p.estate_id = ?";
  $count_params[] = $estate_filter;
  $count_types .= "i";
}

if (!empty($search)) {
  $count_sql .= " AND (s.buyer_name LIKE ? OR p.plot_number LIKE ?)";
  $count_params[] = "%" . $search . "%";
  $count_params[] = "%" . $search . "%";
  $count_types .= "ss";
}

$count_stmt = $conn->prepare($count_sql);
$count_stmt->bind_param($count_types, ...$count_params);
$count_stmt->execute();
$total_records = $count_stmt->get_result()->fetch_assoc()['total'];
$total_pages = ceil($total_records / $limit);

$page_title = 'Your Sold Plots - ProProperty';

ob_start();
?>
  <div class="top-bar">
    <h1>Your Sold Plots</h1>
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
              <th>Plot Number</th>
              <th>Estate</th>
              <th>Amount (Ksh)</th>
              <th>Date Sold</th>
            </tr>
          </thead>
          <tbody>
            <?php while ($row = $result->fetch_assoc()): ?>
              <tr>
                <td><?php echo htmlspecialchars($row['client_name']); ?></td>
                <td><?php echo htmlspecialchars($row['phone']); ?></td>
                <td><?php echo htmlspecialchars($row['email']); ?></td>
                <td><?php echo htmlspecialchars($row['plot_number']); ?></td>
                <td><?php echo htmlspecialchars($row['estate_name']); ?></td>
                <td><?php echo number_format((float)$row['amount']); ?></td>
                <td><?php echo htmlspecialchars($row['date_sold']); ?></td>
              </tr>
            <?php endwhile; ?>
          </tbody>
        </table>
      </div>
    <?php else: ?>
      <p>No sold plots found.</p>
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
