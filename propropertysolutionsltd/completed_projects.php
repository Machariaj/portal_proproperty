<?php
session_start();
include 'activity_log.php'; // Include activity logging
include 'db_connection.php'; // Include database connection

// Log page access
logPageAccess('completed_projects');

$role = $_SESSION['role'] ?? 'guest';

if ($role !== 'admin') {
  header("Location: index.php");
  exit;
}

// Fetch completed projects: estates with no available plots (all booked or sold)
$completed_estates = $conn->query("
  SELECT e.id, e.name, e.image, e.prop_plotinfo,
         COUNT(p.id) AS total_plots,
         SUM(CASE WHEN p.status = 'available' THEN 1 ELSE 0 END) AS available_plots
  FROM prop_estates e
  LEFT JOIN prop_plots p ON e.id = p.estate_id
  GROUP BY e.id, e.name, e.image, e.prop_plotinfo
  HAVING available_plots = 0 AND total_plots > 0
  ORDER BY e.id DESC
");

$page_title = 'Completed Projects - ProProperty';

ob_start();
?>
  <div class="top-bar">
    <h1>Completed Projects</h1>
    <p>Estates that have all plots booked or sold.</p>
  </div>

  <?php if ($completed_estates->num_rows > 0): ?>
    <?php while($row = $completed_estates->fetch_assoc()): ?>
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
        <img src="uploads/<?= htmlspecialchars($row['image']) ?>" alt="Estate Image" style="max-width: 300px; height: auto;">
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
                if ($status === 'booked') $color = '#ffff99';
                elseif ($status === 'sold') $color = '#22c55e';
                elseif ($status === 'sa_signed') $color = '#007bff';
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
          <p><strong>Total Plots:</strong> <?= $total_plots ?> | <strong>Status:</strong> All Booked/Sold</p>

          <div class="estate-buttons">
            <a href="plots.php?estate_id=<?= $row['id'] ?>&status=available" class="btn-available">🏡 Available</a>
            <a href="plots.php?estate_id=<?= $row['id'] ?>&status=booked" class="btn-booked">📘 Booked</a>
            <a href="plots.php?estate_id=<?= $row['id'] ?>&status=sold" class="btn-sold">💰 Sold</a>
            <a href="plots.php?estate_id=<?= $row['id'] ?>&status=sa_signed" class="btn-sa-signed">📝 SA Signed</a>
          </div>

          <div class="admin-actions">
            <a href="edit_estate.php?id=<?= $row['id'] ?>" class="edit-btn">✏️ Edit</a>
          </div>
        </div>
      </div>
    <?php endwhile; ?>
  <?php else: ?>
    <p>No completed projects found.</p>
  <?php endif; ?>

  <a href="admin_dashboard.php" class="edit-btn">← Back to Dashboard</a>
<?php
$page_content = ob_get_clean();
include 'layout.php';
?>
