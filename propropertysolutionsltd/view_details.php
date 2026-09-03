<?php
include 'db_connection.php';

$plot_id = $_GET['plot_id'] ?? 0;
$q = "
SELECT t.status, c.name AS client_name, c.phone AS client_phone, c.email AS client_email,
       a.name AS agent_name, a.phone AS agent_phone, a.email AS agent_email
FROM prop_transactions t
JOIN prop_clients c ON c.id = t.client_id
JOIN prop_agents a ON a.id = t.agent_id
WHERE t.plot_id = $plot_id
ORDER BY t.date DESC LIMIT 1";

$details = $conn->query($q)->fetch_assoc();

$page_title = 'Plot Details - ProProperty';

ob_start();
?>
  <div class="top-bar">
    <h1>Plot Transaction Details</h1>
  </div>

  <div class="card">
    <?php if ($details): ?>
      <h3>Status: <?php echo ucfirst(htmlspecialchars($details['status'])); ?></h3>
      <p><strong>Client:</strong> <?php echo htmlspecialchars($details['client_name']); ?> (<?php echo htmlspecialchars($details['client_phone']); ?>, <?php echo htmlspecialchars($details['client_email']); ?>)</p>
      <p><strong>Agent:</strong> <?php echo htmlspecialchars($details['agent_name']); ?> (<?php echo htmlspecialchars($details['agent_phone']); ?>, <?php echo htmlspecialchars($details['agent_email']); ?>)</p>
    <?php else: ?>
      <p>No transaction details found for this plot.</p>
    <?php endif; ?>
  </div>
<?php
$page_content = ob_get_clean();
include 'layout.php';
?>
