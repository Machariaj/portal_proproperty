<?php
error_reporting(E_ALL);
ini_set('display_errors', 0);
ini_set('max_execution_time', 290); // Increase to 2 minutes for slow API calls
session_start();
include 'activity_log.php'; // Include activity logging
include 'db_connection.php'; // Include database connection

// Include shared Zoho functions
include 'zoho_functions.php';

// Include PHPMailer
require __DIR__ . '/PHPMailer/src/Exception.php';
require __DIR__ . '/PHPMailer/src/PHPMailer.php';
require __DIR__ . '/PHPMailer/src/SMTP.php';

// Removed: getZohoAccessToken function - now using shared function from zoho_functions.php

// Function to upload file
function uploadFile($field_name, $upload_dir) {
    if (isset($_FILES[$field_name]) && $_FILES[$field_name]['error'] == 0) {
        $file_name = basename($_FILES[$field_name]['name']);
        $file_path = $upload_dir . uniqid() . '_' . $file_name;
        if (move_uploaded_file($_FILES[$field_name]['tmp_name'], $file_path)) {
            return $file_path;
        }
    }
    return null;
}

// Function to send data to Zoho CRM
function sendToZohoCRM($buyer_name, $buyer_phone, $buyer_email, $plot_number, $estate_name, $amount, $agent_name, $payment_plan, $deposit_doc, $id_doc, $kra_doc, $passport_photo, $description = null) {
    date_default_timezone_set('Africa/Nairobi'); // Set timezone to East Africa Time (EAT)
    $log_file = 'zoho_upload_log.txt';
    $timestamp = date('Y-m-d H:i:s');

    $log_entry = "[$timestamp] 🔄 Starting Zoho CRM integration for buyer: $buyer_name\n";
    file_put_contents($log_file, $log_entry, FILE_APPEND);
    error_log($log_entry);

    // Create Deal directly in Zoho CRM with buyer data
    $dealData = [
        'data' => [
            [
                'Deal_Name' => $buyer_name,
                'Phone_Number' => $buyer_phone,
                'Buyer_Email' => $buyer_email,
                'Deposit' => (float)$amount,
                'Payment_Plan' => $payment_plan,
                'Agent_Name' => $agent_name,
                'Estates' => $estate_name,
                'Plot' => (string)$plot_number,
                'Stage' => 'Closed Won', // Assuming the sale is completed
                'Closing_Date' => date('Y-m-d'),
                'Description' => ($description ?: "Plot: $plot_number, Estate: $estate_name, Agent: $agent_name")
            ]
        ]
    ];

    $deal_id = createZohoDeal($dealData);

    if ($deal_id) {
        // Log Zoho CRM deal creation
        logZohoActivity('deal_create', 'success', "Deal ID: $deal_id, Buyer: $buyer_name, Amount: $amount");

        // Create customer in Zoho Books
        $customerData = [
            'contact_name' => $buyer_name,
            'contact_type' => 'customer',
            'email' => $buyer_email,
            'phone' => $buyer_phone
        ];

        // Prepare notes with plot details
        $notes = "Estate: $estate_name, Plot: $plot_number, Sale Date: " . date('Y-m-d');

        // Prepare contact persons with buyer details (primary) and agent
        $contactPersons = [];
        // Primary contact person with buyer details
        $contactPersons[] = [
            'first_name' => explode(' ', $buyer_name)[0] ?? $buyer_name,
            'last_name' => explode(' ', $buyer_name, 2)[1] ?? '',
            'salutation' => '',
            'email' => $buyer_email,
            'mobile' => $buyer_phone,
            'is_primary_contact' => true
        ];
        // Agent as additional contact person
        $nameParts = explode(' ', $agent_name, 2);
        $firstName = $nameParts[0] ?? '';
        $lastName = $nameParts[1] ?? '';
        $contactPersons[] = [
            'first_name' => $firstName,
            'last_name' => $lastName,
            'salutation' => '',
            'email' => '', // Agent email not available
            'mobile' => ''  // Agent phone not available
        ];

        $customer_id = createZohoBooksCustomer($customerData, $notes, $contactPersons);
        if ($customer_id) {
            logZohoActivity('customer_create', 'success', "Customer ID: $customer_id, Buyer: $buyer_name");
        } else {
            logZohoActivity('customer_create', 'failed', "Buyer: $buyer_name");
            return false;
        }

        // Upload attachments to the created deal
        $attachments = [
            'Deposit Reference' => $deposit_doc,
            'ID Photo' => $id_doc,
            'KRA' => $kra_doc,
            'Passport Photo' => $passport_photo
        ];

        $upload_success_count = 0;
        foreach ($attachments as $name => $file_path) {
            if ($file_path && file_exists($file_path)) {
                if (uploadZohoAttachment($deal_id, $file_path, $name, 'Deals')) {
                    $upload_success_count++;
                    // Log successful attachment upload
                    logZohoActivity('attachment_upload', 'success', "Deal ID: $deal_id, File: $name");
                } else {
                    // Log failed attachment upload
                    logZohoActivity('attachment_upload', 'failed', "Deal ID: $deal_id, File: $name");
                }
            } else {
                $log_entry = "[$timestamp] ⚠️ Skipping $name: file not found or invalid path ($file_path)\n";
                file_put_contents($log_file, $log_entry, FILE_APPEND);
                error_log($log_entry);
                // Log skipped attachment
                logZohoActivity('attachment_upload', 'skipped', "Deal ID: $deal_id, File: $name, Reason: file not found");
            }
        }
        $log_entry = "[$timestamp] 📊 Attachment upload summary: $upload_success_count/" . count($attachments) . " files uploaded successfully for deal $deal_id\n";
        file_put_contents($log_file, $log_entry, FILE_APPEND);
        error_log($log_entry);
        return true;
    } else {
        // Log failed deal creation
        logZohoActivity('deal_create', 'failed', "Buyer: $buyer_name");
        return false;
    }
}


