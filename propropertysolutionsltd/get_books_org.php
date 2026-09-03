<?php
include 'zoho_functions.php';

$org_id = getZohoBooksOrganizationId();
if ($org_id) {
    echo "Organization ID: $org_id\n";
    echo "Update ZOHO_BOOKS_ORGANIZATION_ID in zoho_functions.php with this value.\n";
} else {
    echo "Failed to get organization ID.\n";
}
?>