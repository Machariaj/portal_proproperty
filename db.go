package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	"github.com/go-sql-driver/mysql"
)

var db *sql.DB

func initDB() {
	host := os.Getenv("DB_HOST")
	user := os.Getenv("DB_USER")
	pass := os.Getenv("DB_PASS")
	name := os.Getenv("DB_NAME")

	// Fall back to local dev defaults
	if host == "" {
		host = "127.0.0.1"
	}
	if user == "" {
		user = "root"
	}
	if name == "" {
		name = "proproperty"
	}

	dsn := fmt.Sprintf("%s:%s@tcp(%s)/%s?parseTime=true&charset=utf8mb4&collation=utf8mb4_general_ci&loc=Local", user, pass, host, name)

	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		log.Fatalf("parse dsn: %v", err)
	}
	// Params are sent as SET statements on every new connection
	cfg.Params = map[string]string{"time_zone": "'+03:00'"}

	connector, err := mysql.NewConnector(cfg)
	if err != nil {
		log.Fatalf("mysql connector: %v", err)
	}
	db = sql.OpenDB(connector)
	if err = db.Ping(); err != nil {
		log.Fatalf("connect db: %v", err)
	}
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(10)
	log.Printf("Database connected: %s", name)
}
