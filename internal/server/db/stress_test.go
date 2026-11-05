package db

// 并发写入压测：守住“驱动报告成功、写入却不在”这一类问题。-short 时跳过；
// LOOP_NOW_DB_STRESS=long 时加大规模（发版前或升级 modernc 时跑）。

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
		t.Skip("压测在 -short 下跳过")
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

// 多个写者同时做“读改写计数器 + 插唯一键 + 认领队列项”，读者在快照里核对计数器与行数一致，
// 另有一个 goroutine 不停做 PASSIVE/TRUNCATE 检查点。结束后每个已确认的事务都必须完整在库里。
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
						return fmt.Errorf("同一库内快照不一致：counter=%d keys=%d", n, keys)
					}
					// 跨文件没有一致性：WAL 下写事务对 data.db 与 events.db 分别提交，读者对两个文件的快照也是
					// 先后各取一次，写入频繁时两边可能差若干个事务（Linux 上实测到几十个）。这里只记录最大差值，
					// 不当作错误；结束后两边必须相等。业务判断不能依赖跨文件读取。
					if lag := int64(max(keys-logs, logs-keys)); lag > crossLag.Load() {
						crossLag.Store(lag)
					}
					if n < last {
						return fmt.Errorf("计数器倒退：%d → %d", last, n)
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
					errs <- fmt.Errorf("写者 %d 第 %d 次：%w", w, i, err)
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
			t.Fatalf("已确认 %d 个事务，库里 counter=%d keys=%d log=%d claimed=%d", total, n, keys, logs, claimed)
		}
		seen := map[int]bool{}
		for w, ids := range claimedBy {
			for _, id := range ids {
				var by int
				if err := d.QueryRow(ctx, `SELECT claimed_by FROM queue WHERE id = ?`, id).Scan(&by); err != nil || by != w || seen[id] {
					t.Fatalf("队列项 %d：库里 claimed_by=%d，确认给写者 %d，重复=%v，err=%v", id, by, w, seen[id], err)
				}
				seen[id] = true
			}
		}
		integrityOK(t, d)
	}
	check(d)
	t.Logf("读者观察到 data.db 与 events.db 最多相差 %d 个事务（跨文件没有一致快照）", crossLag.Load())
	// 关闭后重开再查一遍：已确认的提交在文件里。
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	check(openTestDir(t, dir))
}

// 写连接上的提交返回后，只读池上的下一次查询必须看见它；一个一直没关的结果集不影响其他读连接。
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
		t.Fatal("预期一行")
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
					errs <- fmt.Errorf("提交后读不到 %d：n=%d err=%v", id, n, err)
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

// 随机时刻取消：返回 nil 的事务必须在库里，返回错误的必须不在。没有“结果未知”。
func TestStressCancellation(t *testing.T) {
	perWriter := stressScale(t, 200, 3000)
	const writers = 8
	d := openTest(t)
	ctx := context.Background()
	mustExec(t, d, `CREATE TABLE t (id INTEGER PRIMARY KEY, w INTEGER NOT NULL) STRICT`)
	mustExec(t, d, `CREATE TABLE ev.t (id INTEGER PRIMARY KEY) STRICT`)
	// 超时取在“一个事务大约要多久”的 0–3 倍之间，这样取消会落在等写连接、语句中间和提交前后各个位置；
	// 按本机实测定，-race 下也有确认也有取消。
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
			return fmt.Errorf("未确认的事务 %d 却在库里", id)
		}
		if !both || wv != w+4 {
			return fmt.Errorf("事务 %d 不完整：ev=%v w=%d", id, both, wv)
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
		t.Fatalf("已确认 %d 个，库里 %d 个", ackedN, found)
	}
	t.Logf("确认 %d 个、取消 %d 个", ackedN, cancelled.Load())
	if cancelled.Load() == 0 || ackedN == 0 {
		t.Errorf("取消与确认都应出现，才能说明问题")
	}
	integrityOK(t, d)
}

const killChildDir = "LOOP_NOW_DB_KILL_CHILD_DIR"

// 子进程不停写并在每次提交成功后打印编号，父进程随机 SIGKILL 它，再打开库核对：打印过的编号一个都不能少。
// WAL 加 synchronous=NORMAL 下进程崩溃不丢已提交的事务（只有断电可能丢最后几笔）。
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
			t.Fatalf("第 %d 轮子进程没有提交任何事务", round)
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
			t.Fatalf("子进程确认提交了 %d，重开后 data=%d events=%d", n, main, ev)
		}
	}
	integrityOK(t, d)
	t.Logf("%d 轮，核对 %d 个已确认提交", rounds, len(printed))
}

// TestStressKillChild 只在 TestStressKill 拉起的子进程里运行。
func TestStressKillChild(t *testing.T) {
	dir := os.Getenv(killChildDir)
	if dir == "" {
		t.Skip("只在 TestStressKill 的子进程里运行")
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

// 长读者占着旧快照时 TRUNCATE 检查点做不完；读者结束后能把 WAL 截为 0。
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
				return fmt.Errorf("长读者快照变化：%d → %d", n, again)
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
		t.Errorf("长读者在时 TRUNCATE 应做不完：%+v", res[0])
	}
	if data, _ := d.WALSize(); data == 0 {
		t.Error("长读者在时 WAL 不应被截断")
	}
	close(release)
	if err := <-readerDone; err != nil {
		t.Fatal(err)
	}
	res, err = d.Checkpoint(ctx, CheckpointTruncate)
	if err != nil || res[0].Busy {
		t.Fatalf("读者结束后 TRUNCATE 应完成：%+v %v", res, err)
	}
	if data, _ := d.WALSize(); data != 0 {
		t.Fatalf("TRUNCATE 后 WAL 应为 0，得到 %d", data)
	}
	if err := errors.Join(d.Optimize(ctx)); err != nil {
		t.Fatal(err)
	}
	integrityOK(t, d)
}
