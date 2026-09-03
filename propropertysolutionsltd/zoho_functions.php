<?php
// Shared Zoho CRM API functions

// Zoho API Configuration
define('ZOHO_CLIENT_ID', '1000.0GHL3CW4SJUE2BJ3SOUQ6KIJB4Q2NN');
define('ZOHO_CLIENT_SECRET', '3c01794c2d6172f0636d041751d7562157d7e7d885');
define('ZOHO_REFRESH_TOKEN', '1000.9071943c18bdca27accca7e39441e9a1.99e3961353cf330e1bd08308021c72a4');
define('ZOHO_TOKEN_URL', 'https://accounts.zoho.com/oauth/v2/token');
define('ZOHO_CRM_API_BASE', 'https://www.zohoapis.com/crm/v8/');

// Server-based application for CREATE operations (current working app)
define('ZOHO_BOOKS_CLIENT_ID', '1000.BLZ4BW6YWVYJIDIZWU9HR8C01DT3VQ');
define('ZOHO_BOOKS_CLIENT_SECRET', 'd7200f642a0afa4dbb37a4259a78913dfb9e95de03');
define('ZOHO_BOOKS_REFRESH_TOKEN', '1000.b668dbb80705026a5f6dcb59eeda2c59.96435801353d226f4b04923c76018ae9');
define('ZOHO_BOOKS_API_BASE', 'https://www.zohoapis.com/books/v3/');
define('ZOHO_BOOKS_ORGANIZATION_ID', '897770663');

// Client-based application for UPDATE operations (add these when you create the new app)
define('ZOHO_BOOKS_UPDATE_CLIENT_ID', 'YOUR_NEW_CLIENT_ID_HERE');
define('ZOHO_BOOKS_UPDATE_CLIENT_SECRET', 'YOUR_NEW_CLIENT_SECRET_HERE');
define('ZOHO_BOOKS_UPDATE_REFRESH_TOKEN', 'YOUR_NEW_REFRESH_TOKEN_HERE');

// Global variables for Books
$zoho_books_access_token = null;
$zoho_books_token_expires = 0;

// Global variable to cache access token
$zoho_access_token = null;
$zoho_token_expires = 0;

/**
 * Get a valid Zoho CRM access token, refreshing if necessary
 * @return string|null Access token or null on failure
 */
function getZohoAccessToken() {
    global $zoho_access_token, $zoho_token_expires;

    // Check if we have a valid cached token
    if ($zoho_access_token && time() < $zoho_token_expires) {
        return $zoho_access_token;
    }

    // Get new token using refresh token
    $postData = [
        'refresh_token' => ZOHO_REFRESH_TOKEN,
        'client_id' => ZOHO_CLIENT_ID,
        'client_secret' => ZOHO_CLIENT_SECRET,
        'grant_type' => 'refresh_token'
    ];

    $ch = curl_init();
    curl_setopt($ch, CURLOPT_URL, ZOHO_TOKEN_URL);
    curl_setopt($ch, CURLOPT_POST, true);
    curl_setopt($ch, CURLOPT_POSTFIELDS, http_build_query($postData));
    curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
    curl_setopt($ch, CURLOPT_HTTPHEADER, ['Content-Type: application/x-www-form-urlencoded']);
    curl_setopt($ch, CURLOPT_SSL_VERIFYPEER, false);
    curl_setopt($ch, CURLOPT_TIMEOUT, 30); // 30 second timeout

    $response = curl_exec($ch);
    $http_code = curl_getinfo($ch, CURLINFO_HTTP_CODE);
    $error = curl_error($ch);
    curl_close($ch);

    $data = json_decode($response, true);

    if ($http_code == 200 && isset($data['access_token'])) {
        $zoho_access_token = $data['access_token'];
        // Set expiration time (subtract 5 minutes for safety margin)
        $zoho_token_expires = time() + ($data['expires_in'] ?? 3600) - 300;

        error_log("Zoho CRM access token refreshed successfully");
        return $zoho_access_token;
    } else {
        error_log("Failed to refresh Zoho CRM access token. HTTP: $http_code, Response: $response, Error: $error");
        return null;
    }
}

/**
 * Get a valid Zoho Books access token for CREATE operations, refreshing if necessary
 * @return string|null Access token or null on failure
 */
