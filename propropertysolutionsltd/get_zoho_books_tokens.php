<?php
// Script to exchange authorization code for access and refresh tokens for Zoho Books

$client_id = '1000.BLZ4BW6YWVYJIDIZWU9HR8C01DT3VQ';
$client_secret = 'd7200f642a0afa4dbb37a4259a78913dfb9e95de03';
$code = '1000.ebc85f18b010ebf7c1938f83151b773a.0844e629ce031815e29e7ffda82dfbfd'; // Authorization code from OAuth flow
$token_url = 'https://accounts.zoho.com/oauth/v2/token';

$postData = [
    'code' => $code,
    'client_id' => $client_id,
    'client_secret' => $client_secret,
    'redirect_uri' => 'http://localhost/propropertysolutions/callback.php',
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