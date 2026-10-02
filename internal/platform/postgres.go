package platform

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"fanbbs.local/backend/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// OpenPostgres opens a pgx stdlib pool and applies the embedded PostgreSQL
// migrations. Existing domain SQL uses SQLite-style positional parameters, so
// the connector below rebinds them before pgx sees each statement (including
// statements executed inside database/sql transactions).
func OpenPostgres(ctx context.Context, databaseURL string) (*sql.DB, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, fmt.Errorf("open postgres: database URL is required")
	}
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse postgres config: %w", err)
	}
	if config.RuntimeParams == nil {
		config.RuntimeParams = map[string]string{}
	}
	config.RuntimeParams["timezone"] = "UTC"
	base := stdlib.GetConnector(*config)
	db := sql.OpenDB(rebindingConnector{Connector: base})
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	db.SetConnMaxIdleTime(5 * time.Minute)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	if err := ApplyPostgresMigrations(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// OpenDatabase selects the explicitly configured adapter. SQLite remains the
// local default; production configuration must opt in to PostgreSQL.
func OpenDatabase(ctx context.Context, adapter, target string) (*sql.DB, error) {
	switch strings.ToLower(strings.TrimSpace(adapter)) {
	case "", "sqlite", "sqlite3":
		return OpenSQLite(ctx, target)
	case "postgres", "postgresql", "pgx":
		return OpenPostgres(ctx, target)
	default:
		return nil, fmt.Errorf("unsupported database adapter %q", adapter)
	}
}

// ApplyPostgresMigrations applies every PostgreSQL migration exactly once. A
// transaction-scoped advisory lock serializes concurrent application starts;
// stored checksums prevent a deployed migration from being silently rewritten.
func ApplyPostgresMigrations(ctx context.Context, db *sql.DB) error {
	entries, err := fs.ReadDir(migrations.PostgresFiles, "postgres")
	if err != nil {
		return fmt.Errorf("read postgres migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		body, err := migrations.PostgresFiles.ReadFile("postgres/" + name)
		if err != nil {
			return fmt.Errorf("read postgres migration %s: %w", name, err)
		}
		sum := sha256.Sum256(body)
		checksum := hex.EncodeToString(sum[:])
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin postgres migration %s: %w", name, err)
		}
		if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(6472117843201)`); err == nil {
			_, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
				version TEXT PRIMARY KEY,
				checksum TEXT NOT NULL,
				applied_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
			)`)
		}
		var existing string
		if err == nil {
			err = tx.QueryRowContext(ctx, `SELECT checksum FROM schema_migrations WHERE version = ?`, name).Scan(&existing)
			if err == nil && existing != checksum {
				err = fmt.Errorf("migration checksum mismatch: %s", name)
			}
			if errors.Is(err, sql.ErrNoRows) {
				err = nil
				if _, err = tx.ExecContext(ctx, string(body)); err == nil {
					_, err = tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, checksum) VALUES (?, ?)`, name, checksum)
				}
			}
		}
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("apply postgres migration %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit postgres migration %s: %w", name, err)
		}
	}
	return nil
}

type rebindingConnector struct{ driver.Connector }

func (c rebindingConnector) Connect(ctx context.Context) (driver.Conn, error) {
	connection, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &rebindingConn{Conn: connection}, nil
}

type rebindingConn struct{ driver.Conn }

func (c *rebindingConn) Prepare(query string) (driver.Stmt, error) {
	return c.Conn.Prepare(postgresQuery(query))
}

func (c *rebindingConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if connection, ok := c.Conn.(driver.ConnPrepareContext); ok {
		return connection.PrepareContext(ctx, postgresQuery(query))
	}
	return c.Prepare(query)
}

func (c *rebindingConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if connection, ok := c.Conn.(driver.ExecerContext); ok {
		return connection.ExecContext(ctx, postgresQuery(query), args)
	}
	return nil, driver.ErrSkip
}

func (c *rebindingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if connection, ok := c.Conn.(driver.QueryerContext); ok {
		return connection.QueryContext(ctx, postgresQuery(query), args)
	}
	return nil, driver.ErrSkip
}

func (c *rebindingConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if connection, ok := c.Conn.(driver.ConnBeginTx); ok {
		return connection.BeginTx(ctx, opts)
	}
	return c.Conn.Begin()
}

func (c *rebindingConn) Ping(ctx context.Context) error {
	if connection, ok := c.Conn.(driver.Pinger); ok {
		return connection.Ping(ctx)
	}
	return nil
}

func (c *rebindingConn) ResetSession(ctx context.Context) error {
	if connection, ok := c.Conn.(driver.SessionResetter); ok {
		return connection.ResetSession(ctx)
	}
	return nil
}

func (c *rebindingConn) IsValid() bool {
	if connection, ok := c.Conn.(driver.Validator); ok {
		return connection.IsValid()
	}
	return true
}

// postgresQuery rewrites positional parameters while ignoring quoted strings,
// quoted identifiers, dollar-quoted bodies and comments. Two aggregate helpers
// are also translated because the read model deliberately shares one compact
// query between SQLite and PostgreSQL.
func postgresQuery(query string) string {
	query = strings.ReplaceAll(query, "group_concat(", "string_agg(")
	query = strings.ReplaceAll(query, "char(", "chr(")
	query = strings.ReplaceAll(query, "u.rowid", "u.sequence")
	query = strings.ReplaceAll(query, "SELECT rowid,", "SELECT sequence,")

	var out strings.Builder
	out.Grow(len(query) + 16)
	parameter := 1
	for index := 0; index < len(query); {
		switch query[index] {
		case '\'':
			start := index
			index++
			for index < len(query) {
				if query[index] == '\'' {
					index++
					if index < len(query) && query[index] == '\'' {
						index++
						continue
					}
					break
				}
				index++
			}
			out.WriteString(query[start:index])
		case '"':
			start := index
			index++
			for index < len(query) {
				if query[index] == '"' {
					index++
					if index < len(query) && query[index] == '"' {
						index++
						continue
					}
					break
				}
				index++
			}
			out.WriteString(query[start:index])
		case '-':
			if index+1 < len(query) && query[index+1] == '-' {
				end := strings.IndexByte(query[index:], '\n')
				if end < 0 {
					out.WriteString(query[index:])
					index = len(query)
				} else {
					out.WriteString(query[index : index+end+1])
					index += end + 1
				}
				continue
			}
			out.WriteByte(query[index])
			index++
		case '/':
			if index+1 < len(query) && query[index+1] == '*' {
				end := strings.Index(query[index+2:], "*/")
				if end < 0 {
					out.WriteString(query[index:])
					index = len(query)
				} else {
					end += index + 4
					out.WriteString(query[index:end])
					index = end
				}
				continue
			}
			out.WriteByte(query[index])
			index++
		case '$':
			end := index + 1
			for end < len(query) && ((query[end] >= 'a' && query[end] <= 'z') || (query[end] >= 'A' && query[end] <= 'Z') || (query[end] >= '0' && query[end] <= '9') || query[end] == '_') {
				end++
			}
			if end < len(query) && query[end] == '$' {
				tag := query[index : end+1]
				closeAt := strings.Index(query[end+1:], tag)
				if closeAt >= 0 {
					closeAt += end + 1 + len(tag)
					out.WriteString(query[index:closeAt])
					index = closeAt
					continue
				}
			}
			out.WriteByte(query[index])
			index++
		case '?':
			out.WriteByte('$')
			out.WriteString(fmt.Sprint(parameter))
			parameter++
			index++
		default:
			out.WriteByte(query[index])
			index++
		}
	}
	return out.String()
}