function getZohoBooksAccessToken() {
    global $zoho_books_access_token, $zoho_books_token_expires;

    // Check if we have a valid cached token
    if ($zoho_books_access_token && time() < $zoho_books_token_expires) {
        return $zoho_books_access_token;
    }

    // Get new token using refresh token
    $postData = [
        'refresh_token' => ZOHO_BOOKS_REFRESH_TOKEN,
        'client_id' => ZOHO_BOOKS_CLIENT_ID,
        'client_secret' => ZOHO_BOOKS_CLIENT_SECRET,
        'grant_type' => 'refresh_token'
    ];

    $ch = curl_init();
    curl_setopt($ch, CURLOPT_URL, ZOHO_TOKEN_URL);
    curl_setopt($ch, CURLOPT_POST, true);
    curl_setopt($ch, CURLOPT_POSTFIELDS, http_build_query($postData));
    curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
    curl_setopt($ch, CURLOPT_HTTPHEADER, ['Content-Type: application/x-www-form-urlencoded']);
    curl_setopt($ch, CURLOPT_SSL_VERIFYPEER, false);
    curl_setopt($ch, CURLOPT_TIMEOUT, 30); // 30 second timeout

    $response = curl_exec($ch);
    $http_code = curl_getinfo($ch, CURLINFO_HTTP_CODE);
    $error = curl_error($ch);
    curl_close($ch);

    $data = json_decode($response, true);

    if ($http_code == 200 && isset($data['access_token'])) {
        $zoho_books_access_token = $data['access_token'];
        // Set expiration time (subtract 5 minutes for safety margin)
        $zoho_books_token_expires = time() + ($data['expires_in'] ?? 3600) - 300;

        error_log("Zoho Books CREATE access token refreshed successfully");
        return $zoho_books_access_token;
    } else {
        error_log("Failed to refresh Zoho Books CREATE access token. HTTP: $http_code, Response: $response, Error: $error");
        return null;
    }
}

/**
 * Get a valid Zoho Books access token for UPDATE operations, refreshing if necessary
 * @return string|null Access token or null on failure
 */
function getZohoBooksUpdateAccessToken() {
    global $zoho_books_update_access_token, $zoho_books_update_token_expires;

    // Check if we have a valid cached token
    if ($zoho_books_update_access_token && time() < $zoho_books_update_token_expires) {
        return $zoho_books_update_access_token;
    }

    // Get new token using refresh token from UPDATE app
    $postData = [
        'refresh_token' => ZOHO_BOOKS_UPDATE_REFRESH_TOKEN,
        'client_id' => ZOHO_BOOKS_UPDATE_CLIENT_ID,
        'client_secret' => ZOHO_BOOKS_UPDATE_CLIENT_SECRET,
        'grant_type' => 'refresh_token'
    ];

    $ch = curl_init();
    curl_setopt($ch, CURLOPT_URL, ZOHO_TOKEN_URL);
    curl_setopt($ch, CURLOPT_POST, true);
    curl_setopt($ch, CURLOPT_POSTFIELDS, http_build_query($postData));
    curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
    curl_setopt($ch, CURLOPT_HTTPHEADER, ['Content-Type: application/x-www-form-urlencoded']);
    curl_setopt($ch, CURLOPT_SSL_VERIFYPEER, false);
    curl_setopt($ch, CURLOPT_TIMEOUT, 60); // 60 second timeout

    $response = curl_exec($ch);
    $http_code = curl_getinfo($ch, CURLINFO_HTTP_CODE);
    $error = curl_error($ch);
    curl_close($ch);

    $data = json_decode($response, true);

    if ($http_code == 200 && isset($data['access_token'])) {
        $zoho_books_update_access_token = $data['access_token'];
        // Set expiration time (subtract 5 minutes for safety margin)
        $zoho_books_update_token_expires = time() + ($data['expires_in'] ?? 3600) - 300;

        error_log("Zoho Books UPDATE access token refreshed successfully");
        return $zoho_books_update_access_token;
    } else {
        error_log("Failed to refresh Zoho Books UPDATE access token. HTTP: $http_code, Response: $response, Error: $error");
        return null;
    }
}

/**
 * Upload an attachment to a Zoho CRM record
 * @param string $record_id The Zoho record ID
 * @param string $file_path Full path to the file to upload
 * @param string $attachment_name Name for the attachment
 * @param string $module The Zoho module (e.g., 'Deals', 'Contacts')
 * @param int $max_retries Maximum number of retry attempts (default: 3)
 * @return bool True on success, false on failure
 */
