<?php
session_start();
if (!isset($_SESSION['role']) || $_SESSION['role'] !== 'admin') {
  header('Location: index.php');
  exit;
}

include 'db_connection.php';

$errors = [];
$success = '';

// Handle delete request
if (isset($_GET['delete']) && is_numeric($_GET['delete'])) {
  $delete_id = (int)$_GET['delete'];
  $delete_stmt = $conn->prepare("DELETE FROM prop_agents WHERE id = ?");
  $delete_stmt->bind_param("i", $delete_id);
  if ($delete_stmt->execute()) {
    $success = 'User deleted successfully';
  } else {
    $errors[] = 'Failed to delete user: ' . $conn->error;
  }
}


if ($_SERVER['REQUEST_METHOD'] === 'POST') {
  $name = trim($_POST['name'] ?? '');
  $email = trim($_POST['email'] ?? '');
  $password = $_POST['password'] ?? '';
  $phone = trim($_POST['phone'] ?? '');
  $role = $_POST['role'] ?? 'agent';

  // Basic validation
  if ($name === '') $errors[] = 'Name is required';
  if ($email === '' || !filter_var($email, FILTER_VALIDATE_EMAIL)) $errors[] = 'Valid email is required';
  if ($password === '' || strlen($password) < 6) $errors[] = 'Password must be at least 6 characters';
  if ($phone !== '' && !preg_match('/^\+?[0-9\s\-\(\)]+$/', $phone)) $errors[] = 'Invalid phone number format';
  if (!in_array($role, ['admin', 'agent'], true)) $errors[] = 'Invalid role selected';

  // Only proceed if no errors so far
  if (!$errors) {
    // Check unique email
    $stmt = $conn->prepare('SELECT COUNT(*) AS cnt FROM prop_agents WHERE email = ?');
    $stmt->bind_param('s', $email);
    $stmt->execute();
    $res = $stmt->get_result()->fetch_assoc();
    if (($res['cnt'] ?? 0) > 0) {
      $errors[] = 'A user with that email already exists';
    } else {
      // Hash password and insert
      $hashed = password_hash($password, PASSWORD_DEFAULT);
      $ins = $conn->prepare('INSERT INTO prop_agents (name, email, password, phone, role) VALUES (?, ?, ?, ?, ?)');
      $ins->bind_param('sssss', $name, $email, $hashed, $phone, $role);
      if ($ins->execute()) {
        $success = 'User created successfully';
      } else {
        $errors[] = 'Database error: ' . $conn->error;
      }
    }
  }
}

$page_title = 'Create User - Admin';

// Fetch all users
$users_query = $conn->query("SELECT id, name, email, phone, role FROM prop_agents ORDER BY name");

