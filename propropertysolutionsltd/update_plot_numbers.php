<?php
include 'db_connection.php';

$conn->query("UPDATE prop_plots SET plot_number = REPLACE(plot_number, 'Plot ', '')");
echo "Updated plot numbers.";
?>
