<?php
include 'db_connection.php';

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
list($agents, $sales) = extractChartData($agent_query, 'agent_name', 'total_sales');
?>
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>ProProperty - Dashboard</title>
  <script src="https://cdn.jsdelivr.net/npm/chart.js"></script>
  <style>
    body {
      margin: 0;
      font-family: Arial, sans-serif;
      display: flex;
    }

    /* Sidebar */
    .sidebar {
      width: 250px;
      background: #2c3e50;
      color: white;
      height: 100vh;
      position: fixed;
      left: 0;
      top: 0;
      padding-top: 20px;
    }
    .sidebar .logo-container {
      text-align: center;
      margin-bottom: 20px;
    }
    .sidebar img.logo {
      width: 60px;
      border-radius: 10px;
    }
    .sidebar h2 {
      font-size: 18px;
      margin-top: 5px;
    }
    .sidebar ul {
      list-style: none;
      padding: 0;
    }
    .sidebar ul li {
      margin: 15px 0;
    }
    .sidebar ul li a {
      color: white;
      text-decoration: none;
      display: block;
      padding: 10px 20px;
    }
    .sidebar ul li a:hover {
      background: #34495e;
    }

    /* Main content */
    .main-content {
      margin-left: 250px;
      padding: 20px;
      flex-grow: 1;
    }

    h1 {
      color: #2c3e50;
      margin-bottom: 20px;
    }

    .filter {
      margin-bottom: 20px;
    }

    select {
      padding: 8px;
      border-radius: 5px;
      border: 1px solid #ccc;
    }

    /* Charts layout */
    .charts-grid {
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(500px, 1fr));
      gap: 20px;
    }

    .chart-container {
      background: #f4f6f7;
      padding: 20px;
      border-radius: 10px;
      box-shadow: 0 3px 10px rgba(0,0,0,0.1);
      height: 400px;
    }

    canvas {
      width: 100%;
      height: 300px !important;
    }
  </style>
</head>
<body>
  <!-- Sidebar -->
  <div class="sidebar">
    <div class="logo-container">
      <img src="uploads/pro-property_logo.png" alt="Logo" class="logo">
      <h2>Pro-Property Solutions Limited</h2>
    </div>
    <ul>
      <li><a href="index.php">Dashboard</a></li>
      <li><a href="estates.php">Estates</a></li>
      <li><a href="plots.php">Pending Projects</a></li>
      <li><a href="bookings.php">Completed Projects</a></li>
      <li><a href="logout.php">Logout</a></li>

    </ul>
  </div>

  <!-- Main Dashboard -->
  <div class="main-content">
    <h1>Dashboard Overview</h1>

    <div class="filter">
      <label for="month">Filter by Month:</label>
      <select id="month">
        <option value="">All Months</option>
        <?php foreach ($months as $month): ?>
          <option value="<?= htmlspecialchars($month) ?>"><?= htmlspecialchars($month) ?></option>
        <?php endforeach; ?>
      </select>
    </div>

    <div class="charts-grid">
      <div class="chart-container">
        <h3>Total Plots per Estate</h3>
        <canvas id="totalChart"></canvas>
      </div>

      <div class="chart-container">
        <h3>Available Plots per Estate</h3>
        <canvas id="availableChart"></canvas>
      </div>

      <div class="chart-container">
        <h3>Booked Plots per Estate</h3>
        <canvas id="bookedChart"></canvas>
      </div>

      <div class="chart-container">
        <h3>Sold Plots per Estate</h3>
        <canvas id="soldChart"></canvas>
      </div>

      <div class="chart-container" style="grid-column: span 2;">
        <h3>Top Sales Agents</h3>
        <canvas id="agentsChart"></canvas>
      </div>
    </div>
  </div>

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
        options: { responsive: true, scales: { y: { beginAtZero: true } } }
      });
    };

    createChart('totalChart', <?= json_encode($estates_total) ?>, <?= json_encode($plots_total) ?>, 'Total Plots', 'rgba(52,152,219,0.6)');
    createChart('availableChart', <?= json_encode($estates_available) ?>, <?= json_encode($plots_available) ?>, 'Available Plots', 'rgba(46,204,113,0.6)');
    createChart('bookedChart', <?= json_encode($estates_booked) ?>, <?= json_encode($plots_booked) ?>, 'Booked Plots', 'rgba(241,196,15,0.6)');
    createChart('soldChart', <?= json_encode($estates_sold) ?>, <?= json_encode($plots_sold) ?>, 'Sold Plots', 'rgba(231,76,60,0.6)');
    createChart('agentsChart', <?= json_encode($agents) ?>, <?= json_encode($sales) ?>, 'Top Sales Agents', 'rgba(155,89,182,0.6)');
  </script>
</body>
</html>