function uploadZohoAttachment($record_id, $file_path, $attachment_name, $module = 'Deals', $max_retries = 3) {
    date_default_timezone_set('Africa/Nairobi');

    $log_file = 'zoho_upload_log.txt';
    $timestamp = date('Y-m-d H:i:s');

    $log_entry = "[$timestamp] 🔄 Starting attachment upload for '$attachment_name' to $module record $record_id\n";
    file_put_contents($log_file, $log_entry, FILE_APPEND);
    error_log($log_entry);

    // Validate file
    if (!$file_path || !file_exists($file_path)) {
        $log_entry = "[$timestamp] ⚠️ File path invalid or not found: $file_path\n";
        file_put_contents($log_file, $log_entry, FILE_APPEND);
        error_log($log_entry);
        return false;
    }

    $file_path = realpath($file_path);
    $file_size = filesize($file_path);
    if ($file_size <= 0) {
        $log_entry = "[$timestamp] ⚠️ Empty or unreadable file: $file_path (size: $file_size)\n";
        file_put_contents($log_file, $log_entry, FILE_APPEND);
        error_log($log_entry);
        return false;
    }

    $mime_type = mime_content_type($file_path);
    $log_entry = "[$timestamp] 📁 File details: Path=$file_path, Size=$file_size bytes, MIME=$mime_type\n";
    file_put_contents($log_file, $log_entry, FILE_APPEND);

    $url = ZOHO_CRM_API_BASE . "$module/$record_id/Attachments";
    $log_entry = "[$timestamp] 🌐 Upload URL: $url\n";
    file_put_contents($log_file, $log_entry, FILE_APPEND);

    $attempt = 0;
    while ($attempt < $max_retries) {
        $attempt++;
        $log_entry = "[$timestamp] 🔄 Upload attempt $attempt/$max_retries\n";
        file_put_contents($log_file, $log_entry, FILE_APPEND);

        // Get fresh access token for each attempt
        $access_token = getZohoAccessToken();
        if (!$access_token) {
            $log_entry = "[$timestamp] ❌ Failed to get access token on attempt $attempt\n";
            file_put_contents($log_file, $log_entry, FILE_APPEND);
            if ($attempt < $max_retries) continue;
            return false;
        }

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
        curl_setopt($ch, CURLOPT_TIMEOUT, 60); // 60 second timeout for uploads

        $response = curl_exec($ch);
        $http_code = curl_getinfo($ch, CURLINFO_HTTP_CODE);
        $error = curl_error($ch);
        curl_close($ch);

        $log_entry = "[$timestamp] 📡 Attempt $attempt - HTTP: $http_code, Response: $response, Error: $error\n";
        file_put_contents($log_file, $log_entry, FILE_APPEND);
        error_log("[$timestamp] 📡 Upload attempt $attempt - HTTP: $http_code");

        if (in_array($http_code, [200, 201, 202])) {
            $log_entry = "[$timestamp] ✅ Attachment '$attachment_name' uploaded successfully to $module $record_id (" . basename($file_path) . ")\n";
            file_put_contents($log_file, $log_entry, FILE_APPEND);
            error_log($log_entry);
            return true;
        } elseif ($http_code == 401 && $attempt < $max_retries) {
            // Token might be expired, try refreshing on next attempt
            $log_entry = "[$timestamp] 🔄 Token expired, will retry with fresh token\n";
            file_put_contents($log_file, $log_entry, FILE_APPEND);
            global $zoho_access_token;
            $zoho_access_token = null; // Force token refresh
            continue;
        } elseif ($attempt >= $max_retries) {
            $log_entry = "[$timestamp] ❌ Failed to upload '$attachment_name' to $module $record_id after $max_retries attempts. Final HTTP: $http_code, Response: $response, Error: $error\n";
            file_put_contents($log_file, $log_entry, FILE_APPEND);
            error_log($log_entry);
            return false;
        }

        // Wait before retry (exponential backoff)
        if ($attempt < $max_retries) {
            $wait_time = pow(2, $attempt - 1); // 1s, 2s, 4s...
            $log_entry = "[$timestamp] ⏳ Waiting {$wait_time}s before retry...\n";
            file_put_contents($log_file, $log_entry, FILE_APPEND);
            sleep($wait_time);
        }
    }

    return false;
}

/**
 * Create a deal in Zoho CRM
 * @param array $dealData Deal data array
 * @return string|null Deal ID on success, null on failure
 */
