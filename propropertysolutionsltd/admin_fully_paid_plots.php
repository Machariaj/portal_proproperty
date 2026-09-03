<?php
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
$estates_query = $conn->query("SELECT id, name FROM prop_estates ORDER BY name");
$estates = [];
while ($row = $estates_query->fetch_assoc()) {
  $estates[] = $row;
}

// Build query with search and pagination
$where_conditions = ["p.status = 'fully paid'"];
$params = [];
$types = '';

if ($estate_filter > 0) {
  $where_conditions[] = "p.estate_id = ?";
  $params[] = $estate_filter;
  $types .= 'i';
}

if (!empty($search)) {
  $search_condition = "(s.buyer_name LIKE ? OR s.buyer_phone LIKE ? OR s.buyer_email LIKE ? OR s.agent_name LIKE ? OR p.plot_number LIKE ? OR e.name LIKE ?)";
  $search_param = "%$search%";
  $where_conditions[] = $search_condition;
  $params = array_merge($params, array_fill(0, 6, $search_param));
  $types .= str_repeat('s', 6);
}

$where_clause = !empty($where_conditions) ? "WHERE " . implode(" AND ", $where_conditions) : "";

// Count total records
$count_sql = "SELECT COUNT(*) as total FROM prop_sales s
              JOIN prop_plots p ON p.id = s.plot_id
              JOIN prop_estates e ON e.id = p.estate_id $where_clause";
$count_stmt = $conn->prepare($count_sql);
if (!empty($params)) {
  $count_stmt->bind_param($types, ...$params);
}
$count_stmt->execute();
$total_records = $count_stmt->get_result()->fetch_assoc()['total'];
$total_pages = ceil($total_records / $per_page);

// Fetch paginated results
$sql = "SELECT s.id AS sale_id, s.plot_id, s.buyer_name AS client_name, s.buyer_phone AS phone, s.buyer_email AS email, s.agent_name, p.plot_number, e.name AS estate_name, s.amount, s.date_sold, s.payment_plan, s.deposit_doc, s.id_doc, s.kra_doc, s.passport_photo
        FROM prop_sales s
        JOIN prop_plots p ON p.id = s.plot_id
        JOIN prop_estates e ON e.id = p.estate_id
        $where_clause
        ORDER BY s.date_sold DESC
        LIMIT ? OFFSET ?";

$params[] = $per_page;
$params[] = $offset;
$types .= 'ii';

$stmt = $conn->prepare($sql);
$stmt->bind_param($types, ...$params);
$stmt->execute();
$result = $stmt->get_result();

$page_title = 'Fully Paid Plots - ProProperty';

