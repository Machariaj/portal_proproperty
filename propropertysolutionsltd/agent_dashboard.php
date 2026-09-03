<?php
session_start();
include 'activity_log.php'; // Include activity logging
include 'db_connection.php'; // Include database connection

// Log page access
logPageAccess('agent_dashboard');

if (!isset($_SESSION['user_id']) || $_SESSION['role'] != 'agent') {
  header("Location: index.php");
  exit;
}

$agentName = $_SESSION['user_name'] ?? '';

// --- Get counts ---
$total_estates = $conn->query("SELECT COUNT(*) AS total FROM prop_estates")->fetch_assoc()['total'];
$total_available = $conn->query("SELECT COUNT(*) AS total FROM prop_plots WHERE status='available'")->fetch_assoc()['total'];

$total_booked = 0;
$total_sold = 0;
if ($agentName !== '') {
  $stmtB = $conn->prepare("SELECT COUNT(*) AS total FROM prop_bookings WHERE agent_name = ? AND status = 'active'");
  $stmtB->bind_param("s", $agentName);
  $stmtB->execute();
  $total_booked = $stmtB->get_result()->fetch_assoc()['total'] ?? 0;

  $stmtS = $conn->prepare("SELECT COUNT(*) AS total FROM prop_sales WHERE agent_name = ?");
  $stmtS->bind_param("s", $agentName);
  $stmtS->execute();
  $total_sold = $stmtS->get_result()->fetch_assoc()['total'] ?? 0;
}

// --- Estates list ---
$estates = $conn->query("SELECT * FROM prop_estates");

$page_title = 'Agent Dashboard - ProProperty';

ob_start();
?>
  <div class="top-bar">
    <h1>Welcome, <?= htmlspecialchars($_SESSION['user_name']) ?></h1>
  </div>

  <div class="card">
    <div style="display: grid; grid-template-columns: repeat(auto-fit, minmax(180px, 1fr)); gap: 12px;">
      <div class="card">
        <h3>Total Estates</h3>
        <p style="font-size: 24px; font-weight: 700; color: #2563eb; margin: 6px 0 0;"><?= (int)$total_estates ?></p>
      </div>
      <div class="card">
        <h3>Available Plots</h3>
        <p style="font-size: 24px; font-weight: 700; color: #16a34a; margin: 6px 0 0;"><?= (int)$total_available ?></p>
      </div>
      <div class="card">
        <h3>Your Booked Plots</h3>
        <p style="font-size: 24px; font-weight: 700; color: #ca8a04; margin: 6px 0 0;"><a href="agent_bookings.php" style="color: inherit; text-decoration: none;"><?= (int)$total_booked ?></a></p>
      </div>
      <div class="card">
        <h3>Your Sold Plots</h3>
        <p style="font-size: 24px; font-weight: 700; color: #dc2626; margin: 6px 0 0;"><a href="agent_sales.php" style="color: inherit; text-decoration: none;"><?= (int)$total_sold ?></a></p>
      </div>
    </div>
  </div>

  <div class="top-bar">
    <h2>Plot Analytics</h2>
  </div>

  <div class="card">
    <div style="display: grid; grid-template-columns: repeat(auto-fit, minmax(300px, 1fr)); gap: 20px;">
      <div class="card">
        <h3>Available Plots Across All Estates</h3>
        <canvas id="availableChart" width="300" height="200"></canvas>
      </div>
      <div class="card">
        <h3>Booked Plots Across All Estates</h3>
        <canvas id="bookedChart" width="300" height="200"></canvas>
      </div>
      <div class="card">
        <h3>Sold Plots Across All Estates</h3>
        <canvas id="soldChart" width="300" height="200"></canvas>
      </div>
    </div>
  </div>

  <script src="https://cdn.jsdelivr.net/npm/chart.js"></script>
  <script>
    // Fetch data for charts
    <?php
    $estate_data = [];
    $estates->data_seek(0); // Reset pointer
    while($row = $estates->fetch_assoc()) {
      $estate_id = $row['id'];
      $estate_name = $row['name'];

      $available = $conn->query("SELECT COUNT(*) AS count FROM prop_plots WHERE estate_id = $estate_id AND status = 'available'")->fetch_assoc()['count'];
      $booked = $conn->query("SELECT COUNT(*) AS count FROM prop_plots WHERE estate_id = $estate_id AND status = 'booked'")->fetch_assoc()['count'];
      $sold = $conn->query("SELECT COUNT(*) AS count FROM prop_plots WHERE estate_id = $estate_id AND status = 'sold'")->fetch_assoc()['count'];

      $estate_data[] = [
        'name' => $estate_name,
        'available' => (int)$available,
        'booked' => (int)$booked,
        'sold' => (int)$sold
      ];
    }
    ?>

    const estateData = <?php echo json_encode($estate_data); ?>;
    const estateNames = estateData.map(item => item.name);

    // Available Plots Chart
    new Chart(document.getElementById('availableChart'), {
      type: 'bar',
      data: {
        labels: estateNames,
        datasets: [{
          label: 'Available Plots',
          data: estateData.map(item => item.available),
          backgroundColor: '#16a34a',
          borderColor: '#15803d',
          borderWidth: 1
        }]
      },
      options: {
        responsive: true,
        scales: {
          y: {
            beginAtZero: true
          }
        }
      }
    });

    // Booked Plots Chart
    new Chart(document.getElementById('bookedChart'), {
      type: 'bar',
      data: {
        labels: estateNames,
        datasets: [{
          label: 'Booked Plots',
          data: estateData.map(item => item.booked),
          backgroundColor: '#ca8a04',
          borderColor: '#a16207',
          borderWidth: 1
        }]
      },
      options: {
        responsive: true,
        scales: {
          y: {
            beginAtZero: true
          }
        }
      }
    });

    // Sold Plots Chart
    new Chart(document.getElementById('soldChart'), {
      type: 'bar',
      data: {
        labels: estateNames,
        datasets: [{
          label: 'Sold Plots',
          data: estateData.map(item => item.sold),
          backgroundColor: '#dc2626',
          borderColor: '#b91c1c',
          borderWidth: 1
        }]
      },
      options: {
        responsive: true,
        scales: {
          y: {
            beginAtZero: true
          }
        }
      }
    });
  </script>
<?php
$page_content = ob_get_clean();
include 'layout.php';
?>