// Removed: uploadZohoAttachment function - now using shared function from zoho_functions.php


// Function to send email notification for sale
function sendSaleEmail($buyer_name, $buyer_phone, $buyer_email, $plot_number, $estate_name, $amount, $agent_name, $payment_plan, $deposit_timing, $deposit_doc, $id_doc, $kra_doc, $passport_photo) {
    $mail = new PHPMailer\PHPMailer\PHPMailer();
    $mail->isSMTP();
    $mail->Host = 'smtp.zoho.com';
    $mail->SMTPAuth = true;
    $mail->Username = 'notifications@proproperty.co.ke';
    $mail->Password = 'ay.r8iVc';
    $mail->SMTPSecure = PHPMailer\PHPMailer\PHPMailer::ENCRYPTION_SMTPS;
    $mail->Port = 465;

    $mail->setFrom('notifications@proproperty.co.ke', 'Marketing Portal');
    #$mail->addAddress('sales@proproperty.co.ke');
    #$mail->addAddress('jomach933@gmail.com');
    $mail->addAddress('jomach690@gmail.com');

    $mail->isHTML(false);
    $mail->Subject = 'New plot sold (' . $estate_name . ') (' . $plot_number . ')';
    $timing_text = ($deposit_timing === 'after')
        ? "Deposit Timing: Paying deposit after signing sale agreement\n"
        : "Deposit Timing: Paying deposit first\n";
    $amount_line = ($deposit_timing === 'after' || $amount === '' || $amount == 0) ? '' : "Amount: Ksh $amount\n";
    $mail->Body = "Hello Susan, see below new plot sold details for your action\n\n" .
                  "Buyer Name: $buyer_name\n" .
                  "Buyer Phone: $buyer_phone\n" .
                  "Buyer Email: $buyer_email\n" .
                  "Plot Number: $plot_number\n" .
                  "Estate: $estate_name\n" .
                  $amount_line .
                  "Payment Plan: $payment_plan\n" .
                  "Agent: $agent_name\n\n" .
                  $timing_text .
                  "Attachments: Please find the uploaded documents attached to this email.";

    // Add attachments
    $attachments = [
        'Deposit Reference' => $deposit_doc,
        'ID Photo' => $id_doc,
        'KRA' => $kra_doc,
        'Passport Photo' => $passport_photo
    ];

    foreach ($attachments as $name => $path) {
        if ($path && file_exists($path)) {
            $mail->addAttachment($path, $name . '.' . pathinfo($path, PATHINFO_EXTENSION));
        }
    }

    if ($mail->send()) {
        error_log("Sale email sent successfully for $buyer_name");
        return true;
    } else {
        error_log("Failed to send sale email for $buyer_name: " . $mail->ErrorInfo);
        return false;
    }
}