function createZohoDeal($dealData) {
    date_default_timezone_set('Africa/Nairobi');

    $log_file = 'zoho_upload_log.txt';
    $timestamp = date('Y-m-d H:i:s');

    $log_entry = "[$timestamp] 🔄 Starting deal creation in Zoho CRM\n";
    file_put_contents($log_file, $log_entry, FILE_APPEND);
    error_log($log_entry);

    $access_token = getZohoAccessToken();
    if (!$access_token) {
        $log_entry = "[$timestamp] ❌ Failed to get Zoho access token for deal creation\n";
        file_put_contents($log_file, $log_entry, FILE_APPEND);
        error_log($log_entry);
        return null;
    }

    $ch = curl_init();
    curl_setopt($ch, CURLOPT_URL, ZOHO_CRM_API_BASE . 'Deals');
    curl_setopt($ch, CURLOPT_POST, true);
    curl_setopt($ch, CURLOPT_POSTFIELDS, json_encode($dealData));
    curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
    curl_setopt($ch, CURLOPT_HTTPHEADER, [
        'Authorization: Zoho-oauthtoken ' . $access_token,
        'Content-Type: application/json'
    ]);
    curl_setopt($ch, CURLOPT_SSL_VERIFYPEER, false);
    curl_setopt($ch, CURLOPT_CONNECTTIMEOUT, 30);
    curl_setopt($ch, CURLOPT_TIMEOUT, 120);

    $response = curl_exec($ch);
    $http_code = curl_getinfo($ch, CURLINFO_HTTP_CODE);
    $error = curl_error($ch);
    curl_close($ch);

    $log_entry = "[$timestamp] 📡 Deal Creation - HTTP: $http_code, Response: $response, Error: $error\n";
    file_put_contents($log_file, $log_entry, FILE_APPEND);
    error_log("[$timestamp] 📡 Deal Creation - HTTP: $http_code, Response: $response");

    $deal_response_data = json_decode($response, true);
    if (in_array($http_code, [200, 201]) && isset($deal_response_data['data'][0]['details']['id'])) {
        $deal_id = $deal_response_data['data'][0]['details']['id'];
        $log_entry = "[$timestamp] ✅ Deal created in Zoho CRM (ID: $deal_id)\n";
        file_put_contents($log_file, $log_entry, FILE_APPEND);
        error_log($log_entry);
        return $deal_id;
    } else {
        $log_entry = "[$timestamp] ❌ Failed to create deal in Zoho CRM. HTTP: $http_code, Response: $response\n";
        file_put_contents($log_file, $log_entry, FILE_APPEND);
        error_log($log_entry);
        return null;
    }
}

/**
 * Get Zoho Books organization ID
 * @return string|null Organization ID or null on failure
 */
function getZohoBooksOrganizationId() {
    $access_token = getZohoBooksAccessToken();
    if (!$access_token) {
        return null;
    }

    $ch = curl_init();
    curl_setopt($ch, CURLOPT_URL, ZOHO_BOOKS_API_BASE . 'organizations');
    curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
    curl_setopt($ch, CURLOPT_HTTPHEADER, [
        'Authorization: Zoho-oauthtoken ' . $access_token
    ]);
    curl_setopt($ch, CURLOPT_SSL_VERIFYPEER, false);
    curl_setopt($ch, CURLOPT_TIMEOUT, 30);

    $response = curl_exec($ch);
    $http_code = curl_getinfo($ch, CURLINFO_HTTP_CODE);
    $error = curl_error($ch);
    curl_close($ch);

    $data = json_decode($response, true);
    if ($http_code == 200 && isset($data['organizations'][0]['organization_id'])) {
        return $data['organizations'][0]['organization_id'];
    } else {
        error_log("Failed to get Zoho Books organization ID. HTTP: $http_code, Response: $response");
        return null;
    }
}

/**
 * Find a customer in Zoho Books by email
 * @param string $email Customer email to search for
 * @return string|null Customer ID if found, null if not found
 */