ob_start();
?>
  <div class="top-bar">
    <h1 style="display:flex; align-items:center; gap:10px;">👤 Create User</h1>
    <p style="margin-top:6px; color:#667085;">Add a new team member and manage existing users.</p>
  </div>

  <div id="create-form" class="card" style="max-width: 800px;">
    <h3 style="display:flex; align-items:center; gap:8px; margin-bottom:10px;">➕ Create New User</h3>
    <p style="margin-top:0; color:#667085;">Provide user details below. Password must be at least 6 characters.</p>

    <?php if ($success): ?>
      <div class="alert success">✅ <?php echo htmlspecialchars($success); ?></div>
    <?php endif; ?>

    <?php if ($errors): ?>
      <div class="alert error">
        <strong>There were some problems:</strong>
        <ul>
          <?php foreach ($errors as $err): ?>
            <li><?php echo htmlspecialchars($err); ?></li>
          <?php endforeach; ?>
        </ul>
      </div>
    <?php endif; ?>

    <form method="POST" autocomplete="off" class="form-grid">
      <div class="form-field">
        <label for="name">Full Name</label>
        <input type="text" id="name" name="name" required value="<?php echo isset($_POST['name']) ? htmlspecialchars($_POST['name']) : '';?>" placeholder="e.g. Jane Doe">
      </div>

      <div class="form-field">
        <label for="email">Email</label>
        <input type="email" id="email" name="email" required value="<?php echo isset($_POST['email']) ? htmlspecialchars($_POST['email']) : '';?>" placeholder="name@company.com">
      </div>

      <div class="form-field span-2">
        <label for="password">Password</label>
        <div class="password-wrapper">
          <input type="password" id="password" name="password" required placeholder="At least 6 characters">
          <button type="button" id="toggle-password" class="toggle-password-btn" title="Show/Hide">👁️</button>
        </div>
        <div class="strength">
          <div id="strength-bar"></div>
          <span id="strength-text">Strength: -</span>
        </div>
      </div>

      <div class="form-field">
        <label for="phone">Phone Number <span class="muted">(optional)</span></label>
        <input type="tel" id="phone" name="phone" value="<?php echo isset($_POST['phone']) ? htmlspecialchars($_POST['phone']) : '';?>" placeholder="e.g. +254 700 000 000">
      </div>

      <div class="form-field">
        <label for="role">Role</label>
        <select id="role" name="role" required>
          <option value="agent" <?php echo (($_POST['role'] ?? '')==='agent') ? 'selected' : '';?>>Agent</option>
          <option value="admin" <?php echo (($_POST['role'] ?? '')==='admin') ? 'selected' : '';?>>Admin</option>
        </select>
      </div>

      <div class="form-actions span-2">
        <button type="submit" class="primary">Create User</button>
        <a href="admin_dashboard.php" class="link-btn">Cancel</a>
      </div>
    </form>
  </div>

  <div class="card">
    <div style="display:flex; align-items:center; justify-content:space-between; gap:12px; flex-wrap:wrap;">
      <h3 style="margin:0; display:flex; align-items:center; gap:8px;">👥 Existing Users</h3>
      <div style="display:flex; gap:8px; align-items:center;">
        <input id="userFilter" type="text" placeholder="Search users..." style="padding:8px 10px; border:1px solid #e5e7eb; border-radius:6px;">
        <select id="roleFilter" style="padding:8px 10px; border:1px solid #e5e7eb; border-radius:6px;">
          <option value="">All Roles</option>
          <option value="agent">Agent</option>
          <option value="admin">Admin</option>
        </select>
      </div>
    </div>
    <table class="users-table" style="margin-top:12px;">
      <thead>
        <tr>
          <th>Name</th>
          <th>Email</th>
          <th>Phone</th>
          <th>Role</th>
          <th>Actions</th>
        </tr>
      </thead>
      <tbody>
        <?php while ($user = $users_query->fetch_assoc()): ?>
          <tr>
            <td><?php echo htmlspecialchars($user['name']); ?></td>
            <td><?php echo htmlspecialchars($user['email']); ?></td>
            <td><?php echo htmlspecialchars($user['phone'] ?: 'N/A'); ?></td>
            <td><?php echo htmlspecialchars(ucfirst($user['role'])); ?></td>
            <td>
              <a href="edit_user.php?id=<?php echo $user['id']; ?>" class="edit-btn">Edit</a>
              <a href="?delete=<?php echo $user['id']; ?>" class="delete-btn" onclick="return confirm('Are you sure you want to delete this user?')">Delete</a>
            </td>
          </tr>
        <?php endwhile; ?>
      </tbody>
    </table>
  </div>
<?php
$page_content = ob_get_clean();
include 'layout.php';
?>

