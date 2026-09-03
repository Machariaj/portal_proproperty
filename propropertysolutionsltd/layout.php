<?php
// layout.php
if (session_status() === PHP_SESSION_NONE) { session_start(); }
$role = $_SESSION['role'] ?? 'guest';
$userName = $_SESSION['user_name'] ?? null;
// Compute base URL for safe linking
$protocol = (!empty($_SERVER['HTTPS']) && $_SERVER['HTTPS'] !== 'off') ? 'https' : 'http';
$host = $_SERVER['HTTP_HOST'] ?? '';
$scriptDir = rtrim(str_replace('\\', '/', dirname($_SERVER['SCRIPT_NAME'] ?? '')), '/');
$base = $protocol . '://' . $host . $scriptDir . '/';
?>
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title><?php echo isset($page_title) ? $page_title : "ProProperty"; ?></title>
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <link rel="icon" href="<?= $base ?>uploads/pro-property_logo.png" type="image/png">
  <link rel="stylesheet" href="<?= $base ?>assets/css/styles.css">
</head>
<body>

  <button class="menu-toggle" onclick="toggleMenu()">☰ Menu</button>

  <div class="sidebar">
    <div class="logo-container">
      <img src="<?= $base ?>uploads/pro-property_logo.png" alt="Logo" class="logo" onerror="this.style.display='none'; this.nextElementSibling.style.display='block';" />
      <div style="display: none; padding: 10px; color: #fff; font-size: 18px; font-weight: bold;">ProProperty</div>
      <h2>Pro-Property</h2>
    </div>
    <ul>
      <?php if ($role === 'agent'): ?>
        <li><a href="<?= $base ?>agent_dashboard.php">Dashboard</a></li>
        <li><a href="<?= $base ?>estates.php">Estates</a></li>
        <li><a href="<?= $base ?>logout.php">Logout</a></li>
      <?php else: ?>
        <li><a href="<?= $base ?>admin_dashboard.php">Dashboard</a></li>
        <li><a href="<?= $base ?>estates.php">Estates</a></li>
        <li><a href="<?= $base ?>add_estate.php">➕ Add Estate</a></li>
        <li><a href="<?= $base ?>add_plot.php">🏡 Add Plots</a></li>
        <li><a href="<?= $base ?>create_user.php">👤 Create User</a></li>
        <li>
          <select onchange="if(this.value) window.location.href='<?= $base ?>admin_booked_plots.php?booking_type=' + this.value; else window.location.href='<?= $base ?>admin_booked_plots.php';" style="width: 100%; padding: 8px; background: #34495e; color: white; border: 1px solid #34495e; border-radius: 4px; font-size: 14px;">
            <option value="">📋 Booked Plots</option>
            <option value="reserve">Reserved Plots</option>
            <option value="deposit">Paid Deposit</option>
            <option value="sa">Signing Sale Agreement First</option>
          </select>
        </li>
        <li><a href="<?= $base ?>admin_sa_signed_plots.php">📝 Signed Plots</a></li>
        <li><a href="<?= $base ?>admin_sold_plots.php">💰 Sold Plots</a></li>
        <li><a href="<?= $base ?>pending_projects.php">Pending Projects</a></li>
        <li><a href="<?= $base ?>completed_projects.php">Completed Projects</a></li>
        <li><a href="<?= $base ?>export_reports.php">📊 Export Reports</a></li>
        <li><a href="<?= $base ?>logout.php">Logout</a></li>
      <?php endif; ?>
    </ul>
    <?php if ($role === 'admin' && basename($_SERVER['REQUEST_URI']) === 'admin_booked_plots.php'): ?>
    <!--div style="padding: 20px; border-top: 1px solid #34495e; margin-top: 20px;">
      <label for="booking_type_sidebar" style="display: block; margin-bottom: 5px; font-size: 14px; color: white;">Filter by Booking Type:</label>
      <select name="booking_type" id="booking_type_sidebar" style="width: 100%; padding: 5px;">
        <option value="">All Types</option>
        <option value="reserve" <?php echo (isset($_GET['booking_type']) && $_GET['booking_type'] == 'reserve') ? 'selected' : ''; ?>>Reserve</option>
        <option value="deposit" <?php echo (isset($_GET['booking_type']) && $_GET['booking_type'] == 'deposit') ? 'selected' : ''; ?>>Booking with deposit</option>
        <option value="sa" <?php echo (isset($_GET['booking_type']) && $_GET['booking_type'] == 'sa') ? 'selected' : ''; ?>>Signing sale agreement first</option>
      </select>
    </div-->
    <?php endif; ?>
  </div>

  <div class="main">
    <?php
    // This is where each page’s content will go
    if (isset($page_content)) {
      echo $page_content;
    }
    ?>
  </div>

  <div class="lightbox" id="lightbox" onclick="closeLightbox(event)">
    <img id="lightboxImg" src="" alt="Preview">
  </div>

  <script>
    function toggleMenu() {
      const sidebar = document.querySelector('.sidebar');
      sidebar.classList.toggle('sidebar--open');
    }
    // Simple lightbox for estate images
    document.addEventListener('click', function(e) {
      const img = e.target.closest('.estate-card img');
      if (!img) return;
      const lightbox = document.getElementById('lightbox');
      const lightboxImg = document.getElementById('lightboxImg');
      lightboxImg.src = img.src;
      lightbox.classList.add('open');
    });
    function closeLightbox(event) {
      const lightbox = document.getElementById('lightbox');
      if (event.target.id === 'lightbox' || event.target.id === 'lightboxImg') {
        lightbox.classList.remove('open');
      }
    }
    document.addEventListener('keydown', function(e) {
      if (e.key === 'Escape') {
        const lightbox = document.getElementById('lightbox');
        lightbox.classList.remove('open');
      }
    });
    // Password toggle
    document.addEventListener('DOMContentLoaded', function() {
      const toggleBtn = document.getElementById('toggle-password');
      if (toggleBtn) {
        toggleBtn.addEventListener('click', function() {
          const input = document.getElementById('password');
          if (input.type === 'password') {
            input.type = 'text';
            this.textContent = '🙈';
          } else {
            input.type = 'password';
            this.textContent = '👁️';
          }
        });
      }
    });
  </script>
</body>
</html>

