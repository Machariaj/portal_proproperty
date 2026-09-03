<?php
// Test script to upload an attachment to an existing Zoho CRM contact

// Include the functions from sell_plot.php (without session_start)
include 'db.php'; // Include database connection if needed

// Zoho CRM API Configuration
define('ZOHO_CLIENT_ID', '1000.0GHL3CW4SJUE2BJ3SOUQ6KIJB4Q2NN');
define('ZOHO_CLIENT_SECRET', '3c01794c2d6172f0636d041751d7562157d7e7d885');
define('ZOHO_REFRESH_TOKEN', '1000.7d5170c71932a3a8bd8d226c2d3e0099.1f795d6367529752002cc1e5ef872248');
define('ZOHO_TOKEN_URL', 'https://accounts.zoho.com/oauth/v2/token');
define('ZOHO_API_BASE', 'https://www.zohoapis.com/crm/v8/');

// Function to get access token using authorization code
function getZohoAccessToken() {
    $postData = [
        'code' => ZOHO_REFRESH_TOKEN,
        'client_id' => ZOHO_CLIENT_ID,
        'client_secret' => ZOHO_CLIENT_SECRET,
        'grant_type' => 'authorization_code'
    ];

    $ch = curl_init();
    curl_setopt($ch, CURLOPT_URL, ZOHO_TOKEN_URL);
    curl_setopt($ch, CURLOPT_POST, true);
    curl_setopt($ch, CURLOPT_POSTFIELDS, http_build_query($postData));
    curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
    curl_setopt($ch, CURLOPT_HTTPHEADER, ['Content-Type: application/x-www-form-urlencoded']);
    curl_setopt($ch, CURLOPT_SSL_VERIFYPEER, false);

    $response = curl_exec($ch);
    $http_code = curl_getinfo($ch, CURLINFO_HTTP_CODE);
    $error = curl_error($ch);
    curl_close($ch);

    $data = json_decode($response, true);
    if (isset($data['access_token'])) {
        return $data['access_token'];
    } else {
        echo "Failed to get access token. Response: $response\n";
        return null;
    }
}

// Function to get a contact ID from Zoho CRM
function getZohoContactId($access_token) {
    $url = ZOHO_API_BASE . "Contacts?fields=id&per_page=1";

    $ch = curl_init();
    curl_setopt($ch, CURLOPT_URL, $url);
    curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
    curl_setopt($ch, CURLOPT_HTTPHEADER, [
        'Authorization: Zoho-oauthtoken ' . $access_token
    ]);
    curl_setopt($ch, CURLOPT_SSL_VERIFYPEER, false);

    $response = curl_exec($ch);
    $http_code = curl_getinfo($ch, CURLINFO_HTTP_CODE);
    curl_close($ch);

    if ($http_code == 200) {
        $data = json_decode($response, true);
        if (isset($data['data'][0]['id'])) {
            return $data['data'][0]['id'];
        }
    }

    echo "Failed to get contact ID. Response: $response\n";
    return null;
}

