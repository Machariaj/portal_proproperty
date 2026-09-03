<?php
$page_title = 'Dashboard - ProProperty';

ob_start();
?>
  <div class="top-bar">
    <h1>Dashboard</h1>
  </div>

  <div class="card">
    <div style="display: grid; grid-template-columns: repeat(auto-fit, minmax(300px, 1fr)); gap: 16px;">
      <div class="card">
        <h3>Plots Sold per Estate</h3>
        <div style="height: 220px;">
          <canvas id="soldChart"></canvas>
        </div>
      </div>

      <div class="card">
        <h3>Plots Booked per Estate</h3>
        <div style="height: 220px;">
          <canvas id="bookedChart"></canvas>
        </div>
      </div>

      <div class="card">
        <h3>Plots Available per Estate</h3>
        <div style="height: 220px;">
          <canvas id="availableChart"></canvas>
        </div>
      </div>
    </div>
  </div>

  <script src="https://cdn.jsdelivr.net/npm/chart.js"></script>
  <script>
    const commonOptions = {
      responsive: true,
      maintainAspectRatio: false,
      scales: { y: { beginAtZero: true } }
    };

    new Chart(document.getElementById('soldChart'), {
      type: 'bar',
      data: {
        labels: ['Estate A', 'Estate B', 'Estate C'],
        datasets: [{
          label: 'Sold',
          data: [5, 8, 4],
          backgroundColor: 'rgba(75, 192, 192, 0.6)'
        }]
      },
      options: commonOptions
    });

    new Chart(document.getElementById('bookedChart'), {
      type: 'bar',
      data: {
        labels: ['Estate A', 'Estate B', 'Estate C'],
        datasets: [{
          label: 'Booked',
          data: [3, 5, 2],
          backgroundColor: 'rgba(255, 206, 86, 0.6)'
        }]
      },
      options: commonOptions
    });

    new Chart(document.getElementById('availableChart'), {
      type: 'bar',
      data: {
        labels: ['Estate A', 'Estate B', 'Estate C'],
        datasets: [{
          label: 'Available',
          data: [12, 7, 10],
          backgroundColor: 'rgba(54, 162, 235, 0.6)'
        }]
      },
      options: commonOptions
    });
  </script>
<?php
$page_content = ob_get_clean();
include 'layout.php';
?>
