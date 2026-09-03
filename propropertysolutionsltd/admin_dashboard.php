<?php
#error_reporting(E_ALL);
#ini_set('display_errors', 1);
session_start();
include 'activity_log.php'; // Include activity logging
include 'db_connection.php'; // Include database connection

// Log page access
logPageAccess('admin_dashboard');

// Fetch data for dashboard charts
$total_query = $conn->query("SELECT e.name AS estate_name, COUNT(p.id) AS total_plots 
  FROM prop_estates e 
  LEFT JOIN prop_plots p ON e.id = p.estate_id 
  GROUP BY e.id");

$available_query = $conn->query("SELECT e.name AS estate_name, COUNT(p.id) AS total_available 
  FROM prop_estates e 
  LEFT JOIN prop_plots p ON e.id = p.estate_id AND p.status = 'available' 
  GROUP BY e.id");

$booked_query = $conn->query("SELECT e.name AS estate_name, COUNT(p.id) AS total_booked 
  FROM prop_estates e 
  LEFT JOIN prop_plots p ON e.id = p.estate_id AND p.status = 'booked' 
  GROUP BY e.id");

$sold_query = $conn->query("SELECT e.name AS estate_name, COUNT(p.id) AS total_sold
  FROM prop_estates e
  LEFT JOIN prop_plots p ON e.id = p.estate_id AND p.status = 'sold'
  GROUP BY e.id");

$sa_signed_query = $conn->query("SELECT e.name AS estate_name, COUNT(p.id) AS total_sa_signed
  FROM prop_estates e
  LEFT JOIN prop_plots p ON e.id = p.estate_id AND p.status = 'sa_signed'
  GROUP BY e.id");

$agent_query = $conn->query("SELECT agent_name, COUNT(*) AS total_sales 
  FROM prop_sales 
  GROUP BY agent_name 
  ORDER BY total_sales DESC");

$months_query = $conn->query("SELECT DISTINCT DATE_FORMAT(sale_date, '%Y-%m') AS month 
  FROM prop_sales 
  ORDER BY month DESC");
$months = [];
while ($row = $months_query->fetch_assoc()) {
  $months[] = $row['month'];
}

function extractChartData($query, $labelField, $valueField) {
  $labels = [];
  $values = [];
  while ($row = $query->fetch_assoc()) {
    $labels[] = $row[$labelField];
    $values[] = (int)$row[$valueField];
  }
  return [$labels, $values];
}

list($estates_total, $plots_total) = extractChartData($total_query, 'estate_name', 'total_plots');
list($estates_available, $plots_available) = extractChartData($available_query, 'estate_name', 'total_available');
list($estates_booked, $plots_booked) = extractChartData($booked_query, 'estate_name', 'total_booked');
list($estates_sold, $plots_sold) = extractChartData($sold_query, 'estate_name', 'total_sold');
list($estates_sa_signed, $plots_sa_signed) = extractChartData($sa_signed_query, 'estate_name', 'total_sa_signed');
list($agents, $sales) = extractChartData($agent_query, 'agent_name', 'total_sales');

// Fetch estates for grids
$estates_grid = $conn->query("SELECT * FROM prop_estates");

$page_title = 'ProProperty - Dashboard';

ob_start();
?>
  <div class="top-bar">
    <h1>Dashboard Overview</h1>
  </div>

  <div class="card" style="max-width: 640px;">
    <label for="month">Filter by Month</label>
    <select id="month">
      <option value="">All Months</option>
      <?php foreach ($months as $month): ?>
        <option value="<?= htmlspecialchars($month) ?>"><?= htmlspecialchars($month) ?></option>
      <?php endforeach; ?>
    </select>
  </div>

  <div style="display: grid; grid-template-columns: repeat(auto-fit, minmax(320px, 1fr)); gap: 16px;">
    <div class="card">
      <h3>Total Plots per Estate</h3>
      <div style="height: 240px;">
        <canvas id="totalChart"></canvas>
      </div>
    </div>

    <div class="card">
      <h3>Available Plots per Estate</h3>
      <div style="height: 240px;">
        <canvas id="availableChart"></canvas>
      </div>
    </div>

    <div class="card">
      <h3>Booked Plots per Estate</h3>
      <div style="height: 240px;">
        <canvas id="bookedChart"></canvas>
      </div>
    </div>

    <div class="card">
      <h3>Sold Plots per Estate</h3>
      <div style="height: 240px;">
        <canvas id="soldChart"></canvas>
      </div>
    </div>

    <div class="card">
      <h3>SA Signed Plots per Estate</h3>
      <div style="height: 240px;">
        <canvas id="saSignedChart"></canvas>
      </div>
    </div>

    <?php if (!(isset($_SESSION['role']) && $_SESSION['role'] === 'agent')): ?>
    <div class="card" style="grid-column: 1 / -1;">
      <h3>Top Sales Agents</h3>
      <div style="height: 260px;">
        <canvas id="agentsChart"></canvas>
      </div>
    </div>
    <?php endif; ?>
  </div>

  <?php if (!(isset($_SESSION['role']) && $_SESSION['role'] === 'agent')): ?>
  <div class="card" style="margin-top: 20px;">
    <h3>Estate Grids</h3>
    <div style="display: grid; grid-template-columns: repeat(auto-fit, minmax(400px, 1fr)); gap: 20px;">
      <?php while ($row = $estates_grid->fetch_assoc()): ?>
        <?php
        // Fetch plots for this estate
        $estate_id = $row['id'];
        $plots_query = $conn->query("SELECT status, plot_number FROM prop_plots WHERE estate_id = $estate_id ORDER BY id");
        $plots = [];
        while ($plot = $plots_query->fetch_assoc()) {
          $plots[] = ['status' => $plot['status'], 'number' => $plot['plot_number']];
        }
        $total_plots = count($plots);
        $cell_size = 40;
        if ($total_plots > 0) {
          $cols = (int)ceil(sqrt($total_plots));
          $rows = (int)ceil($total_plots / max(1, $cols));
        } else {
          $cols = 1;
          $rows = 1;
          $plots = [['status' => 'available', 'number' => '']]; // default
        }
        $svg_width = $cols * $cell_size;
        $svg_height = $rows * $cell_size;
        ?>
        <div class="estate-card" style="display: flex; gap: 20px; align-items: center;">
          <div>
            <h4><?php echo htmlspecialchars($row['name']); ?></h4>
            <img src="uploads/<?= htmlspecialchars($row['image']) ?>" alt="Estate Image" style="max-width: 200px; height: auto;" onerror="this.style.display='none'; this.nextElementSibling.style.display='flex';" />
            <div style="width: 200px; height: 150px; background: #f0f0f0; display: none; align-items: center; justify-content: center; color: #666; font-size: 12px;">No Image</div>
          </div>
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
                  elseif ($status === 'sa_signed') $color = '#ff0000';
                  $x = $c * $cell_size;
                  $y = $r * $cell_size;
                  echo "<rect x='$x' y='$y' width='$cell_size' height='$cell_size' fill='$color' stroke='#000' stroke-width='1' />";
                  if ($number) {
                    echo "<text x='" . ($x + $cell_size / 2) . "' y='" . ($y + $cell_size / 2 + 4) . "' font-size='12' fill='black' text-anchor='middle'>$number</text>";
                  }
                  $index++;
                }
              }
              ?>
            </svg>
          </div>
        </div>
      <?php endwhile; ?>
    </div>
  </div>
  <?php endif; ?>

  <script src="https://cdn.jsdelivr.net/npm/chart.js"></script>
  <script>
    const createChart = (id, labels, data, label, color) => {
      new Chart(document.getElementById(id), {
        type: 'bar',
        data: {
          labels: labels,
          datasets: [{
            label: label,
            data: data,
            backgroundColor: color
          }]
        },
        options: { responsive: true, maintainAspectRatio: false, scales: { y: { beginAtZero: true } } }
      });
    };

    createChart('totalChart', <?= json_encode($estates_total) ?>, <?= json_encode($plots_total) ?>, 'Total Plots', 'rgba(0,0,0,0.6)');
    createChart('availableChart', <?= json_encode($estates_available) ?>, <?= json_encode($plots_available) ?>, 'Available Plots', 'rgba(0,0,0,0.6)');
    createChart('bookedChart', <?= json_encode($estates_booked) ?>, <?= json_encode($plots_booked) ?>, 'Booked Plots', 'rgba(241,196,15,0.6)');
    createChart('soldChart', <?= json_encode($estates_sold) ?>, <?= json_encode($plots_sold) ?>, 'Sold Plots', 'rgba(46,204,113,0.6)');
    createChart('saSignedChart', <?= json_encode($estates_sa_signed) ?>, <?= json_encode($plots_sa_signed) ?>, 'SA Signed Plots', 'rgba(231,76,60,0.6)');
    <?php if (!(isset($_SESSION['role']) && $_SESSION['role'] === 'agent')): ?>
    createChart('agentsChart', <?= json_encode($agents) ?>, <?= json_encode($sales) ?>, 'Top Sales Agents', 'rgba(46,204,113,0.6)');
    <?php endif; ?>
  </script>
<?php
$page_content = ob_get_clean();
include 'layout.php';
?>
