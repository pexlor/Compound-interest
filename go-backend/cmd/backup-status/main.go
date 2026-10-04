// Command backup-status reads local queue health or explicitly seeds a new empty target.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fulibu-go/internal/database"
)

func main() {
	reset := flag.Bool("reinitialize", false, "stop the server and seed a new empty MySQL backup database")
	flag.Parse()
	if err := run(*reset); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(reset bool) error {
	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = "./data"
	}
	dsn := strings.TrimSpace(os.Getenv("MYSQL_DSN"))
	if _, err := os.Stat(filepath.Join(dataDir, "fulibu.db")); err != nil {
		return fmt.Errorf("existing SQLite database required: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if reset {
		if dsn == "" {
			return fmt.Errorf("MYSQL_DSN is required for reinitialization")
		}
		lock, err := database.AcquireDataLock(dataDir)
		if err != nil {
			return err
		}
		defer lock.Close()
		primary, err := database.Open(dataDir)
		if err != nil {
			return err
		}
		defer primary.Close()
		target, err := database.OpenBackupMySQL(dsn)
		if err != nil {
			return err
		}
		defer target.Close()
		if _, err := database.ReinitializeMySQLBackup(ctx, primary, target); err != nil {
			// Driver errors can contain connection details or business values.
			if err == database.ErrBackupUnownedTarget {
				return err
			}
			return fmt.Errorf("cannot reinitialize MySQL backup; check connectivity and an empty dedicated target")
		}
		fmt.Println("Backup snapshot queued. Restart the server with the new MYSQL_DSN.")
		return nil
	}
	primary, err := sql.Open("sqlite3", filepath.Join(dataDir, "fulibu.db")+"?mode=ro&_busy_timeout=5000")
	if err != nil {
		return err
	}
	defer primary.Close()
	status, err := database.ReadBackupStatus(ctx, primary, dsn != "")
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(status)
}