if (!isset($_SESSION['user_id']) || !in_array($_SESSION['role'], ['agent', 'admin'])) {
  header("Location: index.php");
  exit;
}

$plots = [];
$estate_id = isset($_GET['estate_id']) ? intval($_GET['estate_id']) : 0;

if (isset($_POST['selected_plots']) && is_array($_POST['selected_plots']) && !empty($_POST['selected_plots'])) {
  // Bulk sell
  $selected_plots = array_map('intval', $_POST['selected_plots']);
  $placeholders = str_repeat('?,', count($selected_plots) - 1) . '?';
  $sql = "SELECT * FROM prop_plots WHERE id IN ($placeholders)";
  $stmt = $conn->prepare($sql);
  $stmt->bind_param(str_repeat('i', count($selected_plots)), ...$selected_plots);
  $stmt->execute();
  $result = $stmt->get_result();
  while ($row = $result->fetch_assoc()) {
    $plots[] = $row;
  }
  $estate_ids = array_unique(array_column($plots, 'estate_id'));
  if (count($estate_ids) !== 1) {
    echo "All plots must be from the same estate.";
    exit;
  }
  $estate_id = $estate_ids[0];
} elseif (isset($_GET['selected_plots']) && is_array($_GET['selected_plots']) && !empty($_GET['selected_plots'])) {
  // Bulk sell via GET
  $selected_plots = array_map('intval', $_GET['selected_plots']);
  $placeholders = str_repeat('?,', count($selected_plots) - 1) . '?';
  $sql = "SELECT * FROM prop_plots WHERE id IN ($placeholders)";
  $stmt = $conn->prepare($sql);
  $stmt->bind_param(str_repeat('i', count($selected_plots)), ...$selected_plots);
  $stmt->execute();
  $result = $stmt->get_result();
  while ($row = $result->fetch_assoc()) {
    $plots[] = $row;
  }
  $estate_ids = array_unique(array_column($plots, 'estate_id'));
  if (count($estate_ids) !== 1) {
    echo "All plots must be from the same estate.";
    exit;
  }
  $estate_id = $estate_ids[0];
} elseif (isset($_GET['plot_ids'])) {
  // Bulk sell via GET
  $plot_ids = explode(',', $_GET['plot_ids']);
  $selected_plots = array_map('intval', $plot_ids);
  $placeholders = str_repeat('?,', count($selected_plots) - 1) . '?';
  $sql = "SELECT * FROM prop_plots WHERE id IN ($placeholders)";
  $stmt = $conn->prepare($sql);
  $stmt->bind_param(str_repeat('i', count($selected_plots)), ...$selected_plots);
  $stmt->execute();
  $result = $stmt->get_result();
  while ($row = $result->fetch_assoc()) {
    $plots[] = $row;
  }
  $estate_ids = array_unique(array_column($plots, 'estate_id'));
  if (count($estate_ids) !== 1) {
    echo "All plots must be from the same estate.";
    exit;
  }
  $estate_id = $estate_ids[0];
} elseif (isset($_GET['plot_id'])) {
   // Single sell
   $plot_id = intval($_GET['plot_id']);
   $plot = $conn->query("SELECT * FROM prop_plots WHERE id = $plot_id AND status IN ('available', 'booked')")->fetch_assoc();
   if (!$plot) {
     echo "Plot not found or not available for sale.";
     exit;
   }
   $plots[] = $plot;
   $estate_id = $plot['estate_id'];

   // Check for existing booking to prefill form
   $existing_booking = $conn->query("SELECT buyer_name, buyer_phone, buyer_email FROM prop_bookings WHERE plot_id = $plot_id AND status = 'active'")->fetch_assoc();
} else {
   echo "Invalid request.";
   exit;
}

// Check if deposit_timing is provided via GET parameter (from booking selection)
$preset_deposit_timing = isset($_GET['deposit_timing']) ? $_GET['deposit_timing'] : '';

