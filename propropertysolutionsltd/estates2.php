<?php
include 'db.php';

// Fetch estates
$estates = $conn->query("SELECT * FROM prop_estates");

$page_title = 'Estates - ProProperty';

ob_start();
?>
  <div class="top-bar">
    <h1>Estates</h1>
  </div>

  <?php while($estate = $estates->fetch_assoc()): ?>
    <div class="card">
      <h2 style="margin-top: 0;"><?= htmlspecialchars($estate['name']) ?></h2>

      <div class="estate-buttons">
        <a href="javascript:void(0)" class="btn-available" onclick="loadPlots(<?= (int)$estate['id'] ?>,'available')">Available</a>
        <a href="javascript:void(0)" class="btn-booked" onclick="loadPlots(<?= (int)$estate['id'] ?>,'booked')">Booked</a>
        <a href="javascript:void(0)" class="btn-sold" onclick="loadPlots(<?= (int)$estate['id'] ?>,'sold')">Sold</a>
      </div>

      <div id="plots-<?= (int)$estate['id'] ?>" style="margin-top: 12px;"></div>
    </div>
  <?php endwhile; ?>

  <script>
  function loadPlots(estateId, status) {
    fetch(`get_plots.php?estate_id=${estateId}&status=${status}`)
      .then(res => res.json())
      .then(data => {
        const container = document.getElementById("plots-" + estateId);
        container.innerHTML = "";
        if(data.length === 0){
          container.innerHTML = "<div class='card'><p>No plots found.</p></div>";
          return;
        }
        data.forEach(plot => {
          let actions = "";
          if(status === "available") {
            actions = `<a class='add-btn' href='book_plot.php?plot_id=${plot.id}'>Book</a> <a class='btn-sold' href='sell_plot.php?plot_id=${plot.id}'>Sell</a>`;
          } else if(status === "booked") {
            actions = `<a class='btn-sold' href='sell_plot.php?plot_id=${plot.id}'>Sell</a> <a class='edit-btn' href='plot_details.php?plot_id=${plot.id}'>Details</a>`;
          } else if(status === "sold") {
            actions = `<a class='edit-btn' href='plot_details.php?plot_id=${plot.id}'>Details</a>`;
          }

          container.innerHTML += `
            <div class="card" style="padding: 12px;">
              <strong>Plot ${plot.plot_number}</strong><br>
              <div style="margin-top: 8px; display: flex; gap: 8px;">${actions}</div>
            </div>
          `;
        });
      });
  }
  </script>
<?php
$page_content = ob_get_clean();
include 'layout.php';
?>
