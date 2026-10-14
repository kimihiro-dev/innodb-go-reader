package mysql57

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func options() Options {
	return Options{Database: "db", Table: "table", User: "reader", PasswordEnv: "INNODB_DRIVER_TEST_PASSWORD", Timeout: time.Second}
}

func TestDriverConnectionConfig(t *testing.T) {
	t.Setenv("INNODB_DRIVER_TEST_PASSWORD", "opaque:@/password")
	o := options()
	cfg, err := connectionConfig(o)
	if err != nil || cfg.Net != "tcp" || cfg.Addr != "127.0.0.1:3306" || cfg.User != "reader" || cfg.Passwd != "opaque:@/password" || cfg.TLS != nil {
		t.Fatal("default TCP connection", err)
	}
	o.Host = "::1"
	o.Port = 3307
	o.TLS = true
	cfg, err = connectionConfig(o)
	if err != nil || cfg.Addr != "[::1]:3307" || cfg.TLS.ServerName != "::1" || cfg.TLS.InsecureSkipVerify {
		t.Fatal("IPv6/TLS verification", err)
	}
	o = options()
	o.Socket = "/tmp/mysql.sock"
	cfg, err = connectionConfig(o)
	if err != nil || cfg.Net != "unix" || cfg.Addr != o.Socket {
		t.Fatal("Unix socket", err)
	}
	for _, mutate := range []func(*Options){
		func(o *Options) { o.Port = 65536 }, func(o *Options) { o.User = "" }, func(o *Options) { o.Socket = "sock"; o.Host = "host" }, func(o *Options) { o.Socket = "sock"; o.TLS = true }, func(o *Options) { o.Timeout = 0 }, func(o *Options) { o.TLSCA = "ca" }, func(o *Options) { o.PasswordEnv = "bad=name" }, func(o *Options) { o.Host = "bad\x00host" },
	} {
		o = options()
		mutate(&o)
		if _, err = connectionConfig(o); err == nil {
			t.Fatal("invalid connection accepted", o)
		}
	}
	o = options()
	o.TLS = true
	o.TLSCA = filepath.Join(t.TempDir(), "ca.pem")
	os.WriteFile(o.TLSCA, []byte("invalid certificate"), 0600)
	if _, err = connectionConfig(o); err == nil {
		t.Fatal("invalid CA accepted")
	}
}

// The database/sql transport is mocked here; real MySQL authentication, TCP,
// socket and TLS are separately verified against a dedicated 5.7.44 instance.
type responseDriver struct{}
type responseConn struct{ mode string }
type responseRows struct {
	mode string
	sent bool
}

func (responseDriver) Open(mode string) (driver.Conn, error) { return responseConn{mode}, nil }
func (responseConn) Prepare(string) (driver.Stmt, error)     { return nil, errors.New("unused Prepare") }
func (responseConn) Close() error                            { return nil }
func (responseConn) Begin() (driver.Tx, error)               { return nil, errors.New("unused Begin") }
func (c responseConn) QueryContext(ctx context.Context, _ string, _ []driver.NamedValue) (driver.Rows, error) {
	if c.mode == "cancel" {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &responseRows{mode: c.mode}, nil
}
func (*responseRows) Columns() []string { return []string{"metadata"} }
func (*responseRows) Close() error      { return nil }
func (r *responseRows) Next(values []driver.Value) error {
	if r.sent {
		if r.mode == "row_error" {
			return errors.New("row stream failed")
		}
		return io.EOF
	}
	r.sent = true
	switch r.mode {
	case "invalid":
		values[0] = "invalid hexadecimal"
	case "invalid_json":
		values[0] = hex.EncodeToString([]byte("{"))
	case "too_large":
		values[0] = strings.Repeat("0", (8<<20)+2)
	default:
		values[0] = hex.EncodeToString([]byte(`{"number":18446744073709551615,"text":"界'\\","nullable":null}`))
	}
	return nil
}
func init() { sql.Register("innodb_schema_test", responseDriver{}) }

func TestDriverMetadataDecoding(t *testing.T) {
	for _, mode := range []string{"valid", "invalid", "invalid_json", "row_error", "too_large", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			db, err := sql.Open("innodb_schema_test", mode)
			if err != nil {
				t.Fatal(err)
			}
			conn, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			c := &client{ctx: ctx, db: db, conn: conn}
			if mode == "cancel" {
				cancel()
			}
			var rows []struct {
				Number   uint64
				Text     string
				Nullable *string
			}
			err = c.objects("JSON_OBJECT('n',1)", "", &rows)
			if mode == "valid" {
				if err != nil || len(rows) != 1 || rows[0].Number != ^uint64(0) || rows[0].Text != "界'\\" || rows[0].Nullable != nil {
					t.Fatal("lossless decode", rows, err)
				}
			} else if err == nil || len(rows) != 0 {
				t.Fatal("invalid/partial result", rows, err)
			}
			if final := c.finish(err); mode == "valid" && final != nil {
				t.Fatal(final)
			} else if mode == "cancel" && !errors.Is(final, context.Canceled) {
				t.Fatal("cancel lost", final)
			}
		})
	}
}