if ($_SERVER['REQUEST_METHOD'] === 'POST') {
   header('Content-Type: application/json');
   error_log("POST data: " . print_r($_POST, true));
   error_log("FILES data: " . print_r($_FILES, true));
   try {
    $buyer_name = isset($_POST['buyer_name']) ? $_POST['buyer_name'] : '';
    $buyer_phone = isset($_POST['buyer_phone']) ? $_POST['buyer_phone'] : '';
    $buyer_email = isset($_POST['buyer_email']) ? $_POST['buyer_email'] : '';
    $amount = isset($_POST['amount']) ? $_POST['amount'] : '';
    $payment_plan = isset($_POST['payment_plan']) ? $_POST['payment_plan'] : '';
    $deposit_timing = isset($_POST['deposit_timing']) ? $_POST['deposit_timing'] : 'before';

  // Validate required fields
  if (trim($buyer_name) === '' || trim($buyer_phone) === '' || trim($buyer_email) === '' || trim($payment_plan) === '' || ($deposit_timing !== 'after' && trim($amount) === '')) {
    $error = "All buyer information fields must be filled out.";
  } else {
    if ($deposit_timing === 'after') {
      $amount = '0';
    }

  // Handle file uploads
  $upload_dir = 'uploads/';
  if (!is_dir($upload_dir)) {
    mkdir($upload_dir, 0755, true);
  }

  $uploaded_files = [];
  $file_fields = ['deposit_doc', 'id_doc', 'kra_doc', 'passport_photo'];
  foreach ($file_fields as $field) {
    if (isset($_FILES[$field])) {
      $files = $_FILES[$field];
      if (is_array($files['name'])) {
        // Multiple files
        for ($i = 0; $i < count($files['name']); $i++) {
          if ($files['error'][$i] == 0) {
            $file_name = uniqid() . '_' . basename($files['name'][$i]);
            $file_path = $upload_dir . $file_name;
            if (move_uploaded_file($files['tmp_name'][$i], $file_path)) {
              $uploaded_files[$field][] = $file_path;
            }
          }
        }
      } else {
        // Single file
        if ($files['error'] == 0) {
          $file_name = uniqid() . '_' . basename($files['name']);
          $file_path = $upload_dir . $file_name;
          if (move_uploaded_file($files['tmp_name'], $file_path)) {
            $uploaded_files[$field][] = $file_path;
          }
        }
      }
    }
  }

  // Determine selling agent name (use logged-in user name)
  $saleAgentName = $_SESSION['user_name'] ?? 'Unknown';


  // For all agent bookings via sell_plot.php, treat as bookings
  $notes = "Amount: $amount, Payment Plan: $payment_plan, Deposit Timing: $deposit_timing";

  foreach ($plots as $plot) {
    $existing_booking = $conn->query("SELECT id FROM prop_bookings WHERE plot_id = {$plot['id']} AND status = 'active'")->fetch_assoc();

    if ($existing_booking) {
      // Update existing booking
      $stmt = $conn->prepare("UPDATE prop_bookings SET buyer_name = ?, buyer_phone = ?, buyer_email = ?, notes = ? WHERE id = ?");
      $stmt->bind_param("ssssi", $buyer_name, $buyer_phone, $buyer_email, $notes, $existing_booking['id']);
    } else {
      // Insert new booking
      $stmt = $conn->prepare("INSERT INTO prop_bookings (plot_id, estate_id, agent_name, buyer_name, buyer_phone, buyer_email, notes, status) VALUES (?, ?, ?, ?, ?, ?, ?, 'active')");
      $stmt->bind_param("iisssss", $plot['id'], $estate_id, $saleAgentName, $buyer_name, $buyer_phone, $buyer_email, $notes);
    }
    $stmt->execute();

    // Mark plot as booked
    $conn->query("UPDATE prop_plots SET status = 'booked' WHERE id = {$plot['id']}");

    // Log plot booking activity
    logDataModification('prop_plots', 'UPDATE', $plot['id'], ['status' => 'booked']);
    $action = $existing_booking ? 'UPDATE' : 'INSERT';
    logDataModification('prop_bookings', $action, $existing_booking ? $existing_booking['id'] : 'new', [
      'buyer_name' => $buyer_name,
      'buyer_phone' => $buyer_phone,
      'buyer_email' => $buyer_email,
      'plot_id' => $plot['id'],
      'notes' => $notes,
      'agent_id' => $_SESSION['user_id']
    ]);
  }

  // Log file uploads
  foreach ($uploaded_files as $field => $files) {
    foreach ($files as $file) {
      logFileUpload(basename($file), $field, filesize($file));
    }
  }

  // Define uploaded files for Zoho
  $deposit_doc = $uploaded_files['deposit_doc'][0] ?? '';
  $id_doc = $uploaded_files['id_doc'][0] ?? '';
  $kra_doc = $uploaded_files['kra_doc'][0] ?? '';
  $passport_photo = $uploaded_files['passport_photo'][0] ?? '';

  // Send data to Zoho CRM and email for each plot
  $estate_name = $conn->query("SELECT name FROM prop_estates WHERE id = $estate_id")->fetch_assoc()['name'];
  foreach ($plots as $plot) {
    // Include plot details in deal description for tracking multiple purchases
    $deal_description = "Plot: {$plot['plot_number']}, Estate: $estate_name, Agent: $saleAgentName, Payment Plan: $payment_plan" . ($deposit_timing === 'after' ? ", Deposit Timing: Paying deposit after signing sale agreement" : ", Deposit Timing: Paying deposit first");
    if (!sendToZohoCRM($buyer_name, $buyer_phone, $buyer_email, $plot['plot_number'], $estate_name, $amount, $saleAgentName, $payment_plan, $deposit_doc, $id_doc, $kra_doc, $passport_photo, $deal_description)) {
      error_log("Failed to send data to Zoho CRM for plot {$plot['plot_number']} - check zoho_upload_log.txt for details");
    }
    if (!sendSaleEmail($buyer_name, $buyer_phone, $buyer_email, $plot['plot_number'], $estate_name, $amount, $saleAgentName, $payment_plan, $deposit_timing, $deposit_doc, $id_doc, $kra_doc, $passport_photo)) {
      error_log("Failed to send sale email for plot {$plot['plot_number']}");
    }
  }

  // Return success JSON
  $redirect_url = ($_SESSION['role'] == 'admin') ? 'admin_booked_plots.php' : 'agent_bookings.php';
  echo json_encode(['success' => true, 'redirect_url' => $redirect_url]);
  exit;
  }
  } catch (Exception $e) {
    echo json_encode(['success' => false, 'message' => $e->getMessage()]);
    exit;
  }
}

