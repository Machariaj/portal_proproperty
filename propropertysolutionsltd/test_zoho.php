<?php
// Test Zoho CRM API integration

// Include shared Zoho functions
include 'zoho_functions.php';

// Test data
$buyer_name = 'Test Buyer ' . time(); // Unique name
$buyer_phone = '1234567890';
$buyer_email = 'test@example.com';
$plot_number = 'P001';
$estate_name = 'Test Estate';
$amount = '1000000';
$agent_name = 'Test Agent';

// Test deal creation
echo "Testing Zoho CRM Deal Creation...\n";

$dealData = [
    'data' => [
        [
            'Deal_Name' => $buyer_name,
            'Phone_Number' => $buyer_phone,
            'Buyer_Email' => $buyer_email,
            'Deposit' => (float)$amount,
            'Payment_Plan' => 'Test Plan',
            'Agent_Name' => $agent_name,
            'Estates' => $estate_name,
            'Plot' => (string)$plot_number,
            'Stage' => 'Closed Won',
            'Closing_Date' => date('Y-m-d'),
            'Description' => "Test deal: Plot: $plot_number, Estate: $estate_name, Agent: $agent_name"
        ]
    ]
];

$deal_id = createZohoDeal($dealData);

if ($deal_id) {
    echo "✅ Deal created successfully with ID: $deal_id\n";

    // Test attachment upload if files exist
    $files = glob('uploads/*');
    if (!empty($files)) {
        $test_file = $files[0];
        echo "Testing attachment upload with file: $test_file\n";

        if (uploadZohoAttachment($deal_id, $test_file, 'Test Attachment', 'Deals')) {
            echo "✅ Attachment uploaded successfully\n";
        } else {
            echo "❌ Attachment upload failed\n";
        }
    } else {
        echo "No files found in uploads directory for attachment test\n";
    }
} else {
    echo "❌ Deal creation failed\n";
}

// Test customer creation in Zoho Books
echo "Testing Zoho Books Customer Creation...\n";

$customerData = [
    'contact_name' => $buyer_name,
    'contact_type' => 'customer',
    'email' => $buyer_email,
    'phone' => $buyer_phone
];

$notes = "Estate: $estate_name, Plot: $plot_number, Sale Date: " . date('Y-m-d');
$contactPersons = [
    [
        'first_name' => explode(' ', $buyer_name)[0] ?? $buyer_name,
        'last_name' => explode(' ', $buyer_name, 2)[1] ?? '',
        'salutation' => '',
        'email' => $buyer_email,
        'mobile' => $buyer_phone,
        'is_primary_contact' => true
    ],
    [
        'first_name' => 'Test',
        'last_name' => 'Agent',
        'salutation' => '',
        'email' => '',
        'mobile' => ''
    ]
];

$customer_id = createZohoBooksCustomer($customerData, $notes, $contactPersons);

if ($customer_id) {
    echo "✅ Customer created successfully with ID: $customer_id\n";
} else {
    echo "❌ Customer creation failed\n";
}

echo "Test completed. Check Zoho CRM for new deal and Zoho Books for new customer.\n";
?>