// Function to upload an attachment to Zoho CRM
function uploadZohoAttachment($contact_id, $file_path, $attachment_name, $access_token) {
    // Create a custom log file for Zoho operations
    $log_file = 'zoho_upload_log.txt';
    $timestamp = date('Y-m-d H:i:s');

    $log_entry = "[$timestamp] 🔄 Starting attachment upload for $attachment_name to contact $contact_id\n";
    file_put_contents($log_file, $log_entry, FILE_APPEND);

    if (!$file_path || !file_exists($file_path)) {
        $log_entry = "[$timestamp] ⚠️ File path invalid or not found for $attachment_name: $file_path\n";
        file_put_contents($log_file, $log_entry, FILE_APPEND);
        echo $log_entry;
        return false;
    }

    $file_path = realpath($file_path); // Convert to absolute path
    $file_size = filesize($file_path);
    if ($file_size <= 0) {
        $log_entry = "[$timestamp] ⚠️ Empty or unreadable file for $attachment_name: $file_path (size: $file_size)\n";
        file_put_contents($log_file, $log_entry, FILE_APPEND);
        echo $log_entry;
        return false;
    }

    $mime_type = mime_content_type($file_path);
    $log_entry = "[$timestamp] 📁 File details: Path=$file_path, Size=$file_size bytes, MIME=$mime_type\n";
    file_put_contents($log_file, $log_entry, FILE_APPEND);

    $url = ZOHO_API_BASE . "Contacts/$contact_id/Attachments";
    $log_entry = "[$timestamp] 🌐 Upload URL: $url\n";
    file_put_contents($log_file, $log_entry, FILE_APPEND);

    $ch = curl_init();
    curl_setopt($ch, CURLOPT_URL, $url);
    curl_setopt($ch, CURLOPT_POST, true);
    curl_setopt($ch, CURLOPT_POSTFIELDS, [
        'file' => new CURLFile($file_path, $mime_type, basename($file_path))
    ]);
    curl_setopt($ch, CURLOPT_HTTPHEADER, [
        'Authorization: Zoho-oauthtoken ' . $access_token
    ]);
    curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
    curl_setopt($ch, CURLOPT_SSL_VERIFYPEER, false);
    curl_setopt($ch, CURLOPT_VERBOSE, true); // Enable verbose output for debugging

    // Capture verbose output
    $verbose = fopen('php://temp', 'rw+');
    curl_setopt($ch, CURLOPT_STDERR, $verbose);

    $response = curl_exec($ch);
    $http_code = curl_getinfo($ch, CURLINFO_HTTP_CODE);
    $error = curl_error($ch);
    $info = curl_getinfo($ch);

    // Get verbose output
    rewind($verbose);
    $verbose_log = stream_get_contents($verbose);
    fclose($verbose);

    curl_close($ch);

    $log_entry = "[$timestamp] 📡 Upload response details:\n";
    $log_entry .= "  HTTP Code: $http_code\n";
    $log_entry .= "  Response: $response\n";
    $log_entry .= "  Curl Error: $error\n";
    $log_entry .= "  Curl Info: " . print_r($info, true) . "\n";
    $log_entry .= "  Verbose Output: $verbose_log\n";
    file_put_contents($log_file, $log_entry, FILE_APPEND);

    echo "[$timestamp] 📡 Upload response details: HTTP $http_code, Response: $response\n";

    if (in_array($http_code, [200, 201, 202])) {
        $log_entry = "[$timestamp] ✅ Attachment '$attachment_name' uploaded successfully for contact $contact_id (" . basename($file_path) . ")\n";
        file_put_contents($log_file, $log_entry, FILE_APPEND);
        echo $log_entry;
        return true;
    } else {
        $log_entry = "[$timestamp] ❌ Failed to upload '$attachment_name' for contact $contact_id. HTTP: $http_code, Response: $response, Error: $error\n";
        file_put_contents($log_file, $log_entry, FILE_APPEND);
        echo $log_entry;
        return false;
    }
}

echo "Starting Zoho attachment upload test...\n";

// Get access token
echo "Getting Zoho access token...\n";
$access_token = getZohoAccessToken();
if (!$access_token) {
    echo "❌ Failed to get Zoho access token\n";
    exit;
}

echo "✅ Got access token\n";

// Get a contact ID from Zoho CRM
echo "Getting a contact ID from Zoho CRM...\n";
$test_contact_id = getZohoContactId($access_token);
if (!$test_contact_id) {
    echo "❌ Failed to get contact ID from Zoho CRM\n";
    exit;
}

echo "✅ Got contact ID: $test_contact_id\n";

// Test file path (use an existing file from uploads directory)
$files = glob('uploads/*');
if (empty($files)) {
    echo "❌ No files found in uploads directory\n";
    exit;
}

$test_file_path = $files[0]; // Get first file in uploads

echo "Testing attachment upload to Zoho CRM Contact ID: $test_contact_id\n";
echo "Using test file: $test_file_path\n\n";

if (!file_exists($test_file_path)) {
    echo "❌ Test file not found: $test_file_path\n";
    exit;
}

// Test upload
echo "Starting attachment upload...\n";
$result = uploadZohoAttachment($test_contact_id, $test_file_path, 'Test Attachment', $access_token);

if ($result) {
    echo "✅ Attachment upload test completed successfully\n";
    echo "Check Zoho CRM contact $test_contact_id for the attachment\n";
} else {
    echo "❌ Attachment upload test failed\n";
    echo "Check the zoho_upload_log.txt file and PHP error logs for details\n";
}

echo "\nTest completed.\n";

// Check if log file was created
if (file_exists('zoho_upload_log.txt')) {
    echo "Log file created. Contents:\n";
    echo file_get_contents('zoho_upload_log.txt');
} else {
    echo "Log file was not created. Check file permissions.\n";
}
?>
