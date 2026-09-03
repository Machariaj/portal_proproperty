package main

import "log"

// initPlotStatusLog creates the table that records every plot status change.
func initPlotStatusLog() {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS prop_plot_status_log (
		id          INT AUTO_INCREMENT PRIMARY KEY,
		plot_id     INT          NOT NULL,
		plot_number VARCHAR(50)  NOT NULL,
		estate_id   INT          NOT NULL,
		estate_name VARCHAR(255) NOT NULL,
		old_status  VARCHAR(50)  NOT NULL,
		new_status  VARCHAR(50)  NOT NULL,
		changed_by  VARCHAR(255) DEFAULT '',
		reason      VARCHAR(255) DEFAULT '',
		changed_at  DATETIME     DEFAULT CURRENT_TIMESTAMP,
		INDEX idx_plot      (plot_id),
		INDEX idx_estate    (estate_id),
		INDEX idx_changed_at(changed_at)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`); err != nil {
		log.Printf("[plotlog] create table: %v", err)
	}
}

// logPlotStatus records a single status transition.
// changedBy is the agent/admin name; reason is optional context.
func logPlotStatus(plotID int, plotNumber, estateName string, estateID int, oldStatus, newStatus, changedBy, reason string) {
	if _, err := db.Exec(
		`INSERT INTO prop_plot_status_log
		 (plot_id, plot_number, estate_id, estate_name, old_status, new_status, changed_by, reason)
		 VALUES (?,?,?,?,?,?,?,?)`,
		plotID, plotNumber, estateID, estateName, oldStatus, newStatus, changedBy, reason,
	); err != nil {
		log.Printf("[plotlog] insert plot=%d %s→%s: %v", plotID, oldStatus, newStatus, err)
	}
}