function findZohoBooksCustomerByEmail($email) {
    date_default_timezone_set('Africa/Nairobi');

    $log_file = 'zoho_upload_log.txt';
    $timestamp = date('Y-m-d H:i:s');

    $log_entry = "[$timestamp] 🔍 Searching for existing customer by email: $email\n";
    file_put_contents($log_file, $log_entry, FILE_APPEND);
    error_log($log_entry);

    $access_token = getZohoBooksAccessToken();
    if (!$access_token) {
        $log_entry = "[$timestamp] ❌ Failed to get Zoho Books CREATE access token for customer search\n";
        file_put_contents($log_file, $log_entry, FILE_APPEND);
        error_log($log_entry);
        return null;
    }

    $organization_id = ZOHO_BOOKS_ORGANIZATION_ID ?: getZohoBooksOrganizationId();
    if (!$organization_id) {
        $log_entry = "[$timestamp] ❌ Failed to get Zoho Books organization ID\n";
        file_put_contents($log_file, $log_entry, FILE_APPEND);
        error_log($log_entry);
        return null;
    }

    $url = ZOHO_BOOKS_API_BASE . 'contacts?organization_id=' . $organization_id . '&email=' . urlencode($email);
    $ch = curl_init();
    curl_setopt($ch, CURLOPT_URL, $url);
    curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
    curl_setopt($ch, CURLOPT_HTTPHEADER, [
        'Authorization: Zoho-oauthtoken ' . $access_token
    ]);
    curl_setopt($ch, CURLOPT_SSL_VERIFYPEER, false);
    curl_setopt($ch, CURLOPT_TIMEOUT, 30);

    $response = curl_exec($ch);
    $http_code = curl_getinfo($ch, CURLINFO_HTTP_CODE);
    $error = curl_error($ch);
    curl_close($ch);

    $log_entry = "[$timestamp] 📡 Customer Search - HTTP: $http_code, Response: $response, Error: $error\n";
    file_put_contents($log_file, $log_entry, FILE_APPEND);
    error_log("[$timestamp] 📡 Customer Search - HTTP: $http_code");

    if ($http_code == 200) {
        $data = json_decode($response, true);
        if (isset($data['contacts']) && count($data['contacts']) > 0) {
            // Filter contacts by email since the search might return multiple
            foreach ($data['contacts'] as $contact) {
                if (isset($contact['email']) && $contact['email'] === $email) {
                    $customer_id = $contact['contact_id'];
                    $log_entry = "[$timestamp] ✅ Found existing customer (ID: $customer_id)\n";
                    file_put_contents($log_file, $log_entry, FILE_APPEND);
                    error_log($log_entry);
                    return $customer_id;
                }
            }
        }
    } else {
        $log_entry = "[$timestamp] ❌ Customer search failed - HTTP: $http_code, Response: $response\n";
        file_put_contents($log_file, $log_entry, FILE_APPEND);
        error_log($log_entry);
    }

    $log_entry = "[$timestamp] ℹ️ Customer not found, will create new one\n";
    file_put_contents($log_file, $log_entry, FILE_APPEND);
    error_log($log_entry);
    return null;
}



/**
 * Update an existing customer in Zoho Books
 * @param string $customer_id Existing customer ID
 * @param array $customerData Customer data array
 * @param string $notes Remarks/notes for the customer
 * @param array $contactPersons Array of contact persons
 * @return string|null Customer ID on success, null on failure
 */
