<?php
session_start();
include 'activity_log.php'; // Include activity logging
include 'db_connection.php'; // Include database connection

// Log page access
logPageAccess('select_booking_type');

if (!isset($_SESSION['user_id']) || !in_array($_SESSION['role'], ['agent', 'admin'])) {
  header("Location: index.php");
  exit;
}

// Get plot_id or selected_plots
$plot_id = isset($_GET['plot_id']) ? intval($_GET['plot_id']) : 0;
$selected_plots = isset($_GET['selected_plots']) ? $_GET['selected_plots'] : [];
$estate_id = isset($_GET['estate_id']) ? intval($_GET['estate_id']) : 0;

// Validate that we have either a single plot or multiple plots
if ($plot_id == 0 && empty($selected_plots)) {
  die("Invalid request: No plot selected.");
}

// Fetch plot details for display
if ($plot_id > 0) {
  $plot_query = $conn->query("SELECT p.*, e.name AS estate_name FROM prop_plots p
                              JOIN prop_estates e ON p.estate_id = e.id
                              WHERE p.id = $plot_id");
  $plot = $plot_query ? $plot_query->fetch_assoc() : null;
  
  if (!$plot) {
    die("Plot not found or invalid ID.");
  }
  $page_title = "Select Booking Type - Plot " . htmlspecialchars($plot['plot_number']);
  $plot_info = "Plot " . htmlspecialchars($plot['plot_number']) . " - " . htmlspecialchars($plot['estate_name']);
} else {
  // Multiple plots
  $page_title = "Select Booking Type - Multiple Plots";
  $plot_info = count($selected_plots) . " plots selected";
}

ob_start();
?>
  <div class="card" style="max-width: 600px; margin: 50px auto;">
    <h2>Select Booking Type</h2>
    <p style="margin-bottom: 20px; color: #666;"><?php echo $plot_info; ?></p>
    
    <form method="GET" id="bookingTypeForm">
      <?php if ($plot_id > 0): ?>
        <input type="hidden" name="plot_id" value="<?php echo $plot_id; ?>">
      <?php else: ?>
        <?php foreach ($selected_plots as $pid): ?>
          <input type="hidden" name="selected_plots[]" value="<?php echo htmlspecialchars($pid); ?>">
        <?php endforeach; ?>
      <?php endif; ?>
      
      <?php if ($estate_id > 0): ?>
        <input type="hidden" name="estate_id" value="<?php echo $estate_id; ?>">
      <?php endif; ?>
      
      <label for="booking_type">Choose Booking Option:</label>
      <select name="booking_type" id="booking_type" required style="width: 100%; padding: 10px; margin-bottom: 20px; font-size: 16px;">
        <option value="">-- Select an option --</option>
        <option value="reserve">Reserve a plot</option>
        <option value="deposit">Booking with a deposit</option>
        <option value="sa">Signing sale agreement first</option>
      </select>
      
      <div style="display: flex; gap: 10px; justify-content: center;">
        <button type="submit" class="add-btn" style="padding: 12px 30px; font-size: 16px;">Continue</button>
        <button type="button" class="edit-btn" onclick="goBack()" style="padding: 12px 30px; font-size: 16px;">Cancel</button>
      </div>
    </form>
  </div>

  <script>
    document.getElementById('bookingTypeForm').addEventListener('submit', function(e) {
      e.preventDefault();

      const bookingType = document.getElementById('booking_type').value;
      if (!bookingType) {
        alert('Please select a booking option.');
        return;
      }

      const formData = new FormData(this);
      let redirectUrl = '';

      // Check if we have multiple plots or single plot
      const selectedPlots = formData.getAll('selected_plots[]');
      const plotId = formData.get('plot_id');
      const isMultiplePlots = selectedPlots.length > 0;

      if (bookingType === 'reserve') {
        // Redirect to book plot page (single) or bulk book page (multiple)
        if (isMultiplePlots) {
          // For multiple plots, use POST to bulk_book.php
          const form = document.createElement('form');
          form.method = 'POST';
          form.action = 'bulk_book.php';

          // Add hidden fields
          selectedPlots.forEach(plotId => {
            const input = document.createElement('input');
            input.type = 'hidden';
            input.name = 'selected_plots[]';
            input.value = plotId;
            form.appendChild(input);
          });

          const estateInput = document.createElement('input');
          estateInput.type = 'hidden';
          estateInput.name = 'estate_id';
          estateInput.value = formData.get('estate_id');
          form.appendChild(estateInput);

          const actionInput = document.createElement('input');
          actionInput.type = 'hidden';
          actionInput.name = 'bulk_action';
          actionInput.value = 'book';
          form.appendChild(actionInput);

          const statusInput = document.createElement('input');
          statusInput.type = 'hidden';
          statusInput.name = 'status';
          statusInput.value = 'available';
          form.appendChild(statusInput);

          document.body.appendChild(form);
          form.submit();
        } else {
          // Single plot - redirect to book_plot.php
          redirectUrl = 'book_plot.php';
          const params = new URLSearchParams();
          params.append('plot_id', plotId);
          window.location.href = redirectUrl + '?' + params.toString();
        }
      } else if (bookingType === 'deposit') {
        // Redirect to selling page with paying deposit first (grid color remains yellow/booked)
        redirectUrl = 'sell_plot.php';
        const params = new URLSearchParams();

        if (isMultiplePlots) {
          selectedPlots.forEach(plotId => params.append('selected_plots[]', plotId));
        } else {
          params.append('plot_id', plotId);
        }

        if (formData.get('estate_id')) {
          params.append('estate_id', formData.get('estate_id'));
        }
        params.set('deposit_timing', 'before');
        window.location.href = redirectUrl + '?' + params.toString();
      } else if (bookingType === 'sa') {
        // Redirect to selling page with paying deposit after signing SA (grid color remains yellow/booked)
        redirectUrl = 'sell_plot.php';
        const params = new URLSearchParams();

        if (isMultiplePlots) {
          selectedPlots.forEach(plotId => params.append('selected_plots[]', plotId));
        } else {
          params.append('plot_id', plotId);
        }

        if (formData.get('estate_id')) {
          params.append('estate_id', formData.get('estate_id'));
        }
        params.set('deposit_timing', 'after');
        window.location.href = redirectUrl + '?' + params.toString();
      }
    });

    function goBack() {
      window.history.back();
    }
  </script>
<?php
$page_content = ob_get_clean();
include "layout.php";
?>