$page_title = count($plots) > 1 ? 'Sell Multiple Plots' : 'Sell Plot ' . htmlspecialchars($plots[0]['plot_number']);

ob_start();
?>
  <div class="top-bar">
    <h1><?php echo count($plots) > 1 ? 'Sell Multiple Plots' : 'Sell Plot ' . htmlspecialchars($plots[0]['plot_number']); ?></h1>
  </div>

  <div class="card" style="max-width: 640px;">
    <?php if (isset($error)): ?>
      <p style="color: red;"><?php echo htmlspecialchars($error); ?></p>
    <?php endif; ?>
    <p data-estate-id="<?php echo htmlspecialchars($estate_id); ?>"><strong>Estate ID:</strong> <?php echo htmlspecialchars($estate_id); ?></p>
    <?php if (count($plots) > 1): ?>
      <p><strong>Plots to sell:</strong></p>
      <ul>
        <?php foreach ($plots as $plot): ?>
          <li><?php echo htmlspecialchars($plot['plot_number']); ?></li>
        <?php endforeach; ?>
      </ul>
    <?php endif; ?>

    <!-- Deposit Preference Modal -->
    <div id="preferenceModal" class="modal" style="display: none;">
      <div class="modal-content">
        <h3>Select Deposit Option</h3>
        <p>Please choose how the client will pay the deposit.</p>
        <select id="deposit_timing_select">
          <option value="">Select option</option>
          <option value="before">Paying deposit first</option>
          <option value="after">Paying deposit after signing sale agreement</option>
        </select>
        <div style="margin-top: 15px; display:flex; gap:10px; justify-content:center;">
          <button type="button" onclick="confirmDepositTiming()">Continue</button>
          <button type="button" class="secondary" onclick="cancelDepositTiming()">Cancel</button>
        </div>
      </div>
    </div>

    <!-- Loading Modal -->
    <div id="loadingModal" class="modal" style="display: none;">
      <div class="modal-content">
        <div class="loading-spinner"></div>
        <h3>Processing Sale...</h3>
        <p>Please wait while we process the plot sale and upload documents to Zoho CRM.</p>
        <div id="loadingStatus">Initializing...</div>
      </div>
    </div>

    <!-- Success Modal -->
    <div id="successModal" class="modal" style="display: none;">
      <div class="modal-content success">
        <div class="success-icon">✓</div>
        <h3>Sale Completed Successfully!</h3>
        <p>The plot has been sold and all documents have been uploaded to Zoho CRM.</p>
        <button onclick="closeSuccessModal()">Continue</button>
      </div>
    </div>

    <!-- Error Modal -->
    <div id="errorModal" class="modal" style="display: none;">
      <div class="modal-content error">
        <div class="error-icon">✕</div>
        <h3>Sale Failed</h3>
        <p id="errorMessage">An error occurred while processing the sale. Please try again.</p>
        <button onclick="closeErrorModal()">Close</button>
      </div>
    </div>

    <form method="POST" enctype="multipart/form-data" id="sellPlotForm">
      <?php foreach ($plots as $plot): ?>
        <input type="hidden" name="selected_plots[]" value="<?php echo $plot['id']; ?>">
      <?php endforeach; ?>
      <input type="hidden" id="deposit_timing" name="deposit_timing" value="<?php echo htmlspecialchars($preset_deposit_timing); ?>">
      <label for="buyer_name">Buyer Name</label>
      <input type="text" id="buyer_name" name="buyer_name" required value="<?php echo htmlspecialchars($existing_booking['buyer_name'] ?? ''); ?>">

      <label for="buyer_phone">Buyer Phone</label>
      <input type="text" id="buyer_phone" name="buyer_phone" required value="<?php echo htmlspecialchars($existing_booking['buyer_phone'] ?? ''); ?>">

      <label for="buyer_email">Buyer Email</label>
      <input type="email" id="buyer_email" name="buyer_email" required value="<?php echo htmlspecialchars($existing_booking['buyer_email'] ?? ''); ?>">

      <div id="deposit_amount_group">
        <label for="amount">Deposit (Ksh)</label>
        <input type="number" id="amount" name="amount" value="">
      </div>

      <label for="payment_plan">Payment Plan</label>
      <select id="payment_plan" name="payment_plan" required>
        <option value="">Select Payment Plan</option>
        <option value="3 months">3 Months</option>
        <option value="6 months">6 Months</option>
        <option value="12 months">12 Months</option>
      </select>

      <div id="deposit_doc_group">
        <label for="deposit_doc">Deposit Reference (multiple files allowed)</label>
        <input type="file" id="deposit_doc" name="deposit_doc[]" accept=".pdf,.jpg,.jpeg,.png" multiple>
      </div>

      <label for="id_doc">ID Photo (multiple files allowed)</label>
      <input type="file" id="id_doc" name="id_doc[]" accept=".pdf,.jpg,.jpeg,.png" multiple required>

      <label for="kra_doc">KRA (multiple files allowed)</label>
      <input type="file" id="kra_doc" name="kra_doc[]" accept=".pdf,.jpg,.jpeg,.png" multiple required>

      <label for="passport_photo">Passport Photo (multiple files allowed)</label>
      <input type="file" id="passport_photo" name="passport_photo[]" accept=".jpg,.jpeg,.png" multiple required>

      <button type="button" id="sellButton" onclick="handleFormSubmit(event)">Confirm Sale</button>
    </form>
  </div>
