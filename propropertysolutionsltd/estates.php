

<?php
session_start();
include 'activity_log.php'; // Include activity logging
include 'db_connection.php'; // Include database connection

// Log page access
logPageAccess('estates');

$role = $_SESSION['role'] ?? 'guest';

// Handle delete request
if (isset($_GET['delete_id'])) {
  $delete_id = intval($_GET['delete_id']);
  $conn->query("DELETE FROM prop_estates WHERE id = $delete_id");
  header("Location: estates.php?deleted=1");
  exit;
}

// Handle search
$search = "";
$whereClause = "";
if (!empty($_GET['search'])) {
  $search = $conn->real_escape_string($_GET['search']);
  $whereClause = "WHERE name LIKE '%$search%' OR prop_plotinfo LIKE '%$search%'";
}

// Pagination setup
$limit = 6; // Estates per page
$page = isset($_GET['page']) && $_GET['page'] > 0 ? intval($_GET['page']) : 1;
$offset = ($page - 1) * $limit;

// Count total estates
$totalQuery = $conn->query("SELECT COUNT(*) AS total FROM prop_estates $whereClause");
$totalEstates = $totalQuery->fetch_assoc()['total'];
$totalPages = ceil($totalEstates / $limit);

// Fetch estates
$estates = $conn->query("SELECT * FROM prop_estates $whereClause ORDER BY id DESC LIMIT $limit OFFSET $offset");

$page_title = 'ProProperty - Estates';