function updateZohoBooksCustomer($customer_id, $customerData, $notes = '', $contactPersons = []) {
    date_default_timezone_set('Africa/Nairobi');

    $log_file = 'zoho_upload_log.txt';
    $timestamp = date('Y-m-d H:i:s');

    $log_entry = "[$timestamp] 🔄 Updating existing customer in Zoho Books (ID: $customer_id)\n";
    file_put_contents($log_file, $log_entry, FILE_APPEND);
    error_log($log_entry);

    $access_token = getZohoBooksAccessToken();
    if (!$access_token) {
        $log_entry = "[$timestamp] ❌ Failed to get Zoho Books access token for customer update\n";
        file_put_contents($log_file, $log_entry, FILE_APPEND);
        error_log($log_entry);
        return null;
    }

    $organization_id = ZOHO_BOOKS_ORGANIZATION_ID ?: getZohoBooksOrganizationId();
    if (!$organization_id) {
        $log_entry = "[$timestamp] ❌ Failed to get Zoho Books organization ID\n";
        file_put_contents($log_file, $log_entry, FILE_APPEND);
        error_log($log_entry);
        return null;
    }

    // If we have new notes to add, retrieve existing notes and append
    if (!empty($notes)) {
        // Get existing customer details to retrieve current notes
        $existing_url = ZOHO_BOOKS_API_BASE . 'contacts/' . $customer_id . '?organization_id=' . $organization_id;
        $ch_existing = curl_init();
        curl_setopt($ch_existing, CURLOPT_URL, $existing_url);
        curl_setopt($ch_existing, CURLOPT_RETURNTRANSFER, true);
        curl_setopt($ch_existing, CURLOPT_HTTPHEADER, [
            'Authorization: Zoho-oauthtoken ' . $access_token
        ]);
        curl_setopt($ch_existing, CURLOPT_SSL_VERIFYPEER, false);
        curl_setopt($ch_existing, CURLOPT_TIMEOUT, 30);

        $existing_response = curl_exec($ch_existing);
        $existing_http_code = curl_getinfo($ch_existing, CURLINFO_HTTP_CODE);
        curl_close($ch_existing);

        if ($existing_http_code == 200) {
            $existing_data = json_decode($existing_response, true);
            if (isset($existing_data['contact']['notes']) && !empty($existing_data['contact']['notes'])) {
                // Append new notes to existing notes
                $customerData['notes'] = $existing_data['contact']['notes'] . "\n\n" . $notes;
                $log_entry = "[$timestamp] 📝 Appended new notes to existing notes\n";
                file_put_contents($log_file, $log_entry, FILE_APPEND);
                error_log($log_entry);
            } else {
                // No existing notes, just use the new notes
                $customerData['notes'] = $notes;
                $log_entry = "[$timestamp] 📝 No existing notes found, using new notes\n";
                file_put_contents($log_file, $log_entry, FILE_APPEND);
                error_log($log_entry);
            }
        } else {
            // Could not retrieve existing notes, just use new notes
            $customerData['notes'] = $notes;
            $log_entry = "[$timestamp] ⚠️ Could not retrieve existing notes, using new notes only\n";
            file_put_contents($log_file, $log_entry, FILE_APPEND);
            error_log($log_entry);
        }
    }

    if (!empty($contactPersons)) {
        $customerData['contact_persons'] = $contactPersons;
    }

    $url = ZOHO_BOOKS_API_BASE . 'contacts/' . $customer_id . '?organization_id=' . $organization_id;
    $ch = curl_init();
    curl_setopt($ch, CURLOPT_URL, $url);
    curl_setopt($ch, CURLOPT_CUSTOMREQUEST, 'PUT');
    curl_setopt($ch, CURLOPT_POSTFIELDS, json_encode($customerData));
    curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
    curl_setopt($ch, CURLOPT_HTTPHEADER, [
        'Authorization: Zoho-oauthtoken ' . $access_token,
        'Content-Type: application/json'
    ]);
    curl_setopt($ch, CURLOPT_SSL_VERIFYPEER, false);
    curl_setopt($ch, CURLOPT_TIMEOUT, 30);

    $response = curl_exec($ch);
    $http_code = curl_getinfo($ch, CURLINFO_HTTP_CODE);
    $error = curl_error($ch);
    curl_close($ch);

    $log_entry = "[$timestamp] 📡 Customer Update - HTTP: $http_code, Response: $response, Error: $error\n";
    file_put_contents($log_file, $log_entry, FILE_APPEND);
    error_log("[$timestamp] 📡 Customer Update - HTTP: $http_code, Response: $response");

    $customer_response_data = json_decode($response, true);
    if (in_array($http_code, [200, 201]) && isset($customer_response_data['contact']['contact_id'])) {
        $updated_customer_id = $customer_response_data['contact']['contact_id'];
        $log_entry = "[$timestamp] ✅ Customer updated in Zoho Books (ID: $updated_customer_id)\n";
        file_put_contents($log_file, $log_entry, FILE_APPEND);
        error_log($log_entry);
        return $updated_customer_id;
    } else {
        $log_entry = "[$timestamp] ❌ Failed to update customer in Zoho Books. HTTP: $http_code, Response: $response\n";
        file_put_contents($log_file, $log_entry, FILE_APPEND);
        error_log($log_entry);
        return null;
    }
}

/**
 * Create a customer in Zoho Books
 * @param array $customerData Customer data array
 * @param string $notes Remarks/notes for the customer
 * @param array $contactPersons Array of contact persons
 * @return string|null Customer ID on success, null on failure
 */
