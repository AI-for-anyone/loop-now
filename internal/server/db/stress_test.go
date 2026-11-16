package db

// Concurrent write stress tests: they guard against the class of bugs where "the driver reports success but the
// write is not there". Skipped with -short; LOOP_NOW_DB_STRESS=long scales them up (run before a release or when
// upgrading modernc).

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func stressScale(t *testing.T, normal, long int) int {
	t.Helper()
	if testing.Short() {
		t.Skip("stress tests are skipped with -short")
	}
	if os.Getenv("LOOP_NOW_DB_STRESS") == "long" {
		return long
	}
	return normal
}

func integrityOK(t *testing.T, d *DB) {
	t.Helper()
	for _, schema := range []string{"main", EventsSchema} {
		var res string
		if err := d.QueryRow(context.Background(), "PRAGMA "+schema+".integrity_check").Scan(&res); err != nil || res != "ok" {
			t.Fatalf("%s integrity_check: %q %v", schema, res, err)
		}
	}
}

// TestStressConcurrentWrites runs several writers at once doing "read-modify-write a counter + insert a unique key +
// claim a queue item", while readers check in their snapshot that the counter matches the row count, and another
// goroutine keeps running PASSIVE/TRUNCATE checkpoints. Afterwards every confirmed transaction must be in the
// database in full.
func TestStressConcurrentWrites(t *testing.T) {
	perWriter := stressScale(t, 150, 2000)
	const writers, readers = 8, 4
	dir := t.TempDir()
	d := openTestDir(t, dir)
	ctx := context.Background()
	mustExec(t, d, `CREATE TABLE counter (id INTEGER PRIMARY KEY, n INTEGER NOT NULL) STRICT`)
	mustExec(t, d, `INSERT INTO counter VALUES (1, 0)`)
	mustExec(t, d, `CREATE TABLE keys (k INTEGER PRIMARY KEY, writer INTEGER NOT NULL) STRICT`)
	mustExec(t, d, `CREATE TABLE queue (id INTEGER PRIMARY KEY, claimed_by INTEGER) STRICT`)
	mustExec(t, d, `CREATE TABLE ev.log (k INTEGER NOT NULL) STRICT`)
	total := writers * perWriter
	if err := d.InTx(ctx, func(tx *Tx) error {
		for i := 1; i <= total; i++ {
			if _, err := tx.Exec(ctx, `INSERT INTO queue (id) VALUES (?)`, i); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	var done atomic.Bool
	var crossLag atomic.Int64
	var bg sync.WaitGroup
	errs := make(chan error, 64)
	for range readers {
		bg.Go(func() {
			last := -1
			for !done.Load() {
				err := d.ReadTx(ctx, func(tx *Tx) error {
					var n, keys, logs int
					if err := tx.QueryRow(ctx, `SELECT (SELECT n FROM counter), (SELECT count(*) FROM keys), (SELECT count(*) FROM ev.log)`).Scan(&n, &keys, &logs); err != nil {
						return err
					}
					if n != keys {
						return fmt.Errorf("inconsistent snapshot within one database: counter=%d keys=%d", n, keys)
					}
					// There is no consistency across files: under WAL a write transaction commits data.db and
					// events.db separately, and a reader takes its snapshots of the two files one after the other,
					// so under heavy writes the two sides can differ by several transactions (dozens measured on
					// Linux). Only the largest difference is recorded here, not treated as an error; once the run
					// ends the two sides must be equal. Business decisions must not rely on reads across files.
					if lag := int64(max(keys-logs, logs-keys)); lag > crossLag.Load() {
						crossLag.Store(lag)
					}
					if n < last {
						return fmt.Errorf("the counter went backwards: %d → %d", last, n)
					}
					last = n
					return nil
				})
				if err != nil {
					errs <- err
					return
				}
			}
		})
	}
	bg.Go(func() {
		for i := 0; !done.Load(); i++ {
			mode := CheckpointPassive
			if i%10 == 9 {
				mode = CheckpointTruncate
			}
			if _, err := d.Checkpoint(ctx, mode); err != nil {
				errs <- err
				return
			}
			time.Sleep(time.Millisecond)
		}
	})

	claimedBy := make([][]int, writers)
	var wg sync.WaitGroup
	for w := range writers {
		wg.Go(func() {
			for i := range perWriter {
				var claimed int
				err := d.InTx(ctx, func(tx *Tx) error {
					var n int
					if err := tx.QueryRow(ctx, `SELECT n FROM counter WHERE id = 1`).Scan(&n); err != nil {
						return err
					}
					if _, err := tx.Exec(ctx, `UPDATE counter SET n = ? WHERE id = 1`, n+1); err != nil {
						return err
					}
					k := w*perWriter + i
					if _, err := tx.Exec(ctx, `INSERT INTO keys VALUES (?, ?)`, k, w); err != nil {
						return err
					}
					if _, err := tx.Exec(ctx, `INSERT INTO ev.log VALUES (?)`, k); err != nil {
						return err
					}
					return tx.QueryRow(ctx, `UPDATE queue SET claimed_by = ? WHERE id = (SELECT id FROM queue WHERE claimed_by IS NULL ORDER BY id LIMIT 1) RETURNING id`, w).Scan(&claimed)
				})
				if err != nil {
					errs <- fmt.Errorf("writer %d, write %d: %w", w, i, err)
					return
				}
				claimedBy[w] = append(claimedBy[w], claimed)
			}
		})
	}
	wg.Wait()
	done.Store(true)
	bg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	check := func(d *DB) {
		var n, keys, logs, claimed int
		if err := d.QueryRow(ctx, `SELECT (SELECT n FROM counter), (SELECT count(*) FROM keys), (SELECT count(*) FROM ev.log), (SELECT count(*) FROM queue WHERE claimed_by IS NOT NULL)`).Scan(&n, &keys, &logs, &claimed); err != nil {
			t.Fatal(err)
		}
		if n != total || keys != total || logs != total || claimed != total {
			t.Fatalf("%d transactions confirmed, the database has counter=%d keys=%d log=%d claimed=%d", total, n, keys, logs, claimed)
		}
		seen := map[int]bool{}
		for w, ids := range claimedBy {
			for _, id := range ids {
				var by int
				if err := d.QueryRow(ctx, `SELECT claimed_by FROM queue WHERE id = ?`, id).Scan(&by); err != nil || by != w || seen[id] {
					t.Fatalf("queue item %d: the database has claimed_by=%d, confirmed to writer %d, duplicate=%v, err=%v", id, by, w, seen[id], err)
				}
				seen[id] = true
			}
		}
		integrityOK(t, d)
	}
	check(d)
	t.Logf("readers saw data.db and events.db differ by at most %d transactions (no consistent snapshot across files)", crossLag.Load())
	// Close, reopen and check again: the confirmed commits are in the files.
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	check(openTestDir(t, dir))
}

// TestStressReadYourWrites checks that once a commit on the write connection returns, the next query on the
// read-only pool must see it; a result set that is never closed does not affect other read connections.
func TestStressReadYourWrites(t *testing.T) {
	perWriter := stressScale(t, 300, 5000)
	d := openTest(t)
	ctx := context.Background()
	mustExec(t, d, `CREATE TABLE t (id INTEGER PRIMARY KEY) STRICT`)
	mustExec(t, d, `INSERT INTO t VALUES (0)`)
	leaked, err := d.Query(ctx, `SELECT id FROM t`)
	if err != nil {
		t.Fatal(err)
	}
	defer leaked.Close()
	if !leaked.Next() {
		t.Fatal("expected one row")
	}
	var next atomic.Int64
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 4 {
		wg.Go(func() {
			for range perWriter {
				id := next.Add(1)
				if _, err := d.Exec(ctx, `INSERT INTO t VALUES (?)`, id); err != nil {
					errs <- err
					return
				}
				var n int
				if err := d.QueryRow(ctx, `SELECT count(*) FROM t WHERE id = ?`, id).Scan(&n); err != nil || n != 1 {
					errs <- fmt.Errorf("cannot read %d after its commit: n=%d err=%v", id, n, err)
					return
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

// TestStressCancellation cancels at random moments: a transaction that returned nil must be in the database, and one
// that returned an error must not. There is no "outcome unknown".
func TestStressCancellation(t *testing.T) {
	perWriter := stressScale(t, 200, 3000)
	const writers = 8
	d := openTest(t)
	ctx := context.Background()
	mustExec(t, d, `CREATE TABLE t (id INTEGER PRIMARY KEY, w INTEGER NOT NULL) STRICT`)
	mustExec(t, d, `CREATE TABLE ev.t (id INTEGER PRIMARY KEY) STRICT`)
	// Timeouts are picked between 0 and 3 times "roughly how long one transaction takes", so cancellations land
	// everywhere: while waiting for the write connection, mid-statement, and just before and after commit. The
	// duration is measured on the current machine, so even under -race there are both confirmed and cancelled
	// transactions.
	warm := time.Now()
	for i := range 20 {
		mustExec(t, d, `INSERT INTO t VALUES (?, -1)`, -1-i)
	}
	window := 3 * writers * time.Since(warm) / 20
	mustExec(t, d, `DELETE FROM t`)
	acked := make([]map[int]bool, writers)
	var cancelled atomic.Int64
	var wg sync.WaitGroup
	for w := range writers {
		acked[w] = map[int]bool{}
		wg.Go(func() {
			r := rand.New(rand.NewPCG(uint64(w), 7))
			for i := range perWriter {
				id := w*perWriter + i
				c, cancel := context.WithTimeout(ctx, time.Duration(r.Int64N(int64(window))))
				err := d.InTx(c, func(tx *Tx) error {
					if _, err := tx.Exec(c, `INSERT INTO t VALUES (?, ?)`, id, w); err != nil {
						return err
					}
					for j := range 5 {
						if _, err := tx.Exec(c, `UPDATE t SET w = ? WHERE id = ?`, w+j, id); err != nil {
							return err
						}
					}
					_, err := tx.Exec(c, `INSERT INTO ev.t VALUES (?)`, id)
					return err
				})
				cancel()
				if err == nil {
					acked[w][id] = true
				} else {
					cancelled.Add(1)
				}
			}
		})
	}
	wg.Wait()
	rows, err := d.Query(ctx, `SELECT t.id, t.w, e.id IS NOT NULL FROM t LEFT JOIN ev.t e ON e.id = t.id`)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	var id, wv int
	var both bool
	if _, err := ForEachRow(rows, []any{&id, &wv, &both}, func() error {
		w := id / perWriter
		if !acked[w][id] {
			return fmt.Errorf("unconfirmed transaction %d is in the database", id)
		}
		if !both || wv != w+4 {
			return fmt.Errorf("transaction %d is incomplete: ev=%v w=%d", id, both, wv)
		}
		found++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ackedN := 0
	for _, m := range acked {
		ackedN += len(m)
	}
	if found != ackedN {
		t.Fatalf("%d confirmed, %d in the database", ackedN, found)
	}
	t.Logf("%d confirmed, %d cancelled", ackedN, cancelled.Load())
	if cancelled.Load() == 0 || ackedN == 0 {
		t.Errorf("both cancellations and confirmations should occur for the test to prove anything")
	}
	integrityOK(t, d)
}

const killChildDir = "LOOP_NOW_DB_KILL_CHILD_DIR"

// TestStressKill: a child process keeps writing and prints a number after each successful commit; the parent
// SIGKILLs it at a random moment, then opens the database and checks that not a single printed number is missing.
// With WAL plus synchronous=NORMAL a process crash does not lose committed transactions (only a power loss may lose
// the last few).
func TestStressKill(t *testing.T) {
	rounds := stressScale(t, 10, 200)
	dir := t.TempDir()
	setup := openTestDir(t, dir)
	mustExec(t, setup, `CREATE TABLE t (id INTEGER PRIMARY KEY, pad TEXT NOT NULL) STRICT`)
	mustExec(t, setup, `CREATE TABLE ev.t (id INTEGER PRIMARY KEY) STRICT`)
	if err := setup.Close(); err != nil {
		t.Fatal(err)
	}
	printed := map[int]bool{}
	for round := range rounds {
		cmd := exec.Command(os.Args[0], "-test.run=^TestStressKillChild$", "-test.count=1")
		cmd.Env = append(os.Environ(), killChildDir+"="+dir, "LOOP_NOW_DB_KILL_START="+strconv.Itoa(round*1_000_000))
		out, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		lines := make(chan int, 1024)
		readDone := make(chan struct{})
		go func() {
			defer close(readDone)
			sc := bufio.NewScanner(out)
			for sc.Scan() {
				if v, ok := strings.CutPrefix(sc.Text(), "committed "); ok {
					if n, err := strconv.Atoi(v); err == nil {
						lines <- n
					}
				}
			}
		}()
		killAfter := time.Duration(20+rand.IntN(150)) * time.Millisecond
		timer := time.NewTimer(killAfter + 5*time.Second)
		first := true
	collect:
		for {
			select {
			case n := <-lines:
				printed[n] = true
				if first {
					first = false
					timer.Reset(killAfter)
				}
			case <-timer.C:
				break collect
			}
		}
		_ = cmd.Process.Kill()
		<-readDone
		for len(lines) > 0 {
			printed[<-lines] = true
		}
		_ = cmd.Wait()
		if first {
			t.Fatalf("round %d: the child process committed no transactions", round)
		}
	}
	d := openTestDir(t, dir)
	ctx := context.Background()
	for n := range printed {
		var main, ev int
		if err := d.QueryRow(ctx, `SELECT (SELECT count(*) FROM t WHERE id = ?), (SELECT count(*) FROM ev.t WHERE id = ?)`, n, n).Scan(&main, &ev); err != nil {
			t.Fatal(err)
		}
		if main != 1 || ev != 1 {
			t.Fatalf("the child confirmed committing %d, after reopening data=%d events=%d", n, main, ev)
		}
	}
	integrityOK(t, d)
	t.Logf("%d rounds, %d confirmed commits checked", rounds, len(printed))
}

// TestStressKillChild only runs in the child process started by TestStressKill.
func TestStressKillChild(t *testing.T) {
	dir := os.Getenv(killChildDir)
	if dir == "" {
		t.Skip("only runs in the child process of TestStressKill")
	}
	start, _ := strconv.Atoi(os.Getenv("LOOP_NOW_DB_KILL_START"))
	d, err := Open(context.Background(), Options{Dir: dir})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	ctx := context.Background()
	go func() {
		for {
			_, _ = d.Checkpoint(ctx, CheckpointPassive)
			time.Sleep(2 * time.Millisecond)
		}
	}()
	pad := strings.Repeat("x", 512)
	for i := start; ; i++ {
		err := d.InTx(ctx, func(tx *Tx) error {
			if _, err := tx.Exec(ctx, `INSERT INTO t VALUES (?, ?)`, i, pad); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO ev.t VALUES (?)`, i)
			return err
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(3)
		}
		fmt.Printf("committed %d\n", i)
	}
}

// TestStressCheckpointWithLongReader checks that a TRUNCATE checkpoint cannot finish while a long reader holds an old
// snapshot, and that it truncates the WAL to 0 once the reader is done.
func TestStressCheckpointWithLongReader(t *testing.T) {
	rows := stressScale(t, 20000, 200000)
	d := openTest(t)
	ctx := context.Background()
	mustExec(t, d, `CREATE TABLE t (id INTEGER PRIMARY KEY, pad TEXT NOT NULL) STRICT`)
	insert := func(from int) {
		if err := d.InTx(ctx, func(tx *Tx) error {
			for i := from; i < from+rows; i++ {
				if _, err := tx.Exec(ctx, `INSERT INTO t VALUES (?, 'padding-padding-padding')`, i); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	insert(0)
	release := make(chan struct{})
	reading := make(chan struct{})
	readerDone := make(chan error, 1)
	go func() {
		readerDone <- d.ReadTx(ctx, func(tx *Tx) error {
			var n int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM t`).Scan(&n); err != nil {
				return err
			}
			close(reading)
			<-release
			var again int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM t`).Scan(&again); err != nil {
				return err
			}
			if again != n {
				return fmt.Errorf("the long reader's snapshot changed: %d → %d", n, again)
			}
			return nil
		})
	}()
	<-reading
	insert(rows)
	res, err := d.Checkpoint(ctx, CheckpointTruncate)
	if err != nil {
		t.Fatal(err)
	}
	if !res[0].Busy {
		t.Errorf("TRUNCATE should not complete while a long reader is active: %+v", res[0])
	}
	if data, _ := d.WALSize(); data == 0 {
		t.Error("the WAL should not be truncated while a long reader is active")
	}
	close(release)
	if err := <-readerDone; err != nil {
		t.Fatal(err)
	}
	res, err = d.Checkpoint(ctx, CheckpointTruncate)
	if err != nil || res[0].Busy {
		t.Fatalf("TRUNCATE should complete after the reader is done: %+v %v", res, err)
	}
	if data, _ := d.WALSize(); data != 0 {
		t.Fatalf("the WAL should be 0 after TRUNCATE, got %d", data)
	}
	if err := errors.Join(d.Optimize(ctx)); err != nil {
		t.Fatal(err)
	}
	integrityOK(t, d)
}
