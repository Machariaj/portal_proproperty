<?php
session_start();
include 'activity_log.php'; // Include activity logging
include 'db_connection.php'; // Include database connection

if ($_SERVER['REQUEST_METHOD'] == 'POST') {
  $email = $_POST['email'];
  $password = $_POST['password'];

  $stmt = $conn->prepare("SELECT * FROM prop_agents WHERE email = ?");
  $stmt->bind_param("s", $email);
  $stmt->execute();
  $user = $stmt->get_result()->fetch_assoc();

  if ($user && password_verify($password, $user['password'])) {
    $_SESSION['user_id'] = $user['id'];
    $_SESSION['user_name'] = $user['name'];
    $_SESSION['role'] = $user['role'];

    // Log successful login
    logLoginAttempt($email, true);

    // Compute base URL for robust redirects
    $protocol = (!empty($_SERVER['HTTPS']) && $_SERVER['HTTPS'] !== 'off') ? 'https' : 'http';
    $host = $_SERVER['HTTP_HOST'] ?? '';
    $scriptDir = rtrim(str_replace('\\', '/', dirname($_SERVER['SCRIPT_NAME'] ?? '')), '/');
    $base = $protocol . '://' . $host . $scriptDir . '/';

    if ($user['role'] == 'admin') {
      header("Location: " . $base . "admin_dashboard.php");
    } else {
      header("Location: " . $base . "agent_dashboard.php");
    }
    exit;
  } else {
    // Log failed login attempt
    logLoginAttempt($email, false);
    $error = "Invalid email or password.";
  }
} else {
  // Log page access for login page
  logPageAccess('login');
}
?>
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>ProProperty - Login</title>
  <style>
    body {
      font-family: Arial, sans-serif;
      background: #f3f6fa;
      display: flex;
      justify-content: center;
      align-items: center;
      height: 100vh;
    }
    .login-container {
      background: white;
      padding: 30px 40px;
      border-radius: 10px;
      box-shadow: 0 0 15px rgba(0,0,0,0.1);
      width: 350px;
      text-align: center;
    }
    .logo {
      max-width: 150px;
      height: auto;
      margin-bottom: 20px;
    }
    .login-container h2 {
      text-align: center;
      margin-bottom: 20px;
      color: #2c3e50;
    }
    input {
      width: 100%;
      padding: 10px;
      margin: 10px 0;
      border: 1px solid #ccc;
      border-radius: 5px;
    }
    .password-container {
      position: relative;
      width: 100%;
    }
    .password-container input {
      width: 100%;
      padding-right: 50px;
      box-sizing: border-box;
    }
    .password-container .toggle-password {
      position: absolute;
      right: 10px;
      top: 50%;
      transform: translateY(-50%);
      cursor: pointer;
      font-size: 18px;
      color: #666;
      user-select: none;
    }
    button {
      width: 100%;
      padding: 10px;
      background: #3498db;
      color: white;
      border: none;
      border-radius: 5px;
      cursor: pointer;
    }
    button:hover {
      background: #2980b9;
    }
    .error {
      color: red;
      text-align: center;
      font-size: 14px;
    }
  </style>
</head>
<body>
  <div class="login-container">
    <h2>ProProperty Login</h2>
    <img src="uploads/pro-property_logo.png" alt="ProProperty Logo" class="logo">
    <?php if (!empty($error)): ?>
      <p class="error"><?= htmlspecialchars($error) ?></p>
    <?php endif; ?>
    <form method="POST">
      <input type="email" name="email" placeholder="Email" required>
      <div class="password-container">
        <input type="password" id="password" name="password" placeholder="Password" required>
        <span class="toggle-password" onclick="togglePassword()">👁️</span>
      </div>
      <button type="submit">Login</button>
    </form>
  </div>

  <script>
    function togglePassword() {
      const passwordInput = document.getElementById('password');
      const toggleButton = document.querySelector('.toggle-password');
      if (passwordInput.type === 'password') {
        passwordInput.type = 'text';
        toggleButton.textContent = '🙈';
      } else {
        passwordInput.type = 'password';
        toggleButton.textContent = '👁️';
      }
    }
  </script>
</body>
</html>