function createZohoBooksCustomer($customerData, $notes = '', $contactPersons = []) {
    date_default_timezone_set('Africa/Nairobi');

    $log_file = 'zoho_upload_log.txt';
    $timestamp = date('Y-m-d H:i:s');

    $log_entry = "[$timestamp] 🔄 Starting customer creation/update in Zoho Books\n";
    file_put_contents($log_file, $log_entry, FILE_APPEND);
    error_log($log_entry);

    // Check if customer already exists by email
    $existing_customer_id = findZohoBooksCustomerByEmail($customerData['email']);
    if ($existing_customer_id) {
        $log_entry = "[$timestamp] 📝 Customer already exists (ID: $existing_customer_id), attempting to update notes with new plot info\n";
        file_put_contents($log_file, $log_entry, FILE_APPEND);
        error_log($log_entry);

        // Try to update customer with new notes (append to existing)
        $update_result = updateZohoBooksCustomer($existing_customer_id, $customerData, $notes, $contactPersons);
        if ($update_result) {
            $log_entry = "[$timestamp] ✅ Customer notes updated successfully\n";
            file_put_contents($log_file, $log_entry, FILE_APPEND);
            error_log($log_entry);
            return $update_result;
        } else {
            $log_entry = "[$timestamp] ⚠️ Customer update failed, but will use existing customer for this sale\n";
            file_put_contents($log_file, $log_entry, FILE_APPEND);
            error_log($log_entry);
            return $existing_customer_id;
        }
    } else {
        $log_entry = "[$timestamp] ℹ️ No existing customer found, will create new customer\n";
        file_put_contents($log_file, $log_entry, FILE_APPEND);
        error_log($log_entry);
    }

    $access_token = getZohoBooksAccessToken();
    if (!$access_token) {
        $log_entry = "[$timestamp] ❌ Failed to get Zoho Books access token for customer creation\n";
        file_put_contents($log_file, $log_entry, FILE_APPEND);
        error_log($log_entry);
        return null;
    }

    $organization_id = ZOHO_BOOKS_ORGANIZATION_ID ?: getZohoBooksOrganizationId();
    if (!$organization_id) {
        $log_entry = "[$timestamp] ❌ Failed to get Zoho Books organization ID\n";
        file_put_contents($log_file, $log_entry, FILE_APPEND);
        error_log($log_entry);
        return null;
    }

    // Add notes and contact persons to customer data
    if (!empty($notes)) {
        $customerData['notes'] = $notes;
    }
    if (!empty($contactPersons)) {
        $customerData['contact_persons'] = $contactPersons;
    }

    // Log request data
    $log_entry = "[$timestamp] 📤 Customer Creation Request Data: " . json_encode($customerData) . "\n";
    file_put_contents($log_file, $log_entry, FILE_APPEND);
    error_log($log_entry);

    $url = ZOHO_BOOKS_API_BASE . 'contacts?organization_id=' . $organization_id;
    $ch = curl_init();
    curl_setopt($ch, CURLOPT_URL, $url);
    curl_setopt($ch, CURLOPT_POST, true);
    curl_setopt($ch, CURLOPT_POSTFIELDS, json_encode($customerData));
    curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
    curl_setopt($ch, CURLOPT_HTTPHEADER, [
        'Authorization: Zoho-oauthtoken ' . $access_token,
        'Content-Type: application/json'
    ]);
    curl_setopt($ch, CURLOPT_SSL_VERIFYPEER, false);
    curl_setopt($ch, CURLOPT_TIMEOUT, 30);

    $response = curl_exec($ch);
    $http_code = curl_getinfo($ch, CURLINFO_HTTP_CODE);
    $error = curl_error($ch);
    curl_close($ch);

    $log_entry = "[$timestamp] 📡 Customer Creation - HTTP: $http_code, Response: $response, Error: $error\n";
    file_put_contents($log_file, $log_entry, FILE_APPEND);
    error_log("[$timestamp] 📡 Customer Creation - HTTP: $http_code, Response: $response");

    $customer_response_data = json_decode($response, true);
    if (in_array($http_code, [200, 201]) && isset($customer_response_data['contact']['contact_id'])) {
        $customer_id = $customer_response_data['contact']['contact_id'];
        $log_entry = "[$timestamp] ✅ Customer created in Zoho Books (ID: $customer_id)\n";
        file_put_contents($log_file, $log_entry, FILE_APPEND);
        error_log($log_entry);
        return $customer_id;
    } else {
        $log_entry = "[$timestamp] ❌ Failed to create customer in Zoho Books. HTTP: $http_code, Response: $response\n";
        file_put_contents($log_file, $log_entry, FILE_APPEND);
        error_log($log_entry);
        return null;
    }
}
?>
