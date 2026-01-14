package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3" // SQLite driver for demonstration
	"github.com/michael-jaquier/circuitbreaker"
)

// DB wraps a database connection with circuit breaker protection
// This protects against database connection failures, timeouts, and overload
type DB struct {
	db      *sql.DB
	breaker circuitbreaker.CircuitBreaker
}

// NewDB creates a new circuit-breaker protected database connection
// The circuit breaker will:
// - Open on any database error (connection failures, query timeouts)
// - Prevent hammering a struggling database
// - Allow recovery through controlled probe requests
func NewDB(db *sql.DB) (*DB, error) {
	cb, err := circuitbreaker.NewZeroTolerance(
		circuitbreaker.WithCooldownTimer(30*time.Second),
		circuitbreaker.WithSuccessToClose(5),
		circuitbreaker.WithWindowSize(120*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create circuit breaker: %w", err)
	}

	return &DB{
		db:      db,
		breaker: cb,
	}, nil
}

// Query executes a query that returns multiple rows with circuit breaker protection
func (d *DB) Query(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	var rows *sql.Rows
	var queryErr error

	timer, err := d.breaker.Execute(ctx, func(ctx context.Context) error {
		rows, queryErr = d.db.QueryContext(ctx, query, args...)
		return queryErr
	})

	if timer != nil {
		return nil, fmt.Errorf("circuit breaker open")
	}
	if err != nil {
		return nil, fmt.Errorf("query failed: %w", err)
	}

	return rows, queryErr
}

// QueryRow executes a query that returns a single row with circuit breaker protection
func (d *DB) QueryRow(ctx context.Context, query string, args ...interface{}) (*sql.Row, error) {
	var row *sql.Row

	timer, err := d.breaker.Execute(ctx, func(ctx context.Context) error {
		row = d.db.QueryRowContext(ctx, query, args...)
		return nil // QueryRow doesn't return an error, defer error checking to Scan()
	})

	if timer != nil {
		return nil, fmt.Errorf("circuit breaker open")
	}
	if err != nil {
		return nil, err
	}

	return row, nil
}

// Exec executes a query without returning rows (INSERT, UPDATE, DELETE)
func (d *DB) Exec(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	var result sql.Result
	var execErr error

	timer, err := d.breaker.Execute(ctx, func(ctx context.Context) error {
		result, execErr = d.db.ExecContext(ctx, query, args...)
		return execErr
	})

	if timer != nil {
		return nil, fmt.Errorf("circuit breaker open")
	}
	if err != nil {
		return nil, fmt.Errorf("exec failed: %w", err)
	}

	return result, execErr
}

// Close closes the underlying database connection
func (d *DB) Close() error {
	return d.db.Close()
}

func main() {
	// Create an in-memory SQLite database for demonstration
	rawDB, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		panic(fmt.Sprintf("Failed to open database: %v", err))
	}
	defer rawDB.Close()

	// Wrap with circuit breaker
	db, err := NewDB(rawDB)
	if err != nil {
		panic(fmt.Sprintf("Failed to create circuit breaker DB: %v", err))
	}
	defer db.Close()

	ctx := context.Background()

	// Example 1: Create table and insert data
	fmt.Println("=== Example 1: Create table and insert data ===")
	_, err = db.Exec(ctx, `
		CREATE TABLE users (
			id INTEGER PRIMARY KEY,
			username TEXT NOT NULL,
			email TEXT NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		fmt.Printf("Failed to create table: %v\n", err)
		return
	}
	fmt.Println("Table created successfully")

	// Insert multiple users
	users := []struct {
		username string
		email    string
	}{
		{"alice", "alice@example.com"},
		{"bob", "bob@example.com"},
		{"charlie", "charlie@example.com"},
	}

	for _, user := range users {
		_, err := db.Exec(ctx, "INSERT INTO users (username, email) VALUES (?, ?)", user.username, user.email)
		if err != nil {
			fmt.Printf("Failed to insert user %s: %v\n", user.username, err)
		} else {
			fmt.Printf("Inserted user: %s\n", user.username)
		}
	}
	fmt.Println()

	// Example 2: Query multiple rows
	fmt.Println("=== Example 2: Query all users ===")
	rows, err := db.Query(ctx, "SELECT id, username, email FROM users ORDER BY id")
	if err != nil {
		fmt.Printf("Query failed: %v\n", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var id int
		var username, email string
		if err := rows.Scan(&id, &username, &email); err != nil {
			fmt.Printf("Scan failed: %v\n", err)
			continue
		}
		fmt.Printf("User %d: %s (%s)\n", id, username, email)
	}
	fmt.Println()

	// Example 3: Query single row
	fmt.Println("=== Example 3: Query single user by username ===")
	row, err := db.QueryRow(ctx, "SELECT id, email FROM users WHERE username = ?", "alice")
	if err != nil {
		fmt.Printf("QueryRow failed: %v\n", err)
	} else {
		var id int
		var email string
		err = row.Scan(&id, &email)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				fmt.Println("User not found")
			} else {
				fmt.Printf("Scan failed: %v\n", err)
			}
		} else {
			fmt.Printf("Found user: ID=%d, Email=%s\n", id, email)
		}
	}
	fmt.Println()

	// Example 4: Update operation
	fmt.Println("=== Example 4: Update user email ===")
	result, err := db.Exec(ctx, "UPDATE users SET email = ? WHERE username = ?", "alice.new@example.com", "alice")
	if err != nil {
		fmt.Printf("Update failed: %v\n", err)
	} else {
		affected, _ := result.RowsAffected()
		fmt.Printf("Updated %d row(s)\n", affected)
	}
	fmt.Println()

	// Example 5: Demonstrate circuit breaker behavior with bad query
	fmt.Println("=== Example 5: Demonstrate circuit breaker with errors ===")

	// This query will fail (table doesn't exist)
	_, err = db.Query(ctx, "SELECT * FROM nonexistent_table")
	if err != nil {
		fmt.Printf("Expected error (table doesn't exist): %v\n", err)
		fmt.Println("Circuit breaker recorded this failure")
	}

	// Since we're using zero tolerance, circuit is now open
	// Next request should be blocked
	_, err = db.Query(ctx, "SELECT * FROM users")
	if err != nil {
		if strings.Contains(err.Error(), "circuit breaker is open") {
			fmt.Println("Circuit breaker successfully blocked the request!")
			fmt.Println("Database is protected from being hammered during failures")
		} else {
			fmt.Printf("Unexpected error: %v\n", err)
		}
	}
	fmt.Println()

	fmt.Println("=== Circuit Breaker Database Integration Complete ===")
	fmt.Println("Key takeaways:")
	fmt.Println("1. Circuit protects database from connection failures and overload")
	fmt.Println("2. Failed queries open the circuit immediately (zero tolerance)")
	fmt.Println("3. Circuit recovers after cooldown period with successful probes")
	fmt.Println("4. Execute method automatically reports success/failure")
	fmt.Println("5. In production, combine with connection pooling and timeouts")
}
