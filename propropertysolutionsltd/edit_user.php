<?php
session_start();
if (!isset($_SESSION['role']) || $_SESSION['role'] !== 'admin') {
  header('Location: index.php');
  exit;
}

include 'db_connection.php';

$errors = [];
$success = '';

$user_id = isset($_GET['id']) ? (int)$_GET['id'] : 0;
$user = null;

// Fetch user data
if ($user_id > 0) {
  $stmt = $conn->prepare("SELECT * FROM prop_agents WHERE id = ?");
  $stmt->bind_param("i", $user_id);
  $stmt->execute();
  $user = $stmt->get_result()->fetch_assoc();
  if (!$user) {
    header('Location: create_user.php');
    exit;
  }
} else {
  header('Location: create_user.php');
  exit;
}

if ($_SERVER['REQUEST_METHOD'] === 'POST') {
  $name = trim($_POST['name'] ?? '');
  $email = trim($_POST['email'] ?? '');
  $phone = trim($_POST['phone'] ?? '');
  $role = $_POST['role'] ?? 'agent';
  $password = $_POST['password'] ?? '';

  // Basic validation
  if ($name === '') $errors[] = 'Name is required';
  if ($email === '' || !filter_var($email, FILTER_VALIDATE_EMAIL)) $errors[] = 'Valid email is required';
  if ($phone !== '' && !preg_match('/^\+?[0-9\s\-\(\)]+$/', $phone)) $errors[] = 'Invalid phone number format';
  if (!in_array($role, ['admin', 'agent'], true)) $errors[] = 'Invalid role selected';

  // Check unique email (exclude current user)
  if (!$errors) {
    $stmt = $conn->prepare('SELECT COUNT(*) AS cnt FROM prop_agents WHERE email = ? AND id != ?');
    $stmt->bind_param('si', $email, $user_id);
    $stmt->execute();
    $res = $stmt->get_result()->fetch_assoc();
    if (($res['cnt'] ?? 0) > 0) {
      $errors[] = 'A user with that email already exists';
    }
  }

  // Only proceed if no errors so far
  if (!$errors) {
    if ($password !== '') {
      // Update with new password
      if (strlen($password) < 6) {
        $errors[] = 'Password must be at least 6 characters';
      } else {
        $hashed = password_hash($password, PASSWORD_DEFAULT);
        $update_stmt = $conn->prepare('UPDATE prop_agents SET name = ?, email = ?, phone = ?, password = ?, role = ? WHERE id = ?');
        $update_stmt->bind_param('sssssi', $name, $email, $phone, $hashed, $role, $user_id);
      }
    } else {
      // Update without changing password
      $update_stmt = $conn->prepare('UPDATE prop_agents SET name = ?, email = ?, phone = ?, role = ? WHERE id = ?');
      $update_stmt->bind_param('ssssi', $name, $email, $phone, $role, $user_id);
    }

    if (isset($update_stmt) && $update_stmt->execute()) {
      $success = 'User updated successfully';
      // Refresh user data
      $stmt = $conn->prepare("SELECT * FROM prop_agents WHERE id = ?");
      $stmt->bind_param("i", $user_id);
      $stmt->execute();
      $user = $stmt->get_result()->fetch_assoc();
    } else {
      $errors[] = 'Database error: ' . $conn->error;
    }
  }
}

$page_title = 'Edit User - Admin';

ob_start();
?>
  <div class="top-bar">
    <h1>Edit User</h1>
    <a href="create_user.php" class="add-btn">← Back to Users</a>
  </div>

  <?php if ($success): ?>
    <div class="card"><p class="message"><?php echo htmlspecialchars($success); ?></p></div>
  <?php endif; ?>

  <?php if ($errors): ?>
    <div class="card">
      <ul style="margin:0; padding-left: 18px; color:#dc2626;">
        <?php foreach ($errors as $err): ?>
          <li><?php echo htmlspecialchars($err); ?></li>
        <?php endforeach; ?>
      </ul>
    </div>
  <?php endif; ?>

  <div class="card" style="max-width: 640px;">
    <form method="POST" autocomplete="off">
      <label for="name">Full Name</label>
      <input type="text" id="name" name="name" required value="<?php echo htmlspecialchars($user['name']); ?>">

      <label for="email">Email</label>
      <input type="email" id="email" name="email" required value="<?php echo htmlspecialchars($user['email']); ?>">

      <label for="phone">Phone Number</label>
      <input type="tel" id="phone" name="phone" value="<?php echo htmlspecialchars($user['phone'] ?: ''); ?>" placeholder="Optional">

      <label for="password">New Password (leave blank to keep current)</label>
      <input type="password" id="password" name="password" placeholder="At least 6 characters">

      <label for="role">Role</label>
      <select id="role" name="role" required>
        <option value="agent" <?php echo ($user['role'] === 'agent') ? 'selected' : ''; ?>>Agent</option>
        <option value="admin" <?php echo ($user['role'] === 'admin') ? 'selected' : ''; ?>>Admin</option>
      </select>

      <button type="submit">Update User</button>
    </form>
  </div>
<?php
$page_content = ob_get_clean();
include 'layout.php';
?>
