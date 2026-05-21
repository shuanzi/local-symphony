package db

/*
#cgo linux LDFLAGS: -lsqlite3
#cgo darwin LDFLAGS: -lsqlite3
#include <sqlite3.h>
#include <stdlib.h>

static int bind_text_transient(sqlite3_stmt *stmt, int idx, const char *v) {
    return sqlite3_bind_text(stmt, idx, v, -1, SQLITE_TRANSIENT);
}
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"unsafe"
)

type DB struct {
	mu   sync.Mutex
	ptr  *C.sqlite3
	path string
}

var closeSQLite = func(d *DB) int {
	return int(C.sqlite3_close(d.ptr))
}

type Tx struct {
	db *DB
}

type Value struct {
	Text string
	Null bool
}

func (v Value) String() string {
	if v.Null {
		return ""
	}
	return v.Text
}
func (v Value) Int() int     { i, _ := strconv.Atoi(v.String()); return i }
func (v Value) Int64() int64 { i, _ := strconv.ParseInt(v.String(), 10, 64); return i }
func (v Value) Bool() bool   { return v.String() == "1" || v.String() == "true" }

func Open(path string) (*DB, error) {
	if path == "" {
		return nil, errors.New("sqlite path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	var p *C.sqlite3
	if rc := C.sqlite3_open(cpath, &p); rc != C.SQLITE_OK {
		msg := "sqlite open failed"
		if p != nil {
			msg = C.GoString(C.sqlite3_errmsg(p))
			C.sqlite3_close(p)
		}
		return nil, errors.New(msg)
	}
	db := &DB{ptr: p, path: path}
	if err := db.Exec("PRAGMA busy_timeout = 5000"); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := db.Exec("PRAGMA journal_mode = WAL"); err != nil { /* best effort */
	}
	return db, nil
}

func (d *DB) Path() string { return d.path }

func (d *DB) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ptr == nil {
		return nil
	}
	rc := closeSQLite(d)
	if rc != int(C.SQLITE_OK) {
		return fmt.Errorf("sqlite close rc=%d", rc)
	}
	d.ptr = nil
	return nil
}

func (d *DB) lastErr() error {
	if d.ptr == nil {
		return errors.New("sqlite database is closed")
	}
	return errors.New(C.GoString(C.sqlite3_errmsg(d.ptr)))
}

func (d *DB) ExecScript(sql string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ptr == nil {
		return errors.New("sqlite database is closed")
	}
	csql := C.CString(sql)
	defer C.free(unsafe.Pointer(csql))
	var errmsg *C.char
	rc := C.sqlite3_exec(d.ptr, csql, nil, nil, &errmsg)
	if rc != C.SQLITE_OK {
		var msg string
		if errmsg != nil {
			msg = C.GoString(errmsg)
			C.sqlite3_free(unsafe.Pointer(errmsg))
		} else {
			msg = C.GoString(C.sqlite3_errmsg(d.ptr))
		}
		return errors.New(msg)
	}
	return nil
}

func (d *DB) Exec(sql string, args ...any) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.execLocked(sql, args...)
}

func (d *DB) execLocked(sql string, args ...any) error {
	stmt, err := d.prepareLocked(sql, args...)
	if err != nil {
		return err
	}
	defer C.sqlite3_finalize(stmt)
	rc := C.sqlite3_step(stmt)
	if rc != C.SQLITE_DONE && rc != C.SQLITE_ROW {
		return d.lastErr()
	}
	return nil
}

func (d *DB) Query(sql string, args ...any) ([]map[string]Value, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.queryLocked(sql, args...)
}

