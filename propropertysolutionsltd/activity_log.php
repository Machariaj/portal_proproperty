<?php
// Activity Logging System
date_default_timezone_set('Africa/Nairobi'); // Set timezone to East Africa Time (EAT)

/**
 * Log user activity
 * @param string $action The action performed
 * @param string $details Additional details about the action
 * @param int|null $user_id User ID (optional, will use session if not provided)
 * @param string|null $user_name User name (optional, will use session if not provided)
 * @param string|null $role User role (optional, will use session if not provided)
 */
function logActivity($action, $details = '', $user_id = null, $user_name = null, $role = null) {
    $log_file = 'activity_log.txt';
    $timestamp = date('Y-m-d H:i:s');

    // Use session data if not provided
    if ($user_id === null && isset($_SESSION['user_id'])) {
        $user_id = $_SESSION['user_id'];
    }
    if ($user_name === null && isset($_SESSION['user_name'])) {
        $user_name = $_SESSION['user_name'];
    }
    if ($role === null && isset($_SESSION['role'])) {
        $role = $_SESSION['role'];
    }

    // Get IP address
    $ip_address = $_SERVER['REMOTE_ADDR'] ?? 'unknown';

    // Get user agent
    $user_agent = $_SERVER['HTTP_USER_AGENT'] ?? 'unknown';

    // Format log entry
    $log_entry = sprintf(
        "[%s] USER_ID:%s | NAME:%s | ROLE:%s | IP:%s | ACTION:%s | DETAILS:%s | UA:%s\n",
        $timestamp,
        $user_id ?: 'guest',
        $user_name ?: 'unknown',
        $role ?: 'guest',
        $ip_address,
        $action,
        $details,
        substr($user_agent, 0, 100) // Truncate user agent for readability
    );

    // Append to log file
    file_put_contents($log_file, $log_entry, FILE_APPEND);

    // Also log to PHP error log for immediate visibility
    error_log("Activity: " . trim($log_entry));
}

/**
 * Log login attempt
 * @param string $email Email used for login
 * @param bool $success Whether login was successful
 */
function logLoginAttempt($email, $success = false) {
    $action = $success ? 'LOGIN_SUCCESS' : 'LOGIN_FAILED';
    $details = "Email: $email";
    logActivity($action, $details);
}

/**
 * Log page access
 * @param string $page Page being accessed
 */
function logPageAccess($page) {
    logActivity('PAGE_ACCESS', "Page: $page");
}

/**
 * Log data modification
 * @param string $table Database table
 * @param string $operation Operation (INSERT, UPDATE, DELETE)
 * @param string $record_id Record ID affected
 * @param array $data Additional data
 */
function logDataModification($table, $operation, $record_id, $data = []) {
    $details = sprintf("Table: %s | Operation: %s | RecordID: %s | Data: %s",
        $table, $operation, $record_id, json_encode($data));
    logActivity('DATA_MODIFICATION', $details);
}

/**
 * Log file upload
 * @param string $filename Filename uploaded
 * @param string $type Type of file (deposit, id, kra, passport, etc.)
 * @param int $size File size in bytes
 */
function logFileUpload($filename, $type, $size) {
    $details = sprintf("Filename: %s | Type: %s | Size: %d bytes", $filename, $type, $size);
    logActivity('FILE_UPLOAD', $details);
}

/**
 * Log Zoho CRM integration
 * @param string $action Zoho action (contact_create, deal_create, attachment_upload)
 * @param string $status Status (success, failed)
 * @param string $details Additional details
 */
function logZohoActivity($action, $status, $details = '') {
    logActivity('ZOHO_INTEGRATION', "Action: $action | Status: $status | Details: $details");
}

/**
 * Get recent activity logs
 * @param int $limit Number of recent entries to return
 * @return array Array of log entries
 */
function getRecentActivityLogs($limit = 100) {
    $log_file = 'activity_log.txt';
    if (!file_exists($log_file)) {
        return [];
    }

    $logs = file($log_file, FILE_IGNORE_NEW_LINES | FILE_SKIP_EMPTY_LINES);
    return array_slice(array_reverse($logs), 0, $limit);
}

/**
 * Search activity logs
 * @param string $search_term Term to search for
 * @param int $limit Maximum results to return
 * @return array Array of matching log entries
 */
function searchActivityLogs($search_term, $limit = 50) {
    $log_file = 'activity_log.txt';
    if (!file_exists($log_file)) {
        return [];
    }

    $logs = file($log_file, FILE_IGNORE_NEW_LINES | FILE_SKIP_EMPTY_LINES);
    $results = [];

    foreach ($logs as $log) {
        if (stripos($log, $search_term) !== false) {
            $results[] = $log;
            if (count($results) >= $limit) {
                break;
            }
        }
    }

    return $results;
}
?>