<script>
(function() {
  const pwd = document.getElementById('password');
  const toggle = document.getElementById('toggle-password');
  const bar = document.getElementById('strength-bar');
  const text = document.getElementById('strength-text');
  const email = document.getElementById('email');
  const phone = document.getElementById('phone');

  function scorePassword(p) {
    let score = 0;
    if (!p) return score;
    // length
    if (p.length >= 6) score += 1;
    if (p.length >= 10) score += 1;
    // variety
    if (/[a-z]/.test(p)) score += 1;
    if (/[A-Z]/.test(p)) score += 1;
    if (/[0-9]/.test(p)) score += 1;
    if (/[^A-Za-z0-9]/.test(p)) score += 1;
    return Math.min(score, 6);
  }

  function updateStrength() {
    const s = scorePassword(pwd.value);
    const perc = (s / 6) * 100;
    bar.style.width = perc + '%';
    let label = 'Weak';
    let color = '#ef4444';
    if (s >= 4) { label = 'Good'; color = '#f59e0b'; }
    if (s >= 5) { label = 'Strong'; color = '#10b981'; }
    bar.style.backgroundColor = color;
    text.textContent = 'Strength: ' + label;
  }

  if (pwd) {
    pwd.addEventListener('input', updateStrength);
    updateStrength();
  }

  // Password toggle is handled globally in layout.php to avoid duplicate handlers.

  // Simple real-time validation hinting
  if (email) {
    email.addEventListener('input', function(){
      email.classList.toggle('invalid', email.value && !/^\S+@\S+\.\S+$/.test(email.value));
    });
  }
  if (phone) {
    phone.addEventListener('input', function(){
      phone.classList.toggle('invalid', phone.value && !/^\+?[0-9\s\-\(\)]+$/.test(phone.value));
    });
  }

  // Table filtering
  const filterInput = document.getElementById('userFilter');
  const roleFilter = document.getElementById('roleFilter');
  function applyFilter() {
    const q = (filterInput?.value || '').toLowerCase();
    const roleQ = (roleFilter?.value || '').toLowerCase();
    document.querySelectorAll('table.users-table tbody tr').forEach(tr => {
      const tds = tr.querySelectorAll('td');
      const name = (tds[0]?.textContent || '').toLowerCase();
      const email = (tds[1]?.textContent || '').toLowerCase();
      const phone = (tds[2]?.textContent || '').toLowerCase();
      const role = (tds[3]?.textContent || '').toLowerCase();
      const matchText = name.includes(q) || email.includes(q) || phone.includes(q);
      const matchRole = !roleFilter.value || role === roleQ;
      tr.style.display = (matchText && matchRole) ? '' : 'none';
    });
  }
  if (filterInput) filterInput.addEventListener('input', applyFilter);
  if (roleFilter) roleFilter.addEventListener('change', applyFilter);
})();
</script>

<style>
/* Alerts */
.alert { padding: 10px 12px; border-radius: 8px; margin: 10px 0; }
.alert.success { background: #ecfdf5; color: #065f46; border: 1px solid #a7f3d0; }
.alert.error { background: #fef2f2; color: #991b1b; border: 1px solid #fecaca; }
.alert.error ul { margin: 6px 0 0 18px; }

/* Form grid */
.form-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 14px 16px; }
.form-grid .span-2 { grid-column: span 2; }
.form-field label { display:block; font-weight:600; margin-bottom:6px; color:#344054; }
.form-field input, .form-field select { width:100%; padding:10px 12px; border:1px solid #e5e7eb; border-radius:8px; outline:none; transition: box-shadow .2s, border-color .2s; }
.form-field input:focus, .form-field select:focus { border-color:#93c5fd; box-shadow: 0 0 0 4px rgba(59,130,246,.15); }
.muted { color:#98a2b3; font-weight: normal; }

.password-wrapper { position: relative; }
.toggle-password-btn { position:absolute; right:8px; top:50%; transform: translateY(-50%); background:#f3f4f6; border:1px solid #e5e7eb; border-radius:6px; padding:4px 6px; cursor:pointer; z-index:2; }
.toggle-password-btn:hover { background:#e5e7eb; }

.strength { display:flex; align-items:center; gap:10px; margin-top:6px; }
#strength-bar { height:6px; width:0; background:#ef4444; border-radius:4px; transition: width .3s ease, background-color .3s ease; flex: 0 0 140px; }
#strength-text { color:#6b7280; font-size: 12px; }

.form-actions { display:flex; gap:10px; align-items:center; }
.form-actions .primary { background:#2563eb; color:#fff; border:none; padding:10px 16px; border-radius:8px; cursor:pointer; }
.form-actions .primary:hover { background:#1d4ed8; }
.form-actions .link-btn { padding:10px 12px; color:#374151; text-decoration:none; }
.form-actions .link-btn:hover { text-decoration:underline; }

/* Table */
.users-table { width:100%; border-collapse: collapse; }
.users-table th, .users-table td { padding:10px 12px; border-bottom:1px solid #f1f5f9; }
.users-table thead th { background:#f8fafc; color:#475569; font-weight:700; }
.users-table tbody tr:hover { background:#f8fafc; }

/* Validation hints */
input.invalid { border-color:#fca5a5 !important; box-shadow: 0 0 0 3px rgba(239,68,68,.15) !important; }
</style>
