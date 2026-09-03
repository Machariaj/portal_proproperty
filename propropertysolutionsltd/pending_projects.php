<?php
session_start();
include 'activity_log.php'; // Include activity logging
include 'db_connection.php'; // Include database connection

// Log page access
logPageAccess('pending_projects');

$role = $_SESSION['role'] ?? 'guest';

if ($role !== 'admin') {
  header("Location: index.php");
  exit;
}

// Fetch pending projects: estates with at least one available plot
$pending_estates = $conn->query("
  SELECT e.id, e.name, e.image, e.prop_plotinfo,
         COUNT(p.id) AS total_plots,
         SUM(CASE WHEN p.status = 'available' THEN 1 ELSE 0 END) AS available_plots
  FROM prop_estates e
  LEFT JOIN prop_plots p ON e.id = p.estate_id
  GROUP BY e.id, e.name, e.image, e.prop_plotinfo
  HAVING available_plots > 0
  ORDER BY e.id DESC
");

$page_title = 'Pending Projects - ProProperty';

ob_start();
?>
  <div class="top-bar">
    <h1>Pending Projects</h1>
    <p>Estates that still have available plots.</p>
  </div>

  <?php if ($pending_estates->num_rows > 0): ?>
    <?php while($row = $pending_estates->fetch_assoc()): ?>
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
                $color = 'white'; // available
                if ($status === 'booked') $color = 'yellow';
                elseif ($status === 'sold') $color = 'green';
                elseif ($status === 'sa_signed') $color = 'blue';
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
          <p><strong>Total Plots:</strong> <?= $total_plots ?> | <strong>Available:</strong> <?= $row['available_plots'] ?></p>

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
    <p>No pending projects found.</p>
  <?php endif; ?>

  <a href="admin_dashboard.php" class="edit-btn">← Back to Dashboard</a>
<?php
$page_content = ob_get_clean();
include 'layout.php';
?>
