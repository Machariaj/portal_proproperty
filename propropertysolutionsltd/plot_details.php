<?php
session_start();
include 'db_connection.php';

if (!isset($_GET['plot_id'])) {
  echo "Invalid request.";
  exit;
}

$plot_id = intval($_GET['plot_id']);

// Fetch plot info
$plot = $conn->query("SELECT * FROM prop_plots WHERE id = $plot_id")->fetch_assoc();
if (!$plot) {
  echo "Plot not found.";
  exit;
}

// Fetch estate name
$estate = $conn->query("SELECT name FROM prop_estates WHERE id = {$plot['estate_id']}")->fetch_assoc();
$estate_name = $estate ? $estate['name'] : 'Unknown';

// Check booking/sale details
$booking = $conn->query("SELECT * FROM prop_bookings WHERE plot_id = $plot_id ORDER BY date_booked DESC LIMIT 1")->fetch_assoc();
$sale = $conn->query("SELECT * FROM prop_sales WHERE plot_id = $plot_id ORDER BY date_sold DESC LIMIT 1")->fetch_assoc();

$page_title = 'Plot Details - ' . htmlspecialchars($plot['plot_number']);

ob_start();
?>
  <div class="top-bar">
    <h1>Plot Details</h1>
  </div>

  <div class="card">
    <div class="table-responsive">
      <table>
        <tr><th>Plot Number</th><td><?php echo htmlspecialchars($plot['plot_number']); ?></td></tr>
        <tr><th>Estate</th><td><?php echo htmlspecialchars($estate_name); ?></td></tr>
        <tr><th>Status</th><td><?php echo ucfirst(htmlspecialchars($plot['status'])); ?></td></tr>
      </table>
    </div>
  </div>

  <?php if ($plot['status'] == 'booked' && $booking): ?>
    <div class="card">
      <h3>Booking Details</h3>
      <div class="table-responsive">
        <table>
          <tr><th>Buyer Name</th><td><?php echo htmlspecialchars($booking['buyer_name']); ?></td></tr>
          <tr><th>Phone</th><td><?php echo htmlspecialchars($booking['buyer_phone']); ?></td></tr>
          <tr><th>Email</th><td><?php echo htmlspecialchars($booking['buyer_email']); ?></td></tr>
          <?php if (!empty($booking['notes'])): ?>
          <tr><th>Notes</th><td><?php echo nl2br(htmlspecialchars($booking['notes'])); ?></td></tr>
          <?php endif; ?>
          <tr><th>Agent Name</th><td><?php echo htmlspecialchars($booking['agent_name']); ?></td></tr>
          <tr><th>Date Booked</th><td><?php echo htmlspecialchars($booking['date_booked']); ?></td></tr>
        </table>
      </div>
    </div>
  <?php elseif ($plot['status'] == 'sold' && $sale): ?>
    <div class="card">
      <h3>Sale Details</h3>
      <div class="table-responsive">
        <table>
          <tr><th>Buyer Name</th><td><?php echo htmlspecialchars($sale['buyer_name']); ?></td></tr>
          <tr><th>Phone</th><td><?php echo htmlspecialchars($sale['buyer_phone']); ?></td></tr>
          <tr><th>Email</th><td><?php echo htmlspecialchars($sale['buyer_email']); ?></td></tr>
          <tr><th>Agent Name</th><td><?php echo htmlspecialchars($sale['agent_name']); ?></td></tr>
          <tr><th>Amount (Ksh)</th><td><?php echo number_format((float)$sale['amount']); ?></td></tr>
          <tr><th>Date Sold</th><td><?php echo htmlspecialchars($sale['date_sold']); ?></td></tr>
        </table>
      </div>
      <?php if ($_SESSION['role'] === 'admin'): ?>
      <button type="button" onclick="markSaSigned(<?php echo $plot_id; ?>)" class="btn-sa-signed" style="margin-top: 10px;">Mark as SA Signed</button>
      <?php endif; ?>
    </div>
  <?php else: ?>
    <div class="card"><p><em>No booking or sale details available for this plot.</em></p></div>
  <?php endif; ?>

  <a href="plots.php?estate_id=<?php echo (int)$plot['estate_id']; ?>&status=<?php echo urlencode($plot['status']); ?>" class="edit-btn">← Back to Plots</a>

  <script>
    function markSaSigned(plotId) {
      if (confirm('Are you sure you want to mark this plot as SA Signed?')) {
        const formData = new FormData();
        formData.append('action', 'make_sa_signed');
        formData.append('plot_id', plotId);
        fetch('update_plot_status.php', {
          method: 'POST',
          body: formData
        })
        .then(response => response.json())
        .then(data => {
          if (data.success) {
            alert('Plot marked as SA Signed successfully.');
            location.reload();
          } else {
            alert('Error: ' + (data.message || 'Unknown error'));
          }
        })
        .catch(error => {
          alert('Error: ' + error.message);
        });
      }
    }
  </script>
<?php
$page_content = ob_get_clean();
include 'layout.php';
?>
