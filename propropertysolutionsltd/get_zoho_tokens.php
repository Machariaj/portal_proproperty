<?php
// Script to exchange authorization code for access and refresh tokens

$client_id = '1000.0GHL3CW4SJUE2BJ3SOUQ6KIJB4Q2NN';
$client_secret = '3c01794c2d6172f0636d041751d7562157d7e7d885';
$code = '1000.93c6da51871466490c1508d4bce454fd.44b1b0813890ecbf6b3d8cd7e5950689';
$token_url = 'https://accounts.zoho.com/oauth/v2/token';

$postData = [
    'code' => $code,
    'client_id' => $client_id,
    'client_secret' => $client_secret,
    'grant_type' => 'authorization_code'
];

$ch = curl_init();
curl_setopt($ch, CURLOPT_URL, $token_url);
curl_setopt($ch, CURLOPT_POST, true);
curl_setopt($ch, CURLOPT_POSTFIELDS, http_build_query($postData));
curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
curl_setopt($ch, CURLOPT_HTTPHEADER, ['Content-Type: application/x-www-form-urlencoded']);
curl_setopt($ch, CURLOPT_SSL_VERIFYPEER, false);

$response = curl_exec($ch);
$http_code = curl_getinfo($ch, CURLINFO_HTTP_CODE);
$error = curl_error($ch);
curl_close($ch);

echo "Token exchange response: $response\n";
echo "HTTP code: $http_code\n";
echo "Curl error: $error\n";

$data = json_decode($response, true);
if (isset($data['access_token']) && isset($data['refresh_token'])) {
    echo "New Access Token: " . $data['access_token'] . "\n";
    echo "New Refresh Token: " . $data['refresh_token'] . "\n";
    echo "Expires in: " . $data['expires_in'] . " seconds\n";
} else {
    echo "Failed to get tokens. Response: $response\n";
}
?>