ob_start();
?>
  <div class="top-bar">
    <h1>All Fully Paid Plots</h1>
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
    <div id="column-toggles" style="display: flex; flex-direction: column; gap: 5px;">
      <label><input type="checkbox" class="column-toggle" data-column="client_name" checked> Client Name</label>
      <label><input type="checkbox" class="column-toggle" data-column="phone" checked> Phone</label>
      <label><input type="checkbox" class="column-toggle" data-column="email" checked> Email</label>
      <label><input type="checkbox" class="column-toggle" data-column="agent" checked> Agent</label>
      <label><input type="checkbox" class="column-toggle" data-column="plot_number" checked> Plot Number</label>
      <label><input type="checkbox" class="column-toggle" data-column="estate" checked> Estate</label>
      <label><input type="checkbox" class="column-toggle" data-column="deposit_(ksh)" checked> Deposit (Ksh)</label>
      <label><input type="checkbox" class="column-toggle" data-column="payment_plan" checked> Payment Plan</label>
      <label><input type="checkbox" class="column-toggle" data-column="date_sold" checked> Date Sold</label>
      <label><input type="checkbox" class="column-toggle" data-column="payment_reference" checked> Payment Reference</label>
      <label><input type="checkbox" class="column-toggle" data-column="id_copy" checked> ID Copy</label>
      <label><input type="checkbox" class="column-toggle" data-column="kra_copy" checked> KRA Copy</label>
      <label><input type="checkbox" class="column-toggle" data-column="passport_photo" checked> Passport Photo</label>
    </div>
  </div>

  <div class="card">
    <?php if ($result->num_rows > 0): ?>
      <div class="table-responsive" style="overflow-x: auto; max-width: 100%;">
        <table style="min-width: 1200px;">
          <thead>
            <tr>
              <th>Client Name</th>
              <th>Phone</th>
              <th>Email</th>
              <th>Agent</th>
              <th>Plot Number</th>
              <th>Estate</th>
              <th>Deposit (Ksh)</th>
              <th>Payment Plan</th>
              <th>Date Sold</th>
              <th>Payment Reference</th>
              <th>ID Copy</th>
              <th>KRA Copy</th>
              <th>Passport Photo</th>
              <th>Actions</th>
            </tr>
          </thead>
          <tbody>
            <?php while ($row = $result->fetch_assoc()): ?>
              <tr style="background-color: #ffcccc;">
                <td><?php echo htmlspecialchars($row['client_name']); ?></td>
                <td><?php echo htmlspecialchars($row['phone']); ?></td>
                <td><?php echo htmlspecialchars($row['email']); ?></td>
                <td><?php echo htmlspecialchars($row['agent_name']); ?></td>
                <td><?php echo htmlspecialchars($row['plot_number']); ?></td>
                <td><?php echo htmlspecialchars($row['estate_name']); ?></td>
                <td><?php echo number_format((float)$row['amount']); ?></td>
                <td><?php echo htmlspecialchars($row['payment_plan']); ?></td>
                <td><?php echo htmlspecialchars($row['date_sold']); ?></td>
                <td><?php echo $row['deposit_doc'] ? '<a href="' . htmlspecialchars($row['deposit_doc']) . '" target="_blank">View</a> | <a href="' . htmlspecialchars($row['deposit_doc']) . '" download>Download</a>' : 'N/A'; ?></td>
                <td><?php echo $row['id_doc'] ? '<a href="' . htmlspecialchars($row['id_doc']) . '" target="_blank">View</a> | <a href="' . htmlspecialchars($row['id_doc']) . '" download>Download</a>' : 'N/A'; ?></td>
                <td><?php echo $row['kra_doc'] ? '<a href="' . htmlspecialchars($row['kra_doc']) . '" target="_blank">View</a> | <a href="' . htmlspecialchars($row['kra_doc']) . '" download>Download</a>' : 'N/A'; ?></td>
                <td><?php echo $row['passport_photo'] ? '<a href="' . htmlspecialchars($row['passport_photo']) . '" target="_blank">View</a> | <a href="' . htmlspecialchars($row['passport_photo']) . '" download>Download</a>' : 'N/A'; ?></td>
                <td>
                  <button onclick="makeAvailable(<?php echo $row['sale_id']; ?>, <?php echo $row['plot_id']; ?>, 'sold')" class="edit-btn" style="background-color: #28a745;">Make Available</button>
                  <button onclick="makeBooked(<?php echo $row['sale_id']; ?>, <?php echo $row['plot_id']; ?>, 'sold')" class="edit-btn" style="background-color: #ffc107;">Make Booked</button>
                </td>
              </tr>
            <?php endwhile; ?>
          </tbody>
        </table>
      </div>
    <?php else: ?>
      <p>No fully paid plots found.</p>
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
    });

    // Function to make plot available
    function makeAvailable(saleId, plotId, type) {
      if (confirm('Are you sure you want to make this plot available? This will remove the sale record.')) {
        fetch('update_plot_status.php', {
          method: 'POST',
          headers: {
            'Content-Type': 'application/x-www-form-urlencoded',
          },
          body: 'action=make_available&sale_id=' + saleId + '&plot_id=' + plotId + '&type=' + type
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

    // Function to make plot booked
    function makeBooked(saleId, plotId, type) {
      if (confirm('Are you sure you want to change this fully paid plot to booked?')) {
        fetch('update_plot_status.php', {
          method: 'POST',
          headers: {
            'Content-Type': 'application/x-www-form-urlencoded',
          },
          body: 'action=make_booked&sale_id=' + saleId + '&plot_id=' + plotId + '&type=' + type
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
  </script>
<?php
$page_content = ob_get_clean();
include 'layout.php';
?>