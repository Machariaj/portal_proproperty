<?php
ini_set('display_errors', 1);
error_reporting(E_ALL);
session_start();

if (!isset($_SESSION['user_id']) || $_SESSION['role'] !== 'admin') {
  header("Location: index.php");
  exit;
}

include 'db_connection.php';

// Get filters
$estate_filter = isset($_GET['estate_id']) ? intval($_GET['estate_id']) : 0;
$search = isset($_GET['search']) ? trim($_GET['search']) : '';
$page = isset($_GET['page']) ? max(1, intval($_GET['page'])) : 1;
$per_page = 10;
$offset = ($page - 1) * $per_page;

// Fetch estates for dropdown
try {
  $estates_query = $conn->query("SELECT id, name FROM prop_estates ORDER BY name");
  if (!$estates_query) {
    throw new Exception("Query failed: " . $conn->error);
  }
  $estates = [];
  while ($row = $estates_query->fetch_assoc()) {
    $estates[] = $row;
  }
} catch (Exception $e) {
  error_log("Estates query error: " . $e->getMessage());
  die("Error loading estates.");
}

// Build query with search and pagination for SA Signed plots
$where_conditions = ["p.status = 'sa_signed'"];
$params = [];
$types = '';

if ($estate_filter > 0) {
  $where_conditions[] = "p.estate_id = ?";
  $params[] = $estate_filter;
  $types .= 'i';
}

if (!empty($search)) {
  $search_condition = "(b.buyer_name LIKE ? OR b.buyer_phone LIKE ? OR b.buyer_email LIKE ? OR b.agent_name LIKE ? OR p.plot_number LIKE ? OR e.name LIKE ?)";
  $search_param = "%$search%";
  $where_conditions[] = $search_condition;
  $params = array_merge($params, array_fill(0, 6, $search_param));
  $types .= str_repeat('s', 6);
}

$where_clause = "WHERE " . implode(" AND ", $where_conditions);

// Count total records
try {
  $count_sql = "SELECT COUNT(*) as total FROM prop_plots p
                JOIN prop_estates e ON e.id = p.estate_id
                LEFT JOIN prop_bookings b ON b.plot_id = p.id
                $where_clause";
  $count_stmt = $conn->prepare($count_sql);
  if (!$count_stmt) {
    throw new Exception("Prepare failed: " . $conn->error);
  }
  if (!empty($params)) {
    $count_stmt->bind_param($types, ...$params);
  }
  $count_stmt->execute();
  $total_records = $count_stmt->get_result()->fetch_assoc()['total'];
  $total_pages = ceil($total_records / $per_page);
} catch (Exception $e) {
  error_log("Count query error: " . $e->getMessage());
  die("Error loading data.");
}

// Fetch paginated results
$sql = "SELECT b.id AS booking_id, p.id AS plot_id, b.buyer_name AS client_name, b.buyer_phone AS phone, b.buyer_email AS email, b.agent_name, p.plot_number, p.status, e.name AS estate_name, COALESCE(b.date_signed, b.date_booked) AS date_signed, b.notes
        FROM prop_plots p
        JOIN prop_estates e ON e.id = p.estate_id
        LEFT JOIN prop_bookings b ON b.plot_id = p.id
        $where_clause
        ORDER BY COALESCE(b.date_signed, b.date_booked) DESC
        LIMIT ? OFFSET ?";

$params[] = $per_page;
$params[] = $offset;
$types .= 'ii';

try {
  $stmt = $conn->prepare($sql);
  if (!$stmt) {
    throw new Exception("Prepare failed: " . $conn->error);
  }
  $stmt->bind_param($types, ...$params);
  $stmt->execute();
  $result = $stmt->get_result();
} catch (Exception $e) {
  error_log("Fetch query error: " . $e->getMessage());
  die("Error loading data.");
}

$page_title = 'Sale Agreement Signed Plots';

