<?php
session_start();

if (!isset($_SESSION['user_id']) || $_SESSION['role'] !== 'admin') {
  header("Location: index.php");
  exit;
}

$page_title = 'Export Reports - ProProperty';

ob_start();
?>
  <div class="top-bar">
    <h1>Export Reports</h1>
  </div>

  <div class="card">
    <p>Download CSV reports for plots by status. For booked, sold, and sa_signed reports, you can filter by date range.</p>
    <form method="GET" action="export_csv.php" style="margin-bottom: 20px;">
      <label for="status">Report Type:</label>
      <select name="status" id="status" required>
        <option value="all">All Plots</option>
        <option value="available">Available Plots</option>
        <option value="booked">Booked Plots</option>
        <option value="sold">Sold Plots</option>
        <option value="sa_signed">SA Signed Plots</option>
      </select>
      <label for="from_date">From Date (optional for booked/sold/sa_signed):</label>
      <input type="date" name="from_date" id="from_date">
      <label for="to_date">To Date (optional for booked/sold/sa_signed):</label>
      <input type="date" name="to_date" id="to_date">
      <button type="submit" class="add-btn">📄 Download CSV</button>
    </form>
  </div>
<?php
$page_content = ob_get_clean();
include 'layout.php';
?>