ob_start();
?>
  <div class="top-bar">
    <h1>Pro-Property Estates</h1>
    <div class="search-container">
      <form method="GET" action="estates.php">
        <input type="text" name="search" value="<?= htmlspecialchars($search) ?>" placeholder="Search estates...">
        <button type="submit">🔍 Search</button>
      </form>
    </div>
    <?php if ($role === 'admin'): ?>
    <a href="add_estate.php" class="add-btn">+ Add New Estate</a>
    <?php endif; ?>
  </div>

  <?php if (isset($_GET['deleted'])): ?>
    <div class="message">Estate deleted successfully!</div>
  <?php endif; ?>

  <?php if ($estates->num_rows > 0): ?>
    <?php while($row = $estates->fetch_assoc()): ?>
      <?php
      // Fetch plots for this estate
      $estate_id = $row['id'];
      $plots_query = $conn->query("SELECT status, plot_number FROM prop_plots WHERE estate_id = $estate_id ORDER BY id");
      $plots = [];
      while ($plot = $plots_query->fetch_assoc()) {
        $plots[] = ['status' => $plot['status'], 'number' => $plot['plot_number']];
      }
      $total_plots = count($plots);
      if ($total_plots > 0) {
        $cols = ceil(sqrt($total_plots));
        $rows = ceil($total_plots / $cols);
        $cell_size = 20;
        $svg_width = $cols * $cell_size;
        $svg_height = $rows * $cell_size;
      } else {
        $cols = 1;
        $rows = 1;
        $cell_size = 20;
        $svg_width = $cell_size;
        $svg_height = $cell_size;
        $plots = ['available']; // default
      }
      ?>
      <div class="estate-card" style="display: flex; gap: 20px;">
        <img src="uploads/<?= htmlspecialchars($row['image']) ?>" alt="Estate Image" style="max-width: 300px; height: auto;" onerror="this.style.display='none'; this.nextElementSibling.style.display='block';" />
        <div style="width: 300px; height: 200px; background: #f0f0f0; display: none; align-items: center; justify-content: center; color: #666;">No Image Available</div>
        <div class="estate-grid">
          <svg width="<?= $svg_width ?>" height="<?= $svg_height ?>" viewBox="0 0 <?= $svg_width ?> <?= $svg_height ?>">
            <?php
            // Draw roads (lines) first
            for ($r = 1; $r < $rows; $r++) {
              $y = $r * $cell_size;
              echo "<line x1='0' y1='$y' x2='$svg_width' y2='$y' stroke='black' stroke-width='2' />";
            }
            for ($c = 1; $c < $cols; $c++) {
              $x = $c * $cell_size;
              echo "<line x1='$x' y1='0' x2='$x' y2='$svg_height' stroke='black' stroke-width='2' />";
            }
            // Draw plots
            $index = 0;
            for ($r = 0; $r < $rows; $r++) {
              for ($c = 0; $c < $cols; $c++) {
                if ($index >= $total_plots) break;
                $plot = $plots[$index];
                $status = $plot['status'];
                $number = $plot['number'];
                $color = '#ffffff'; // available
                if ($status === 'booked') $color = '#ffff00';
                elseif ($status === 'sold') $color = '#00aa00';
                elseif ($status === 'sa_signed') $color = '#ff0000';
                $x = $c * $cell_size;
                $y = $r * $cell_size;
                echo "<rect x='$x' y='$y' width='$cell_size' height='$cell_size' fill='$color' stroke='#000' stroke-width='1' />";
                echo "<text x='" . ($x + $cell_size / 2) . "' y='" . ($y + $cell_size / 2 + 3) . "' font-size='10' fill='black' text-anchor='middle'>$number</text>";
                $index++;
              }
            }
            ?>
          </svg>
        </div>
        <div class="estate-info">
          <h3><?= htmlspecialchars($row['name']) ?></h3>
          <?php $desc = stripcslashes($row['prop_plotinfo']); ?>
          <p><?= nl2br(htmlspecialchars($desc)) ?></p>

          <div class="estate-buttons">
            <?php if ($role === 'admin'): ?>
            <a href="plots.php?estate_id=<?= $row['id'] ?>&status=available" class="btn-available">🏡 Available</a>
            <a href="plots.php?estate_id=<?= $row['id'] ?>&status=booked" class="btn-booked">📘 Booked</a>
            <a href="plots.php?estate_id=<?= $row['id'] ?>&status=sa_signed" class="btn-sa-signed">📝 SA Signed</a>
            <a href="plots.php?estate_id=<?= $row['id'] ?>&status=sold" class="btn-sold">💰 Sold</a>
            <?php else: ?>
            <a href="plots.php?estate_id=<?= $row['id'] ?>&status=available" class="btn-available">🏡 Available</a>
            <?php endif; ?>
          </div>

          <?php if ($role === 'admin'): ?>
          <div class="admin-actions">
            <a href="edit_estate.php?id=<?= $row['id'] ?>" class="edit-btn">✏️ Edit</a>
            <a href="javascript:void(0)" class="delete-btn" onclick="confirmDelete(<?= $row['id'] ?>)">🗑️ Delete</a>
          </div>
          <?php endif; ?>
        </div>
      </div>
    <?php endwhile; ?>

    <!-- Pagination -->
    <div class="pagination">
      <?php if ($page > 1): ?>
        <a href="?page=<?= $page - 1 ?>&search=<?= urlencode($search) ?>">⬅ Prev</a>
      <?php endif; ?>

      <?php for ($i = 1; $i <= $totalPages; $i++): ?>
        <a href="?page=<?= $i ?>&search=<?= urlencode($search) ?>" class="<?= ($i == $page ? 'active' : '') ?>"><?= $i ?></a>
      <?php endfor; ?>

      <?php if ($page < $totalPages): ?>
        <a href="?page=<?= $page + 1 ?>&search=<?= urlencode($search) ?>">Next ➡</a>
      <?php endif; ?>
    </div>

  <?php else: ?>
    <p>No estates found.</p>
  <?php endif; ?>

  <script>
    function confirmDelete(id) {
      if (confirm('Are you sure you want to delete this estate? This action cannot be undone.')) {
        window.location.href = 'estates.php?delete_id=' + id;
      }
    }
  </script>
<?php
$page_content = ob_get_clean();
include 'layout.php';
?>