ob_start();
?>
  <div class="top-bar">
    <h1>Sale Agreement Signed Plots</h1>
  </div>

  <div class="card" style="max-width: 600px;">
    <form method="GET" style="display: flex; gap: 10px; align-items: center;">
      <div>
        <label for="estate_id">Filter by Estate:</label>
        <select name="estate_id" id="estate_id" onchange="this.form.submit()">
          <option value="0">All Estates</option>
          <?php foreach ($estates as $estate): ?>
            <option value="<?php echo $estate['id']; ?>" <?php echo ($estate_filter == $estate['id']) ? 'selected' : ''; ?>>
              <?php echo htmlspecialchars($estate['name']); ?>
            </option>
          <?php endforeach; ?>
        </select>
      </div>
      <div style="display: flex; align-items: center;">
        <label for="search" style="margin-right: 5px;">Search:</label>
        <input type="text" name="search" value="<?php echo htmlspecialchars($search); ?>" placeholder="Search..." style="margin-right: 5px;">
        <button type="submit">🔍</button>
      </div>
    </form>
  </div>

  <div class="card">
    <?php if ($result->num_rows > 0): ?>
      <div class="table-responsive" style="overflow-x: auto; max-width: 100%;">
        <table style="min-width: 1000px;">
          <thead>
            <tr>
              <th>Client Name</th>
              <th>Phone</th>
              <th>Email</th>
              <th>Agent</th>
              <th>Plot Number</th>
              <th>Estate</th>
              <th>Date Signed</th>
              <th>Notes</th>
              <th>Actions/Make</th>
            </tr>
          </thead>
          <tbody>
            <?php while ($row = $result->fetch_assoc()): ?>
              <tr class="sa_signed">
                <td><?php echo htmlspecialchars($row['client_name'] ?? ''); ?></td>
                <td><?php echo htmlspecialchars($row['phone'] ?? ''); ?></td>
                <td><?php echo htmlspecialchars($row['email'] ?? ''); ?></td>
                <td><?php echo htmlspecialchars($row['agent_name'] ?? ''); ?></td>
                <td><?php echo htmlspecialchars($row['plot_number'] ?? ''); ?></td>
                <td><?php echo htmlspecialchars($row['estate_name'] ?? ''); ?></td>
                <td><?php echo htmlspecialchars($row['date_signed'] ?? ''); ?></td>
                <td><?php echo htmlspecialchars($row['notes'] ?? ''); ?></td>
                <td>
                  <div style="display: flex; flex-wrap: wrap; gap: 5px;">
                    <button onclick="makeAvailable(<?php echo $row['booking_id'] ?? 'null'; ?>, <?php echo $row['plot_id']; ?>, 'sa_signed')" class="edit-btn" style="background-color: #28a745; white-space: nowrap;">Available</button>
                    <?php if ($row['booking_id']): ?>
                      <button onclick="makeSold(<?php echo $row['booking_id']; ?>, <?php echo $row['plot_id']; ?>, 'sa_signed')" class="edit-btn" style="background-color: #28a745; white-space: nowrap;">Sold</button>
                    <?php endif; ?>
                  </div>
                </td>
              </tr>
            <?php endwhile; ?>
          </tbody>
        </table>
      </div>
    <?php else: ?>
      <p>No SA Signed plots found.</p>
    <?php endif; ?>
  </div>

  <!-- Pagination -->
  <?php if ($total_pages > 1): ?>
    <div class="pagination">
      <?php if ($page > 1): ?>
        <a href="?estate_id=<?php echo $estate_filter; ?>&search=<?php echo urlencode($search); ?>&page=<?php echo $page - 1; ?>">⬅ Prev</a>
      <?php endif; ?>

      <?php for ($i = max(1, $page - 2); $i <= min($total_pages, $page + 2); $i++): ?>
        <a href="?estate_id=<?php echo $estate_filter; ?>&search=<?php echo urlencode($search); ?>&page=<?php echo $i; ?>" class="<?php echo ($i == $page ? 'active' : ''); ?>"><?php echo $i; ?></a>
      <?php endfor; ?>

      <?php if ($page < $total_pages): ?>
        <a href="?estate_id=<?php echo $estate_filter; ?>&search=<?php echo urlencode($search); ?>&page=<?php echo $page + 1; ?>">Next ➡</a>
      <?php endif; ?>
    </div>
  <?php endif; ?>

  <a href="admin_dashboard.php" class="edit-btn">← Back to Dashboard</a>

  <style>
    .sa_signed {
      background-color: #f8d7da;
    }

    /* Responsive button styling */
    @media (max-width: 768px) {
      .table-responsive table {
        font-size: 14px;
      }

      .edit-btn {
        font-size: 12px;
        padding: 6px 10px;
      }
    }

    @media (max-width: 520px) {
      .table-responsive table {
        font-size: 12px;
      }

      .edit-btn {
        font-size: 11px;
        padding: 5px 8px;
        width: 100%;
      }

      td > div {
        flex-direction: column !important;
      }
    }
  </style>

  <script>
    // Function to make plot available
    function makeAvailable(bookingId, plotId, type) {
      if (confirm('Are you sure you want to make this plot available?')) {
        fetch('update_plot_status.php', {
          method: 'POST',
          headers: {
            'Content-Type': 'application/x-www-form-urlencoded',
          },
          body: 'action=make_available&booking_id=' + bookingId + '&plot_id=' + plotId + '&type=' + type
        })
        .then(response => response.json())
        .then(data => {
          if (data.success) {
            alert('Plot status updated successfully.');
            location.reload();
          } else {
            alert('Error updating plot status: ' + data.message);
          }
        })
        .catch(error => {
          console.error('Error:', error);
          alert('An error occurred while updating the plot status.');
        });
      }
    }

    // Function to mark plot as Sold
    function makeSold(bookingId, plotId, type) {
      if (confirm('Are you sure you want to mark this plot as Sold?')) {
        fetch('update_plot_status.php', {
          method: 'POST',
          headers: {
            'Content-Type': 'application/x-www-form-urlencoded',
          },
          body: 'action=make_sold_from_booking&booking_id=' + bookingId + '&plot_id=' + plotId + '&type=' + type
        })
        .then(response => response.json())
        .then(data => {
          if (data.success) {
            alert('Plot marked as Sold successfully.');
            location.reload();
          } else {
            alert('Error updating plot status: ' + data.message);
          }
        })
        .catch(error => {
          console.error('Error:', error);
          alert('An error occurred while updating the plot status.');
        });
      }
    }
  </script>
<?php
$page_content = ob_get_clean();
include 'layout.php';
?>


