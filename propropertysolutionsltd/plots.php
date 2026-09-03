<?php
session_start();
include 'activity_log.php'; // Include activity logging

include 'db_connection.php'; // Include database connection

// Log page access
logPageAccess('plots');

$role = $_SESSION['role'] ?? null;
$agentName = $_SESSION['user_name'] ?? null;

// Selling plots is now disabled for all users
$can_sell = false;

// Debug removed

$estate_id = isset($_GET['estate_id']) ? intval($_GET['estate_id']) : 0;
$status = isset($_GET['status']) ? $_GET['status'] : 'available';
$search = isset($_GET['search']) ? trim($_GET['search']) : '';

if (!$estate_id) {
  echo "<p>No estate selected.</p>";
  exit;
}

// Fetch estate details
$estate = $conn->query("SELECT * FROM prop_estates WHERE id = $estate_id")->fetch_assoc();
if (!$estate) {
  echo "<p>Estate not found.</p>";
  exit;
}

// Pagination setup
$plots_per_page = 5;
$page = isset($_GET['page']) ? (int)$_GET['page'] : 1;
if ($page < 1) $page = 1;
$offset = ($page - 1) * $plots_per_page;

// Build dynamic SQL with optional agent scoping for booked/sold
$join = '';
$where = "WHERE p.estate_id = ? AND p.status = ?";
$params = [$estate_id, $status];
$types = "is";

if (!empty($search)) {
  $where .= " AND p.plot_number LIKE ?";
  $params[] = "%" . $search . "%";
  $types .= "s";
}

// If agent is viewing booked/sold, scope to their own records via agent_name
if ($role === 'agent' && $agentName) {
  if ($status === 'booked') {
    $join = "JOIN prop_bookings pb ON pb.plot_id = p.id AND pb.status = 'active' AND pb.agent_name = ?";
    $params[] = $agentName;
    $types .= 's';
  } elseif ($status === 'sold') {
    $join = "JOIN prop_sales ps ON ps.plot_id = p.id AND ps.agent_name = ?";
    $params[] = $agentName;
    $types .= 's';
  }
}

// Count total for pagination
$count_sql = "SELECT COUNT(DISTINCT p.id) as total FROM prop_plots p $join $where";
$count_stmt = $conn->prepare($count_sql);
$count_stmt->bind_param($types, ...$params);
$count_stmt->execute();
$count_result = $count_stmt->get_result();
$total_rows = (int)($count_result->fetch_assoc()['total'] ?? 0);
$total_pages = max(1, (int)ceil($total_rows / $plots_per_page));

// Fetch paginated plots
$sql = "SELECT p.* FROM prop_plots p $join $where LIMIT ?, ?";
$params[] = $offset;
$params[] = $plots_per_page;
$types .= "ii";

$stmt = $conn->prepare($sql);
$stmt->bind_param($types, ...$params);
$stmt->execute();
$plots = $stmt->get_result();

$page_title = ucfirst($status) . ' Plots - ' . htmlspecialchars($estate['name']);

