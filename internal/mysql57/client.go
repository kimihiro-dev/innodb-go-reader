// Package mysql57 collects trusted definitions through validated MySQL 5.7 SQL connections.
package mysql57

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/go-sql-driver/mysql"
)

type client struct {
	ctx              context.Context
	db               *sql.DB
	conn             *sql.Conn
	network, address string
}

func connectionConfig(o Options) (*mysql.Config, error) {
	if err := ValidateOptions(o); err != nil {
		return nil, err
	}
	cfg := mysql.NewConfig()
	cfg.User = o.User
	cfg.Passwd = os.Getenv(o.PasswordEnv)
	cfg.Net = "tcp"
	host, port := o.Host, o.Port
	if host == "" {
		host = "127.0.0.1"
	}
	if port == 0 {
		port = 3306
	}
	cfg.Addr = net.JoinHostPort(host, strconv.Itoa(port))
	if o.Socket != "" {
		cfg.Net, cfg.Addr = "unix", o.Socket
	}
	cfg.Timeout = o.Timeout
	if err := cfg.Apply(mysql.Charset("utf8mb4", "")); err != nil {
		return nil, err
	}
	cfg.MaxAllowedPacket = 128 << 20
	// Errors go through the CLI JSON diagnostic stream, never global driver logs.
	cfg.Logger = log.New(io.Discard, "", 0)
	if o.TLS {
		cfg.TLS = &tls.Config{
			MinVersion: tls.VersionTLS12, ServerName: host,
			// Some 5.7 servers only offer RSA key exchange, removed from Go's
			// defaults. Keep verified TLS with AES-GCM; RSA lacks forward secrecy.
			CipherSuites: []uint16{
				tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
			},
		}
		if o.TLSCA != "" {
			pem, err := os.ReadFile(o.TLSCA)
			if err != nil {
				return nil, err
			}
			pool, err := x509.SystemCertPool()
			if err != nil || pool == nil {
				pool = x509.NewCertPool()
			}
			if !pool.AppendCertsFromPEM(pem) {
				return nil, fmt.Errorf("TLS CA file contains no valid certificates")
			}
			cfg.TLS.RootCAs = pool
		}
	}
	return cfg, nil
}

func newClient(ctx context.Context, o Options) (*client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cfg, err := connectionConfig(o)
	if err != nil {
		return nil, err
	}
	connector, err := mysql.NewConnector(cfg)
	if err != nil {
		return nil, err
	}
	c := &client{ctx: ctx, db: sql.OpenDB(connector), network: cfg.Net, address: cfg.Addr}
	c.db.SetMaxOpenConns(1)
	c.conn, err = c.db.Conn(ctx)
	if err != nil {
		c.db.Close()
		return nil, err
	}
	for _, query := range []string{"SET SESSION time_zone='+00:00'", "SET SESSION sql_mode='STRICT_ALL_TABLES,NO_ENGINE_SUBSTITUTION'"} {
		if _, err = c.conn.ExecContext(ctx, query); err != nil {
			return nil, c.finish(err)
		}
	}
	return c, nil
}

func (c *client) objects(expression, tail string, value any) error {
	rows, err := c.conn.QueryContext(c.ctx, "SELECT HEX(CAST("+expression+" AS CHAR CHARACTER SET utf8mb4)) "+tail)
	if err != nil {
		return err
	}
	defer rows.Close()
	raw := make([]json.RawMessage, 0)
	for rows.Next() {
		var encoded []byte
		if err = rows.Scan(&encoded); err != nil {
			return err
		}
		if len(encoded) > 8<<20 {
			return fmt.Errorf("metadata response exceeds 8 MiB")
		}
		decoded := make([]byte, hex.DecodedLen(len(encoded)))
		n, err := hex.Decode(decoded, encoded)
		if err != nil || !json.Valid(decoded[:n]) {
			return fmt.Errorf("invalid mysql metadata response")
		}
		raw = append(raw, decoded[:n])
	}
	if err = rows.Err(); err != nil {
		return err
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

func (c *client) finish(prior error) error {
	err := c.conn.Close()
	if e := c.db.Close(); err == nil {
		err = e
	}
	if c.ctx.Err() != nil {
		return c.ctx.Err()
	}
	if prior != nil {
		return prior
	}
	return err
}

func ident(value string) string { return "`" + strings.ReplaceAll(value, "`", "``") + "`" }
func literal(value string) string {
	return "CONVERT(X'" + hex.EncodeToString([]byte(value)) + "' USING utf8mb4)"
}