<?php
$page_content = ob_get_clean();
include 'layout.php';
?>

<script>
function handleFormSubmit(event) {
    event.preventDefault();

    // Manual validation
    const buyerName = document.getElementById('buyer_name').value.trim();
    const buyerPhone = document.getElementById('buyer_phone').value.trim();
    const buyerEmail = document.getElementById('buyer_email').value.trim();
    const amount = document.getElementById('amount').value.trim();
    const paymentPlan = document.getElementById('payment_plan').value;
    const depositDoc = document.getElementById('deposit_doc').files;
    const depositTiming = document.getElementById('deposit_timing').value || '';
    const idDoc = document.getElementById('id_doc').files;
    const kraDoc = document.getElementById('kra_doc').files;
    const passportPhoto = document.getElementById('passport_photo').files;

    if (buyerName === '') {
        alert('Buyer name is required.');
        return false;
    }
    if (buyerPhone === '') {
        alert('Buyer phone is required.');
        return false;
    }
    if (buyerEmail === '') {
        alert('Buyer email is required.');
        return false;
    }
    if (depositTiming !== 'after' && amount === '') {
        alert('Deposit amount is required.');
        return false;
    }
    if (paymentPlan === '') {
        alert('Payment plan is required.');
        return false;
    }
    if (depositTiming !== 'after' && depositDoc.length === 0) {
        alert('Deposit reference document is required.');
        return false;
    }
    if (idDoc.length === 0) {
        alert('ID photo is required.');
        return false;
    }
    if (kraDoc.length === 0) {
        alert('KRA document is required.');
        return false;
    }
    if (passportPhoto.length === 0) {
        alert('Passport photo is required.');
        return false;
    }

    // Ensure deposit timing selected
    if (!depositTiming) {
        document.getElementById('preferenceModal').style.display = 'flex';
        return false;
    }

    // Show confirmation dialog
    if (!confirm('Are you sure you want to sell this plot?')) {
        return false;
    }

    // Show loading modal
    showLoadingModal();

    // Validate form
    const form = document.getElementById('sellPlotForm');
    if (!form.checkValidity()) {
        alert('Please fill all required fields.');
        return false;
    }

    // Disable form
    const submitButton = document.getElementById('sellButton');
    form.style.pointerEvents = 'none';
    submitButton.disabled = true;
    submitButton.textContent = 'Processing...';

    // Submit form via AJAX
    const formData = new FormData();

    // Append text fields
    formData.append('buyer_name', document.getElementById('buyer_name').value);
    formData.append('buyer_phone', document.getElementById('buyer_phone').value);
    formData.append('buyer_email', document.getElementById('buyer_email').value);
    formData.append('amount', document.getElementById('amount').value);
    formData.append('payment_plan', document.getElementById('payment_plan').value);
    formData.append('deposit_timing', depositTiming);

    // Append selected_plots
    const selectedPlots = document.querySelectorAll('input[name="selected_plots[]"]');
    selectedPlots.forEach(input => {
      formData.append('selected_plots[]', input.value);
    });

    // Append files
    const fileInputs = ['deposit_doc', 'id_doc', 'kra_doc', 'passport_photo'];
    fileInputs.forEach(name => {
      const input = document.getElementById(name);
      for (let i = 0; i < input.files.length; i++) {
        formData.append(name + '[]', input.files[i]);
      }
    });

    fetch(window.location.href, {
        method: 'POST',
        body: formData
    })
    .then(response => {
        const contentType = response.headers.get('content-type');
        if (contentType && contentType.includes('application/json')) {
            return response.json();
        } else {
            return response.text().then(text => {
                // If it's HTML, treat as error
                if (text.includes('<html>') || text.includes('<!DOCTYPE')) {
                    throw new Error('Server error: Please check your connection and try again.');
                }
                // Otherwise, try to parse as JSON anyway
                try {
                    return JSON.parse(text);
                } catch {
                    throw new Error(text || 'Unknown server response');
                }
            });
        }
    })
    .then(data => {
        if (data.success) {
            hideLoadingModal();
            showSuccessModal();
            setTimeout(() => {
                window.location.href = data.redirect_url;
            }, 2000);
        } else {
            throw new Error(data.message || 'Unknown error occurred');
        }
    })
    .catch(error => {
        console.error('Error:', error);
        hideLoadingModal();
        showErrorModal(error.message || 'An error occurred while processing the sale. Please try again.');
        // Re-enable form
        form.style.pointerEvents = 'auto';
        submitButton.disabled = false;
        submitButton.textContent = 'Confirm Sale';
    });

    return false;
}

