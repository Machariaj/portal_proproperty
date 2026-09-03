<!-- sidebar.php -->
<?php
session_start();
$role = $_SESSION['role'] ?? null;
?>
<div class="sidebar">
  <h2>ProProperty</h2>
  <a href="dashboard.php">Dashboard</a>
  <a href="estates.php">Estates</a>
  <?php if ($role === 'admin'): ?>
  <!--li><a href="add_estate.php">➕ Add Estate</a></li -->
  <!--li><a href="add_plot.php">🏡 Add Plots</a></li-->
  <a href="admin_sold_plots.php">💰 Sold Plots</a>
  <a href="admin_fully_paid_plots.php">💸 Fully Paid Plots</a>
  <a href="export_reports.php">📊 Export Reports</a>
  <?php endif; ?>
  <a href="pending_projects.php">Pending Projects</a>
  <a href="completed_projects.php">Completed Projects</a>
</div>

<style>
  body {
    margin: 0;
    font-family: Arial, sans-serif;
    display: flex;
  }
  .sidebar {
    width: 220px;
    background: #2c3e50;
    color: white;
    min-height: 100vh;
    padding: 20px 0;
    position: fixed;
    left: 0;
    top: 0;
  }
  .sidebar h2 {
    text-align: center;
    margin-bottom: 30px;
  }
  .sidebar a {
    display: block;
    color: white;
    padding: 12px 20px;
    text-decoration: none;
  }
  .sidebar a:hover {
    background: #34495e;
  }
  .main {
    margin-left: 220px;
    flex: 1;
    padding: 20px;
  }
</style>
