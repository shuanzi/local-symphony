package db

import (
	"testing"
	"time"
)

func TestWithTxHoldsConnectionLockUntilCommit(t *testing.T) {
	d, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()
	if err := d.Exec(`CREATE TABLE items(id INTEGER PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	txDone := make(chan error, 1)
	go func() {
		txDone <- d.WithTx(func(tx *Tx) error {
			if err := tx.Exec(`INSERT INTO items(name) VALUES(?)`, "tx"); err != nil {
				return err
			}
			close(started)
			<-release
			return nil
		})
	}()
	<-started

	execDone := make(chan error, 1)
	go func() {
		execDone <- d.Exec(`INSERT INTO items(name) VALUES(?)`, "outside")
	}()

	select {
	case err := <-execDone:
		t.Fatalf("Exec completed while transaction callback was still open: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	if err := <-txDone; err != nil {
		t.Fatalf("WithTx: %v", err)
	}
	select {
	case err := <-execDone:
		if err != nil {
			t.Fatalf("Exec after transaction release: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Exec did not complete after transaction release")
	}

	row, err := d.QueryOne(`SELECT COUNT(*) AS c FROM items`)
	if err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if got := row["c"].Int(); got != 2 {
		t.Fatalf("row count = %d, want 2", got)
	}
}

func TestBusyTimeoutWaitsForConcurrentWriter(t *testing.T) {
	path := t.TempDir() + "/test.db"
	writer, err := Open(path)
	if err != nil {
		t.Fatalf("Open writer: %v", err)
	}
	defer writer.Close()
	waiter, err := Open(path)
	if err != nil {
		t.Fatalf("Open waiter: %v", err)
	}
	defer waiter.Close()
	if err := writer.Exec(`CREATE TABLE items(id INTEGER PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	txDone := make(chan error, 1)
	go func() {
		txDone <- writer.WithTx(func(tx *Tx) error {
			if err := tx.Exec(`INSERT INTO items(name) VALUES(?)`, "writer"); err != nil {
				return err
			}
			close(started)
			<-release
			return nil
		})
	}()
	<-started

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- waiter.Exec(`INSERT INTO items(name) VALUES(?)`, "waiter")
	}()

	select {
	case err := <-waitDone:
		t.Fatalf("concurrent writer completed before lock release: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	if err := <-txDone; err != nil {
		t.Fatalf("WithTx: %v", err)
	}
	select {
	case err := <-waitDone:
		if err != nil {
			t.Fatalf("concurrent writer after lock release: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("concurrent writer did not complete after lock release")
	}
}