function showLoadingModal() {
    document.getElementById('loadingModal').style.display = 'flex';
    updateLoadingStatus('Initializing sale process...');
}

function hideLoadingModal() {
    document.getElementById('loadingModal').style.display = 'none';
}

function showSuccessModal() {
    document.getElementById('successModal').style.display = 'flex';
}

function closeSuccessModal() {
    document.getElementById('successModal').style.display = 'none';
}

function showErrorModal(message) {
    document.getElementById('errorMessage').textContent = message;
    document.getElementById('errorModal').style.display = 'flex';
}

function closeErrorModal() {
    document.getElementById('errorModal').style.display = 'none';
}

function updateLoadingStatus(status) {
    document.getElementById('loadingStatus').textContent = status;
}

function applyDepositTimingToForm(timing) {
    const showDeposit = (timing !== 'after');
    const amountEl = document.getElementById('amount');
    const depositDocEl = document.getElementById('deposit_doc');

    const amountGroup = document.getElementById('deposit_amount_group');
    const docGroup = document.getElementById('deposit_doc_group');

    if (amountGroup) amountGroup.style.display = showDeposit ? '' : 'none';
    if (docGroup) docGroup.style.display = showDeposit ? '' : 'none';

    if (amountEl) amountEl.required = showDeposit;
    if (depositDocEl) depositDocEl.required = showDeposit;

    if (!showDeposit) {
        if (amountEl) amountEl.value = '';
        if (depositDocEl) depositDocEl.value = '';
    }
}

