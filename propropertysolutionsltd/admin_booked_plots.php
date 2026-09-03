<?php
error_reporting(E_ALL);
ini_set('display_errors', 0);
session_start();

if (!isset($_SESSION['user_id']) || $_SESSION['role'] !== 'admin') {
  header("Location: index.php");
  exit;
}

include 'db_connection.php';

// Get filters
$estate_filter = isset($_GET['estate_id']) ? intval($_GET['estate_id']) : 0;
$booking_type_filter = isset($_GET['booking_type']) ? $_GET['booking_type'] : '';
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

// Build query with search and pagination
$where_conditions = ["b.status = 'active'"];
$params = [];
$types = '';

if ($estate_filter > 0) {
  $where_conditions[] = "p.estate_id = ?";
  $params[] = $estate_filter;
  $types .= 'i';
}

if (!empty($booking_type_filter)) {
  if ($booking_type_filter === 'reserve') {
    $where_conditions[] = "b.notes NOT LIKE '%Deposit Timing%'";
  } elseif ($booking_type_filter === 'deposit') {
    $where_conditions[] = "b.notes LIKE '%Deposit Timing: before%'";
  } elseif ($booking_type_filter === 'sa') {
    $where_conditions[] = "b.notes LIKE '%Deposit Timing: after%'";
  }
}

if (!empty($search)) {
  $search_condition = "(b.buyer_name LIKE ? OR b.buyer_phone LIKE ? OR b.buyer_email LIKE ? OR b.notes LIKE ? OR b.agent_name LIKE ? OR p.plot_number LIKE ? OR e.name LIKE ?)";
  $search_param = "%$search%";
  $where_conditions[] = $search_condition;
  $params = array_merge($params, array_fill(0, 7, $search_param));
  $types .= str_repeat('s', 7);
}

$where_clause = implode(" AND ", $where_conditions);

