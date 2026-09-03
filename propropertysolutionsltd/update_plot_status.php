<?php
error_reporting(E_ALL);
ob_start();
session_start();
include 'activity_log.php'; // Include activity logging

if (!isset($_SESSION['user_id']) || $_SESSION['role'] !== 'admin') {
    ob_clean();
    header("Location: index.php");
    exit;
}

include 'db_connection.php'; // Include database connection

// Ensure status and date_signed columns exist in prop_bookings
try {
  $result = $conn->query("SHOW COLUMNS FROM prop_bookings LIKE 'status'");
  if ($result && $result->num_rows == 0) {
      $conn->query("ALTER TABLE prop_bookings ADD COLUMN status VARCHAR(50) DEFAULT 'active'");
  }
} catch (Exception $e) {
  error_log("Status column check error: " . $e->getMessage());
}

try {
  $result = $conn->query("SHOW COLUMNS FROM prop_bookings LIKE 'date_signed'");
  if ($result && $result->num_rows == 0) {
      $conn->query("ALTER TABLE prop_bookings ADD COLUMN date_signed TIMESTAMP NULL");
  }
} catch (Exception $e) {
  error_log("Date signed column check error: " . $e->getMessage());
}

header('Content-Type: application/json');

if ($_SERVER['REQUEST_METHOD'] === 'POST') {
    $action = $_POST['action'] ?? '';
    $plot_id = intval($_POST['plot_id'] ?? 0);
    $type = $_POST['type'] ?? '';

    if ($plot_id <= 0) {
        echo json_encode(['success' => false, 'message' => 'Invalid plot ID']);
        exit;
    }

    $conn->begin_transaction();

    try {
        if ($action === 'make_available') {
            if ($type === 'sold') {
                $sale_id = intval($_POST['sale_id'] ?? 0);
                if ($sale_id <= 0) {
                    throw new Exception('Invalid sale ID');
                }

                // Delete sale record
                $stmt = $conn->prepare("DELETE FROM prop_sales WHERE id = ?");
                if (!$stmt) {
                    throw new Exception('Prepare failed: ' . $conn->error);
                }
                $stmt->bind_param("i", $sale_id);
                if (!$stmt->execute()) {
                    throw new Exception('Execute failed: ' . $stmt->error);
                }

                // Update plot status to available
                $stmt = $conn->prepare("UPDATE prop_plots SET status = 'available' WHERE id = ?");
                if (!$stmt) {
                    throw new Exception('Prepare failed: ' . $conn->error);
                }
                $stmt->bind_param("i", $plot_id);
                if (!$stmt->execute()) {
                    throw new Exception('Execute failed: ' . $stmt->error);
                }

                // Log admin action
                logDataModification('prop_sales', 'DELETE', $sale_id, ['admin_action' => 'make_available']);
                logDataModification('prop_plots', 'UPDATE', $plot_id, ['status' => 'available', 'admin_action' => 'make_available']);

            } elseif ($type === 'booked' || $type === 'sa_signed') {
                $booking_id = intval($_POST['booking_id'] ?? 0);

                if ($booking_id > 0) {
                    // Delete booking record
                    $stmt = $conn->prepare("DELETE FROM prop_bookings WHERE id = ?");
                    if (!$stmt) {
                        throw new Exception('Prepare failed: ' . $conn->error);
                    }
                    $stmt->bind_param("i", $booking_id);
                    if (!$stmt->execute()) {
                        throw new Exception('Execute failed: ' . $stmt->error);
                    }
                    // Log admin action
                    logDataModification('prop_bookings', 'DELETE', $booking_id, ['admin_action' => 'make_available', 'from_status' => $type]);
                }

                // Update plot status to available
                $stmt = $conn->prepare("UPDATE prop_plots SET status = 'available' WHERE id = ?");
                if (!$stmt) {
                    throw new Exception('Prepare failed: ' . $conn->error);
                }
                $stmt->bind_param("i", $plot_id);
                if (!$stmt->execute()) {
                    throw new Exception('Execute failed: ' . $stmt->error);
                }

                // Log admin action
                logDataModification('prop_plots', 'UPDATE', $plot_id, ['status' => 'available', 'admin_action' => 'make_available', 'from_status' => $type]);
            }

        } elseif ($action === 'make_booked') {
            if ($type === 'sold') {
                $sale_id = intval($_POST['sale_id'] ?? 0);
                if ($sale_id <= 0) {
                    throw new Exception('Invalid sale ID');
                }

                // Get sale details
                $stmt = $conn->prepare("SELECT buyer_name, buyer_phone, buyer_email, agent_name FROM prop_sales WHERE id = ?");
                if (!$stmt) {
                    throw new Exception('Prepare failed: ' . $conn->error);
                }
                $stmt->bind_param("i", $sale_id);
                if (!$stmt->execute()) {
                    throw new Exception('Execute failed: ' . $stmt->error);
                }
                $sale_data = $stmt->get_result()->fetch_assoc();

                if (!$sale_data) {
                    throw new Exception('Sale record not found');
                }

                // Delete sale record
                $stmt = $conn->prepare("DELETE FROM prop_sales WHERE id = ?");
                if (!$stmt) {
                    throw new Exception('Prepare failed: ' . $conn->error);
                }
                $stmt->bind_param("i", $sale_id);
                if (!$stmt->execute()) {
                    throw new Exception('Execute failed: ' . $stmt->error);
                }

                // Insert booking record
                $stmt = $conn->prepare("INSERT INTO prop_bookings (plot_id, buyer_name, buyer_phone, buyer_email, agent_name, date_booked, notes, status) VALUES (?, ?, ?, ?, ?, NOW(), '', 'active')");
                if (!$stmt) {
                    throw new Exception('Prepare failed: ' . $conn->error);
                }
                $stmt->bind_param("isssss", $plot_id, $sale_data['buyer_name'], $sale_data['buyer_phone'], $sale_data['buyer_email'], $sale_data['agent_name']);
                if (!$stmt->execute()) {
                    throw new Exception('Execute failed: ' . $stmt->error);
                }

                // Update plot status to booked
                $stmt = $conn->prepare("UPDATE prop_plots SET status = 'booked' WHERE id = ?");
                if (!$stmt) {
                    throw new Exception('Prepare failed: ' . $conn->error);
                }
                $stmt->bind_param("i", $plot_id);
                if (!$stmt->execute()) {
                    throw new Exception('Execute failed: ' . $stmt->error);
                }
            }

        } elseif ($action === 'sell_plot') {
            if ($type === 'booked') {
                $booking_id = intval($_POST['booking_id'] ?? 0);
                if ($booking_id <= 0) {
                    throw new Exception('Invalid booking ID');
                }

                // Get booking details
                $stmt = $conn->prepare("SELECT buyer_name, buyer_phone, buyer_email, agent_name FROM prop_bookings WHERE id = ?");
                if (!$stmt) {
                    throw new Exception('Prepare failed: ' . $conn->error);
                }
                $stmt->bind_param("i", $booking_id);
                if (!$stmt->execute()) {
                    throw new Exception('Execute failed: ' . $stmt->error);
                }
                $booking_data = $stmt->get_result()->fetch_assoc();

                if (!$booking_data) {
                    throw new Exception('Booking record not found');
                }

                // Get estate_id from plot
                $stmt = $conn->prepare("SELECT estate_id FROM prop_plots WHERE id = ?");
                if (!$stmt) {
                    throw new Exception('Prepare failed: ' . $conn->error);
                }
                $stmt->bind_param("i", $plot_id);
                if (!$stmt->execute()) {
                    throw new Exception('Execute failed: ' . $stmt->error);
                }
                $plot_data = $stmt->get_result()->fetch_assoc();

                if (!$plot_data) {
                    throw new Exception('Plot record not found');
                }

                $estate_id = $plot_data['estate_id'];

                if ($estate_id === null || $estate_id <= 0) {
                    throw new Exception('Invalid estate ID for the plot');
                }

                // Delete booking record
                $stmt = $conn->prepare("DELETE FROM prop_bookings WHERE id = ?");
                if (!$stmt) {
                    throw new Exception('Prepare failed: ' . $conn->error);
                }
                $stmt->bind_param("i", $booking_id);
                if (!$stmt->execute()) {
                    throw new Exception('Execute failed: ' . $stmt->error);
                }

                // Ensure no null values for required fields
                $agent_name = $booking_data['agent_name'] ?? '';
                $buyer_name = $booking_data['buyer_name'] ?? '';
                $buyer_phone = $booking_data['buyer_phone'] ?? '';
                $buyer_email = $booking_data['buyer_email'] ?? '';

                // Insert sale record (amount will need to be set, for now using 0)
                $stmt = $conn->prepare("INSERT INTO prop_sales (plot_id, estate_id, agent_name, buyer_name, buyer_phone, buyer_email, amount, payment_plan, deposit_doc, id_doc, kra_doc, passport_photo) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)");
                if (!$stmt) {
                    throw new Exception('Prepare failed: ' . $conn->error);
                }
                $stmt->bind_param("iissdsssssss", $plot_id, $estate_id, $agent_name, $buyer_name, $buyer_phone, $buyer_email, 0, 'full', '', '', '', '');
                if (!$stmt->execute()) {
                    throw new Exception('Execute failed: ' . $stmt->error);
                }

                // Update plot status to sold
                $stmt = $conn->prepare("UPDATE prop_plots SET status = 'sold' WHERE id = ?");
                if (!$stmt) {
                    throw new Exception('Prepare failed: ' . $conn->error);
                }
                $stmt->bind_param("i", $plot_id);
                if (!$stmt->execute()) {
                    throw new Exception('Execute failed: ' . $stmt->error);
                }
            }

        } elseif ($action === 'make_sa_signed') {
            // Update plot status to sa_signed
            $stmt = $conn->prepare("UPDATE prop_plots SET status = 'sa_signed' WHERE id = ?");
            if (!$stmt) {
                throw new Exception('Prepare failed: ' . $conn->error);
            }
            $stmt->bind_param("i", $plot_id);
            if (!$stmt->execute()) {
                throw new Exception('Execute failed: ' . $stmt->error);
            }

            // Log admin action
            logDataModification('prop_plots', 'UPDATE', $plot_id, ['status' => 'sa_signed']);

        } elseif ($action === 'make_sa_signed_from_booking') {
            // Mark booked plot as SA Signed
            $booking_id = intval($_POST['booking_id'] ?? 0);
            $date_signed = $_POST['date_signed'] ?? null;
            if ($booking_id <= 0) {
                throw new Exception('Invalid booking ID');
            }

            // Update booking status to sa_signed and set date_signed
            $stmt = $conn->prepare("UPDATE prop_bookings SET status = 'sa_signed', date_signed = ? WHERE id = ?");
            if (!$stmt) {
                throw new Exception('Prepare failed: ' . $conn->error);
            }
            $stmt->bind_param("si", $date_signed, $booking_id);
            if (!$stmt->execute()) {
                throw new Exception('Execute failed: ' . $stmt->error);
            }

            // Update plot status to sa_signed
            $stmt = $conn->prepare("UPDATE prop_plots SET status = 'sa_signed' WHERE id = ?");
            if (!$stmt) {
                throw new Exception('Prepare failed: ' . $conn->error);
            }
            $stmt->bind_param("i", $plot_id);
            if (!$stmt->execute()) {
                throw new Exception('Execute failed: ' . $stmt->error);
            }

            // Log admin action
            logDataModification('prop_plots', 'UPDATE', $plot_id, ['status' => 'sa_signed', 'from' => 'booked', 'date_signed' => $date_signed]);

        } elseif ($action === 'make_sold_from_booking') {
            // Mark booked plot as Sold
            $booking_id = intval($_POST['booking_id'] ?? 0);
            if ($booking_id <= 0) {
                throw new Exception('Invalid booking ID');
            }

            // Get booking details
            $stmt = $conn->prepare("SELECT buyer_name, buyer_phone, buyer_email, agent_name FROM prop_bookings WHERE id = ?");
            if (!$stmt) {
                throw new Exception('Prepare failed: ' . $conn->error);
            }
            $stmt->bind_param("i", $booking_id);
            if (!$stmt->execute()) {
                throw new Exception('Execute failed: ' . $stmt->error);
            }
            $booking_data = $stmt->get_result()->fetch_assoc();

            if (!$booking_data) {
                throw new Exception('Booking record not found');
            }

            // Get estate_id from plot
            $stmt = $conn->prepare("SELECT estate_id FROM prop_plots WHERE id = ?");
            if (!$stmt) {
                throw new Exception('Prepare failed: ' . $conn->error);
            }
            $stmt->bind_param("i", $plot_id);
            if (!$stmt->execute()) {
                throw new Exception('Execute failed: ' . $stmt->error);
            }
            $plot_data = $stmt->get_result()->fetch_assoc();

            if (!$plot_data) {
                throw new Exception('Plot record not found');
            }

            $estate_id = $plot_data['estate_id'];

            // Delete booking record
            $stmt = $conn->prepare("DELETE FROM prop_bookings WHERE id = ?");
            if (!$stmt) {
                throw new Exception('Prepare failed: ' . $conn->error);
            }
            $stmt->bind_param("i", $booking_id);
            if (!$stmt->execute()) {
                throw new Exception('Execute failed: ' . $stmt->error);
            }

            // Insert sale record
            $agent_name = $booking_data['agent_name'] ?? '';
            $buyer_name = $booking_data['buyer_name'] ?? '';
            $buyer_phone = $booking_data['buyer_phone'] ?? '';
            $buyer_email = $booking_data['buyer_email'] ?? '';

            $stmt = $conn->prepare("INSERT INTO prop_sales (plot_id, estate_id, agent_name, buyer_name, buyer_phone, buyer_email, amount, payment_plan, deposit_timing) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)");
            if (!$stmt) {
                throw new Exception('Prepare failed: ' . $conn->error);
            }
            $amount = 0;
            $payment_plan = 'N/A';
            $deposit_timing = 'admin_action';
            $stmt->bind_param("iissssdss", $plot_id, $estate_id, $agent_name, $buyer_name, $buyer_phone, $buyer_email, $amount, $payment_plan, $deposit_timing);
            if (!$stmt->execute()) {
                throw new Exception('Execute failed: ' . $stmt->error);
            }

            // Update plot status to sold
            $stmt = $conn->prepare("UPDATE prop_plots SET status = 'sold' WHERE id = ?");
            if (!$stmt) {
                throw new Exception('Prepare failed: ' . $conn->error);
            }
            $stmt->bind_param("i", $plot_id);
            if (!$stmt->execute()) {
                throw new Exception('Execute failed: ' . $stmt->error);
            }

            // Log admin action
            logDataModification('prop_plots', 'UPDATE', $plot_id, ['status' => 'sold', 'from' => 'booked']);

        } elseif ($action === 'make_fully_paid') {
            // Remove fully paid functionality - this action is no longer supported
            throw new Exception('Fully paid status has been removed from the system');
        }

        $conn->commit();
        echo json_encode(['success' => true]);

    } catch (Exception $e) {
        try {
            $conn->rollback();
        } catch (Exception $rollback_e) {
            // Ignore rollback errors
        }
        echo json_encode(['success' => false, 'message' => $e->getMessage()]);
    }
} else {
    echo json_encode(['success' => false, 'message' => 'Invalid request method']);
}

$conn->close();
?>