function confirmDepositTiming() {
    const selected = document.getElementById('deposit_timing_select').value;
    if (!selected) {
        alert('Please select a deposit option to proceed.');
        return;
    }
    document.getElementById('deposit_timing').value = selected;
    applyDepositTimingToForm(selected);
    document.getElementById('preferenceModal').style.display = 'none';
}

function cancelDepositTiming() {
    // Reset selection
    const select = document.getElementById('deposit_timing_select');
    if (select) select.value = '';
    document.getElementById('deposit_timing').value = '';

    // Deterministic redirect to estates page for the current estate
    var estateEl = document.querySelector('[data-estate-id]');
    var estateId = estateEl ? estateEl.getAttribute('data-estate-id') : '';
    if (estateId) {
        window.location.href = 'plots.php?estate_id=' + encodeURIComponent(estateId);
    } else {
        window.location.href = 'estates.php';
    }
}

document.addEventListener('DOMContentLoaded', function() {
    const pref = document.getElementById('deposit_timing').value;
    if (!pref) {
        document.getElementById('preferenceModal').style.display = 'flex';
    } else {
        applyDepositTimingToForm(pref);
    }
});

// Prevent form submission on Enter key
document.getElementById('sellPlotForm').addEventListener('keydown', function(event) {
    if (event.key === 'Enter') {
        event.preventDefault();
    }
});
</script>

<style>
.modal {
    position: fixed;
    top: 0;
    left: 0;
    width: 100%;
    height: 100%;
    background-color: rgba(0, 0, 0, 0.5);
    display: flex;
    justify-content: center;
    align-items: center;
    z-index: 1000;
}

.modal-content {
    background: white;
    padding: 30px;
    border-radius: 10px;
    text-align: center;
    max-width: 400px;
    width: 90%;
    box-shadow: 0 4px 20px rgba(0, 0, 0, 0.3);
}

.loading-spinner {
    border: 4px solid #f3f3f3;
    border-top: 4px solid #007bff;
    border-radius: 50%;
    width: 40px;
    height: 40px;
    animation: spin 1s linear infinite;
    margin: 0 auto 20px;
}

@keyframes spin {
    0% { transform: rotate(0deg); }
    100% { transform: rotate(360deg); }
}

.success-icon, .error-icon {
    font-size: 48px;
    margin-bottom: 15px;
}

.success-icon {
    color: #28a745;
}

.error-icon {
    color: #dc3545;
}

.modal-content.success .success-icon,
.modal-content.error .error-icon {
    display: block;
}

.modal-content h3 {
    margin-bottom: 15px;
    color: #333;
}

.modal-content p {
    margin-bottom: 20px;
    color: #666;
}

.modal-content button {
    background: #007bff;
    color: white;
    border: none;
    padding: 10px 20px;
    border-radius: 5px;
    cursor: pointer;
    font-size: 16px;
}

.modal-content button:hover {
    background: #0056b3;
}

.modal-content button.secondary {
    background: #6c757d;
}

.modal-content button.secondary:hover {
    background: #5a6268;
}

.modal-content.error button {
    background: #dc3545;
}

.modal-content.error button:hover {
    background: #c82333;
}

#loadingStatus {
    color: #007bff;
    font-weight: bold;
    margin-top: 10px;
}
</style>