func (d *DB) queryLocked(sql string, args ...any) ([]map[string]Value, error) {
	stmt, err := d.prepareLocked(sql, args...)
	if err != nil {
		return nil, err
	}
	defer C.sqlite3_finalize(stmt)
	var rows []map[string]Value
	for {
		rc := C.sqlite3_step(stmt)
		if rc == C.SQLITE_DONE {
			break
		}
		if rc != C.SQLITE_ROW {
			return nil, d.lastErr()
		}
		n := int(C.sqlite3_column_count(stmt))
		row := make(map[string]Value, n)
		for i := 0; i < n; i++ {
			name := C.GoString(C.sqlite3_column_name(stmt, C.int(i)))
			typ := C.sqlite3_column_type(stmt, C.int(i))
			if typ == C.SQLITE_NULL {
				row[name] = Value{Null: true}
			} else {
				txt := C.sqlite3_column_text(stmt, C.int(i))
				row[name] = Value{Text: C.GoString((*C.char)(unsafe.Pointer(txt)))}
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (d *DB) WithTx(fn func(*Tx) error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.execLocked(`BEGIN IMMEDIATE`); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = d.execLocked(`ROLLBACK`)
		}
	}()
	tx := &Tx{db: d}
	if err := fn(tx); err != nil {
		return err
	}
	if err := d.execLocked(`COMMIT`); err != nil {
		return err
	}
	committed = true
	return nil
}

func (tx *Tx) Exec(sql string, args ...any) error {
	return tx.db.execLocked(sql, args...)
}

func (tx *Tx) Query(sql string, args ...any) ([]map[string]Value, error) {
	return tx.db.queryLocked(sql, args...)
}

func (tx *Tx) QueryOne(sql string, args ...any) (map[string]Value, error) {
	rows, err := tx.Query(sql, args...)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, os.ErrNotExist
	}
	return rows[0], nil
}

func (d *DB) QueryOne(sql string, args ...any) (map[string]Value, error) {
	rows, err := d.Query(sql, args...)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, os.ErrNotExist
	}
	return rows[0], nil
}

func (d *DB) prepareLocked(sql string, args ...any) (*C.sqlite3_stmt, error) {
	if d.ptr == nil {
		return nil, errors.New("sqlite database is closed")
	}
	csql := C.CString(sql)
	defer C.free(unsafe.Pointer(csql))
	var stmt *C.sqlite3_stmt
	rc := C.sqlite3_prepare_v2(d.ptr, csql, -1, &stmt, nil)
	if rc != C.SQLITE_OK {
		return nil, d.lastErr()
	}
	for i, arg := range args {
		idx := C.int(i + 1)
		var brc C.int
		switch v := arg.(type) {
		case nil:
			brc = C.sqlite3_bind_null(stmt, idx)
		case string:
			cs := C.CString(v)
			brc = C.bind_text_transient(stmt, idx, cs)
			C.free(unsafe.Pointer(cs))
		case *string:
			if v == nil {
				brc = C.sqlite3_bind_null(stmt, idx)
			} else {
				cs := C.CString(*v)
				brc = C.bind_text_transient(stmt, idx, cs)
				C.free(unsafe.Pointer(cs))
			}
		case bool:
			if v {
				brc = C.sqlite3_bind_int64(stmt, idx, 1)
			} else {
				brc = C.sqlite3_bind_int64(stmt, idx, 0)
			}
		case int:
			brc = C.sqlite3_bind_int64(stmt, idx, C.sqlite3_int64(v))
		case int64:
			brc = C.sqlite3_bind_int64(stmt, idx, C.sqlite3_int64(v))
		case float64:
			brc = C.sqlite3_bind_double(stmt, idx, C.double(v))
		default:
			cs := C.CString(fmt.Sprint(v))
			brc = C.bind_text_transient(stmt, idx, cs)
			C.free(unsafe.Pointer(cs))
		}
		if brc != C.SQLITE_OK {
			C.sqlite3_finalize(stmt)
			return nil, d.lastErr()
		}
	}
	return stmt, nil
}

func AppDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = os.TempDir()
	}
	return filepath.Join(home, ".symphony", "app.db")
}

func RuntimeDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = os.TempDir()
	}
	return filepath.Join(home, ".symphony", "runtime")
}

func GlobalWorkspaceRoot() string {
	if v := os.Getenv("SYMPHONY_WORKSPACE_ROOT"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = os.TempDir()
	}
	return filepath.Join(home, ".symphony", "workspaces")
}