ob_start();
?>
  <div class="top-bar">
    <h1><?php echo ucfirst($status); ?> Plots in <?php echo htmlspecialchars($estate['name']); ?></h1>
    <div class="search-container">
      <form method="GET">
        <input type="hidden" name="estate_id" value="<?php echo $estate_id; ?>">
        <input type="hidden" name="status" value="<?php echo htmlspecialchars($status); ?>">
        <input type="text" name="search" placeholder="Search by plot number..." value="<?php echo htmlspecialchars($search); ?>">
        <button type="submit">🔍 Search</button>
      </form>
    </div>
  </div>

  <div class="bulk-actions" style="margin-bottom: 20px;" id="bulk-actions">
    <p style="margin-bottom: 10px; color: #666;">Select multiple plots below to enable bulk booking.</p>
    <button type="button" onclick="submitBulk('book')" class="add-btn" id="book-selected-btn" disabled>Book Selected Plots</button>
  </div>

  <form method="POST" id="bulk-form">
  <input type="hidden" name="bulk_action" id="bulk_action" value="">
  <input type="hidden" name="estate_id" value="<?php echo $estate_id; ?>">
  <input type="hidden" name="status" value="<?php echo $status; ?>">

  <?php if ($plots->num_rows > 0): ?>
    <?php while ($p = $plots->fetch_assoc()): ?>
      <div class="card plot-card" style="background: <?php
      if ($p['status'] == 'available') echo '#ffffff';
      elseif ($p['status'] == 'booked') echo '#ffff99';
      elseif ($p['status'] == 'sold') echo '#d4edda';
      elseif ($p['status'] == 'sa_signed') echo '#ff0000';
    ?>; color: <?php echo ($p['status'] == 'booked') ? '#000' : 'inherit'; ?>;">
        <?php if ($status == 'available' || $status == 'booked'): ?>
        <input type="checkbox" name="selected_plots[]" value="<?php echo $p['id']; ?>" onchange="toggleBulkActions()">
        <?php endif; ?>
        <h3>Plot Number: <?php echo htmlspecialchars($p['plot_number']); ?></h3>

        <?php if ($status == 'available'): ?>
          <a href="select_booking_type.php?plot_id=<?php echo $p['id']; ?>&estate_id=<?php echo $estate_id; ?>" class="add-btn">Book Plot</a>
        <?php elseif ($status == 'booked'): ?>
          <a href="plot_details.php?plot_id=<?php echo $p['id']; ?>" class="edit-btn">View Details</a>
        <?php elseif ($status == 'sold'): ?>
          <a href="plot_details.php?plot_id=<?php echo $p['id']; ?>" class="edit-btn">View Details</a>
        <?php elseif ($status == 'sa_signed'): ?>
          <a href="plot_details.php?plot_id=<?php echo $p['id']; ?>" class="edit-btn">View Details</a>
        <?php endif; ?>
      </div>
    <?php endwhile; ?>
  </form>

    <div class="pagination">
      <?php if ($page > 1): ?>
        <a href="?estate_id=<?php echo $estate_id; ?>&status=<?php echo urlencode($status); ?>&search=<?php echo urlencode($search); ?>&page=<?php echo $page - 1; ?>">⬅ Prev</a>
      <?php endif; ?>

      <?php for ($i = 1; $i <= $total_pages; $i++): ?>
        <a href="?estate_id=<?php echo $estate_id; ?>&status=<?php echo urlencode($status); ?>&search=<?php echo urlencode($search); ?>&page=<?php echo $i; ?>" class="<?php echo ($i == $page ? 'active' : ''); ?>"><?php echo $i; ?></a>
      <?php endfor; ?>

      <?php if ($page < $total_pages): ?>
        <a href="?estate_id=<?php echo $estate_id; ?>&status=<?php echo urlencode($status); ?>&search=<?php echo urlencode($search); ?>&page=<?php echo $page + 1; ?>">Next ➡</a>
      <?php endif; ?>
    </div>
  <?php else: ?>
    <div class="card"><p>No plots found for this status.</p></div>
  <?php endif; ?>

  <script>
    // Unique key for localStorage based on estate and status
    const storageKey = 'selected_plots_<?php echo $estate_id; ?>_<?php echo $status; ?>';

    function saveSelectedPlots() {
      const saved = localStorage.getItem(storageKey);
      let selectedIds = saved ? JSON.parse(saved) : [];

      const checkboxes = document.querySelectorAll('input[name="selected_plots[]"]');
      checkboxes.forEach(cb => {
        const id = cb.value;
        if (cb.checked) {
          // Add to selected if not already there
          if (!selectedIds.includes(id)) {
            selectedIds.push(id);
          }
        } else {
          // Remove from selected if unchecked
          const index = selectedIds.indexOf(id);
          if (index > -1) {
            selectedIds.splice(index, 1);
          }
        }
      });

      localStorage.setItem(storageKey, JSON.stringify(selectedIds));
    }

    function loadSelectedPlots() {
      const saved = localStorage.getItem(storageKey);
      if (saved) {
        const selectedIds = JSON.parse(saved);
        const checkboxes = document.querySelectorAll('input[name="selected_plots[]"]');
        checkboxes.forEach(cb => {
          if (selectedIds.includes(cb.value)) {
            cb.checked = true;
          }
        });
      }
    }

    function clearSelectedPlots() {
      localStorage.removeItem(storageKey);
    }

    function getAllSelectedPlots() {
      const saved = localStorage.getItem(storageKey);
      return saved ? JSON.parse(saved) : [];
    }

    function toggleBulkActions() {
      const selectedIds = getAllSelectedPlots();
      const bookBtn = document.getElementById('book-selected-btn');
      const enabled = selectedIds.length > 0;
      bookBtn.disabled = !enabled;
    }

    function submitBulk(action) {
      const selectedIds = getAllSelectedPlots();
      if (selectedIds.length === 0) {
        alert('Please select at least one plot.');
        return;
      }

      // Add hidden inputs for all selected plots
      const form = document.getElementById('bulk-form');
      selectedIds.forEach(id => {
        const hiddenInput = document.createElement('input');
        hiddenInput.type = 'hidden';
        hiddenInput.name = 'selected_plots[]';
        hiddenInput.value = id;
        form.appendChild(hiddenInput);
      });

      // Clear storage after successful submission
      clearSelectedPlots();
      document.getElementById('bulk_action').value = action;

      // Redirect to booking selection page with selected plots
      const params = new URLSearchParams();
      selectedIds.forEach(id => params.append('selected_plots[]', id));
      params.append('estate_id', '<?php echo $estate_id; ?>');
      window.location.href = 'select_booking_type.php?' + params.toString();
    }

    // Load selected plots on page load
    document.addEventListener('DOMContentLoaded', loadSelectedPlots);

    // Save selected plots on checkbox change
    document.addEventListener('change', function(e) {
      if (e.target.name === 'selected_plots[]') {
        saveSelectedPlots();
        toggleBulkActions();
      }
    });
  </script>
<?php
$page_content = ob_get_clean();
include 'layout.php';
?>