// Count total records
try {
  $count_sql = "SELECT COUNT(*) as total FROM prop_bookings b
                JOIN prop_plots p ON p.id = b.plot_id
                JOIN prop_estates e ON e.id = p.estate_id
                WHERE $where_clause";
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
$sql = "SELECT b.id AS booking_id, b.plot_id, b.buyer_name AS client_name, b.buyer_phone AS phone, b.buyer_email AS email, b.notes, b.agent_name, p.plot_number, e.name AS estate_name, b.date_booked
        FROM prop_bookings b
        JOIN prop_plots p ON p.id = b.plot_id
        JOIN prop_estates e ON e.id = p.estate_id
        WHERE $where_clause
        ORDER BY b.date_booked DESC
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

$page_title = 'Booked Plots - ProProperty';

ob_start();
?>
  <div class="top-bar">
    <h1>All Booked Plots</h1>
    <button id="toggle-columns" class="edit-btn" style="position: absolute; top: 10px; right: 10px;">Manage Columns</button>
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

  <!-- Manage Columns -->
  <div id="manage-columns" class="card" style="display: none; position: absolute; top: 50px; right: 10px; z-index: 1000;">
    <h3>Manage Columns</h3>
    <div id="column-toggles" style="display: flex; flex-wrap: wrap; gap: 10px;">
      <label><input type="checkbox" class="column-toggle" data-column="client_name" checked> Client Name</label>
      <label><input type="checkbox" class="column-toggle" data-column="phone" checked> Phone</label>
      <label><input type="checkbox" class="column-toggle" data-column="email" checked> Email</label>
      <label><input type="checkbox" class="column-toggle" data-column="notes" checked> Notes</label>

      <label><input type="checkbox" class="column-toggle" data-column="agent_name" checked> Agent</label>
      <label><input type="checkbox" class="column-toggle" data-column="plot_number" checked> Plot Number</label>
      <label><input type="checkbox" class="column-toggle" data-column="estate_name" checked> Estate</label>
      <label><input type="checkbox" class="column-toggle" data-column="date_booked" checked> Date Booked</label>
    </div>
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
              <th>Agent</th>
              <th>Plot Number</th>
              <th>Estate</th>
              <th>Booking Type</th>
              <th>Date Booked</th>
              <th>Actions/Make</th>
            </tr>
          </thead>
          <tbody>
            <?php while ($row = $result->fetch_assoc()): ?>
              <tr<?php echo ((time() - strtotime($row['date_booked'])) > 7 * 24 * 60 * 60) ? ' style="background-color:#e8f0fe;"' : ''; ?>>
                <td><?php echo htmlspecialchars($row['client_name']); ?></td>
                <td><?php echo htmlspecialchars($row['phone']); ?></td>
                <td><?php echo htmlspecialchars($row['email']); ?></td>
                <td><?php echo htmlspecialchars($row['notes']); ?></td>
                <td><?php echo htmlspecialchars($row['agent_name']); ?></td>
                <td><?php echo htmlspecialchars($row['plot_number']); ?></td>
                <td><?php echo htmlspecialchars($row['estate_name']); ?></td>
                <td><?php
                  if (strpos($row['notes'], 'Deposit Timing: before') !== false) {
                    echo 'Booking with deposit';
                  } elseif (strpos($row['notes'], 'Deposit Timing: after') !== false) {
                    echo 'Signing sale agreement first';
                  } else {
                    echo 'Reserve';
                  }
                ?></td>
                <td><?php echo htmlspecialchars($row['date_booked']); ?></td>
                <td>
                  <button onclick="makeAvailable(<?php echo $row['booking_id']; ?>, <?php echo $row['plot_id']; ?>, 'booked')" class="edit-btn" style="background-color: #28a745;">Available</button>
                  <button onclick="makeSaSigned(<?php echo $row['booking_id']; ?>, <?php echo $row['plot_id']; ?>, 'booked')" class="edit-btn" style="background-color: #dc3545; color: white;">Signed</button>
                  <button onclick="makeSold(<?php echo $row['booking_id']; ?>, <?php echo $row['plot_id']; ?>, 'booked')" class="edit-btn" style="background-color: #28a745;">Sold</button>
                  <a href="select_booking_type.php?plot_id=<?php echo $row['plot_id']; ?>" class="edit-btn">KYC</a>
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

  <!-- Pagination -->
  <?php if ($total_pages > 1): ?>
    <div class="pagination">
      <?php if ($page > 1): ?>
        <a href="?estate_id=<?php echo $estate_filter; ?>&booking_type=<?php echo urlencode($booking_type_filter); ?>&search=<?php echo urlencode($search); ?>&page=<?php echo $page - 1; ?>">⬅ Prev</a>
      <?php endif; ?>

      <?php for ($i = max(1, $page - 2); $i <= min($total_pages, $page + 2); $i++): ?>
        <a href="?estate_id=<?php echo $estate_filter; ?>&booking_type=<?php echo urlencode($booking_type_filter); ?>&search=<?php echo urlencode($search); ?>&page=<?php echo $i; ?>" class="<?php echo ($i == $page ? 'active' : ''); ?>"><?php echo $i; ?></a>
      <?php endfor; ?>

      <?php if ($page < $total_pages): ?>
        <a href="?estate_id=<?php echo $estate_filter; ?>&booking_type=<?php echo urlencode($booking_type_filter); ?>&search=<?php echo urlencode($search); ?>&page=<?php echo $page + 1; ?>">Next ➡</a>
      <?php endif; ?>
    </div>
  <?php endif; ?>

  <a href="admin_dashboard.php" class="edit-btn">← Back to Dashboard</a>

  <script>
    // Toggle manage columns visibility
    document.getElementById('toggle-columns').addEventListener('click', function() {
      const manageColumns = document.getElementById('manage-columns');
      manageColumns.style.display = manageColumns.style.display === 'none' ? 'block' : 'none';
    });

    // Column toggling functionality
    document.addEventListener('DOMContentLoaded', function() {
      const toggles = document.querySelectorAll('.column-toggle');
      const table = document.querySelector('table');

      if (table) {
        const headers = table.querySelectorAll('th');
        const rows = table.querySelectorAll('tbody tr');

        toggles.forEach(toggle => {
          toggle.addEventListener('change', function() {
            const column = this.dataset.column;
            const index = Array.from(headers).findIndex(th => th.textContent.toLowerCase().replace(/\s+/g, '_') === column);

            if (index !== -1) {
              headers[index].style.display = this.checked ? '' : 'none';
              rows.forEach(row => {
                const cells = row.querySelectorAll('td');
                if (cells[index]) {
                  cells[index].style.display = this.checked ? '' : 'none';
                }
              });
            }
          });
        });
      }

      // Handle sidebar booking type filter
      const sidebarSelect = document.getElementById('booking_type_sidebar');
      if (sidebarSelect) {
        sidebarSelect.addEventListener('change', function() {
          const url = new URL(window.location);
          url.searchParams.set('booking_type', this.value);
          window.location.href = url.toString();
        });
      }
    });

    // Function to make plot available
    function makeAvailable(bookingId, plotId, type) {
      if (confirm('Are you sure you want to make this plot available? This will remove the booking record.')) {
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

    // Function to mark plot as SA Signed
    function makeSaSigned(bookingId, plotId, type) {
      const dateSigned = prompt('Enter the date the sale agreement was signed (YYYY-MM-DD):', new Date().toISOString().split('T')[0]);
      if (dateSigned && confirm('Are you sure you want to mark this plot as SA Signed with date ' + dateSigned + '?')) {
        fetch('update_plot_status.php', {
          method: 'POST',
          headers: {
            'Content-Type': 'application/x-www-form-urlencoded',
          },
          body: 'action=make_sa_signed_from_booking&booking_id=' + bookingId + '&plot_id=' + plotId + '&type=' + type + '&date_signed=' + encodeURIComponent(dateSigned)
        })
        .then(response => response.json())
        .then(data => {
          if (data.success) {
            alert('Plot marked as SA Signed successfully.');
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
