package jobs_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/jobs"

	_ "modernc.org/sqlite"
)

// --- hooking driver -------------------------------------------------------
//
// hookDriver wraps the modernc sqlite driver and invokes a callback the first
// time a query whose text contains a registered marker is executed. The
// callback runs synchronously inside the query, so a test can deterministically
// commit a competing write in the window between AnswerClarification's status
// SELECT and its UPDATE — exactly the TOCTOU window the fix closes by wrapping
// both statements in a single transaction.

type hookDriver struct {
	base driver.Driver
	mu   sync.Mutex
	hook func(query string)
}

func (d *hookDriver) setHook(h func(string)) {
	d.mu.Lock()
	d.hook = h
	d.mu.Unlock()
}

func (d *hookDriver) fire(query string) {
	d.mu.Lock()
	h := d.hook
	d.mu.Unlock()
	if h != nil {
		h(query)
	}
}

func (d *hookDriver) Open(name string) (driver.Conn, error) {
	c, err := d.base.Open(name)
	if err != nil {
		return nil, err
	}
	return &hookConn{Conn: c, d: d}, nil
}

type hookConn struct {
	driver.Conn
	d *hookDriver
}

func (c *hookConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	qc, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	rows, err := qc.QueryContext(ctx, query, args)
	// Fire AFTER the SELECT has read its snapshot but BEFORE the caller proceeds
	// to its UPDATE — this is the lost-update window in the non-transactional
	// implementation. When the same SELECT runs inside a transaction (the fix),
	// the underlying write lock is already held, so a competing write cannot
	// commit during the hook.
	c.d.fire(query)
	return rows, err
}

func (c *hookConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	ec, ok := c.Conn.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	// Fire BEFORE the UPDATE executes so a test can interpose between the
	// preceding status SELECT and this write.
	c.d.fire(query)
	return ec.ExecContext(ctx, query, args)
}

func (c *hookConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	bt, ok := c.Conn.(driver.ConnBeginTx)
	if !ok {
		return c.Conn.Begin() //nolint:staticcheck
	}
	return bt.BeginTx(ctx, opts)
}

func (c *hookConn) Prepare(query string) (driver.Stmt, error) { return c.Conn.Prepare(query) }

func newHookedDB(t *testing.T) (*sql.DB, *hookDriver) {
	t.Helper()
	base, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("probe open: %v", err)
	}
	d := base.Driver()
	base.Close()

	hd := &hookDriver{base: d}
	dsn := fmt.Sprintf("file:hookrace-%d?mode=memory&cache=shared", time.Now().UnixNano())
	connector := &hookConnector{driver: hd, dsn: dsn}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(4)
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		t.Fatalf("busy_timeout: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db, hd
}

type hookConnector struct {
	driver *hookDriver
	dsn    string
}

func (c *hookConnector) Connect(_ context.Context) (driver.Conn, error) {
	return c.driver.Open(c.dsn)
}
func (c *hookConnector) Driver() driver.Driver { return c.driver }

// TestAnswerClarification_ConcurrentCancelRace exercises the check+UPDATE race
// in AnswerClarification. A job is awaiting_input; one goroutine answers the
// clarification (which flips status awaiting_input -> running) while another
// goroutine flips the same row to a terminal "cancelled" state via a
// serializable transaction (the way an out-of-band cancel would).
//
// Invariant: once a competing writer has committed the row to "cancelled",
// AnswerClarification must NOT silently clobber that terminal status back to
// "running". With the non-transactional implementation, AnswerClarification
// reads status (sees awaiting_input) on one statement, releases the connection,
// the cancel commits "cancelled", and then AnswerClarification's UPDATE
// overwrites it with "running" — a lost update. Wrapping check+UPDATE in a
// serializable transaction prevents the interleaving: AnswerClarification
// either commits before the cancel (then sees awaiting_input legitimately) or
// observes "cancelled" in its in-transaction SELECT and refuses.
func TestAnswerClarification_ConcurrentCancelRace(t *testing.T) {
	db, hd := newHookedDB(t)
	js, err := jobs.NewJobStore(db)
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}
	ctx := context.Background()

	id := "job-answer-race"
	j := makeTestJob(id, jobs.JobRunning)
	if err := js.UpsertJob(ctx, j); err != nil {
		t.Fatalf("UpsertJob: %v", err)
	}
	if err := js.RequestClarification(ctx, id, jobs.ClarificationSchema{
		Fields: map[string]string{"q": "string"},
		Prompt: "p",
	}); err != nil {
		t.Fatalf("RequestClarification: %v", err)
	}

	// Hook: the first SELECT against the jobs table that runs the answer's
	// status read commits a competing "cancel" from a separate connection,
	// deterministically landing inside the check->update window.
	//
	//   - Non-transactional impl: the SELECT ran on its own connection and has
	//     already released any lock, so the cancel commits "cancelled"; then the
	//     answer's UPDATE clobbers it back to "running" — a lost update.
	//   - Transactional impl: the SELECT runs inside a BEGIN IMMEDIATE
	//     transaction that already holds the write lock, so the competing
	//     UPDATE blocks until the answer commits, and then matches on the wrong
	//     status (or the answer's in-transaction SELECT already saw the row was
	//     awaiting_input and commits "running" atomically with no lost write).
	var selectOnce, updateOnce sync.Once
	var selectFired, updateFired bool
	cancelLaunched := make(chan struct{})
	cancelCommitted := make(chan struct{})
	hd.setHook(func(query string) {
		switch {
		case strings.Contains(query, "SELECT status FROM jobs"):
			selectOnce.Do(func() {
				selectFired = true
				// Launch the competing cancel on a separate connection. We do NOT
				// block here: the answer's SELECT rows must close (releasing the
				// read lock / connection) before the cancel can grab the write
				// lock in the non-transactional case.
				go func() {
					res, err := db.ExecContext(context.Background(),
						`UPDATE jobs SET status=? WHERE id=? AND status=?`,
						string(jobs.JobCancelled), id, string(jobs.JobAwaitingInput))
					if err == nil {
						if n, _ := res.RowsAffected(); n == 1 {
							close(cancelCommitted)
						}
					}
				}()
				close(cancelLaunched)
			})
		case strings.Contains(query, "clarification_answer="):
			// This is the answer's UPDATE — the second half of the check->update
			// sequence. Gate it on the competing cancel so the interleaving is
			// deterministic.
			//   - Unfixed (no txn): the SELECT already released its lock, the
			//     cancel commits "cancelled", we unblock, and this UPDATE then
			//     clobbers it back to "running" — a lost update.
			//   - Fixed (txn): the SELECT holds the write lock for the whole
			//     transaction, so the cancel cannot commit; we time out and let
			//     the answer commit atomically. The cancel only lands afterwards
			//     (over "running"), so no answer write is lost.
			updateOnce.Do(func() {
				updateFired = true
				select {
				case <-cancelCommitted:
				case <-time.After(750 * time.Millisecond):
				}
			})
		}
	})

	answerErr := js.AnswerClarification(ctx, id, "answered")

	if !selectFired || !updateFired {
		t.Fatalf("hooks did not fire as expected (select=%v update=%v)", selectFired, updateFired)
	}
	<-cancelLaunched
	// Let any still-blocked competing cancel settle before reading final state.
	select {
	case <-cancelCommitted:
	case <-time.After(2 * time.Second):
	}
	committed := false
	select {
	case <-cancelCommitted:
		committed = true
	default:
	}

	var status string
	if err := db.QueryRowContext(ctx, `SELECT status FROM jobs WHERE id=?`, id).Scan(&status); err != nil {
		t.Fatalf("scan final status: %v", err)
	}

	// When a cancel committed "cancelled" during the check->update window, that
	// terminal status must survive. The lost-update bug is: cancel committed,
	// yet AnswerClarification reported success and clobbered the row to
	// "running".
	if committed && answerErr == nil && status == string(jobs.JobRunning) {
		t.Fatalf("lost update: cancel committed during the check->update window but "+
			"AnswerClarification reported success and clobbered status to %q", status)
	}
}

// TestSubscribe_TOCTOU_NoMissedTerminalEvent exercises the race between
// Subscribe's status check and its channel registration. A handler is held
// until the test releases it, then the test calls Subscribe concurrently with
// the handler returning (which flips the job terminal and fans out). Either
// outcome is acceptable as long as the subscriber is never left silently
// stranded: a Subscribe call must return EITHER a channel that delivers a
// terminal event, OR (if it observed the job already terminal) a closed channel
// carrying one event. The bug is the third outcome — Subscribe registered its
// channel AFTER the terminal fanout had already run, so the channel neither
// receives the event nor is closed, and a consumer draining it blocks forever.
//
// With the non-atomic implementation, the status read releases the RLock before
// registration, so a terminal transition can land in that gap. Holding s.mu
// across check+register (the fix) closes the window. Run with -race to also
// catch the unsynchronised status read.
func TestSubscribe_TOCTOU_NoMissedTerminalEvent(t *testing.T) {
	const iters = 300
	for i := 0; i < iters; i++ {
		sched := jobs.NewInMemoryScheduler()

		release := make(chan struct{})
		handler := func(ctx context.Context, args map[string]any) (host.Result, error) {
			<-release
			return host.Result{Data: map[string]any{"ok": true}}, nil
		}

		id, err := sched.Submit(context.Background(), jobs.JobSpec{
			Kind:    "host.toctou",
			Handler: handler,
		})
		if err != nil {
			t.Fatalf("iter %d: submit: %v", i, err)
		}

		// Start a subscriber and the terminal transition as concurrently as
		// possible to maximise the chance of hitting the check->register gap.
		var wg sync.WaitGroup
		wg.Add(2)

		var ch <-chan jobs.JobEvent
		var unsub func()
		subReady := make(chan struct{})
		go func() {
			defer wg.Done()
			ch, unsub = sched.Subscribe(id)
			close(subReady)
		}()
		go func() {
			defer wg.Done()
			close(release) // let the handler return and fan out the terminal event
		}()

		<-subReady
		wg.Wait()

		// The subscriber must observe exactly one terminal event within a
		// generous deadline. If the registration landed after the fanout, the
		// channel stays open and empty and this times out.
		select {
		case ev, ok := <-ch:
			if !ok {
				// Closed channel from the already-terminal fast path is fine, but
				// then it should have carried the event first; a bare close with
				// no event would mean a missed terminal event.
				t.Fatalf("iter %d: channel closed with no terminal event delivered", i)
			}
			if ev.Status != jobs.JobDone {
				t.Fatalf("iter %d: expected JobDone, got %s", i, ev.Status)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("iter %d: subscriber never received terminal event "+
				"(registered after fanout — missed event)", i)
		}
		unsub()
	}
}

// TestWaitSessionDrained_CancelDoesNotLeak verifies that cancelling the context
// while WaitSessionDrained is blocked on a still-pending subscriber returns
// promptly and does not leak the internal cond goroutine.
//
// We make pending > 0 by fanning out an event to a session subscriber and never
// acking it. WaitSessionDrained then parks on the cond. We cancel ctx and assert
// the call returns ctx.Err() AND that no goroutine remains parked (goroutine
// count returns to baseline). Without the cancel-flag fix, the cond goroutine
// re-checks pending (still > 0), re-parks, and leaks.
func TestWaitSessionDrained_CancelDoesNotLeak(t *testing.T) {
	sched := jobs.NewInMemoryScheduler()
	sid := app.SessionID("sess-drain-leak")

	// Subscribe but never ack, so pending stays > 0 after a fanout.
	_, _, unsub := sched.SubscribeSession(sid)
	defer unsub()

	// Submit a job; its terminal event fans out to the session subscriber,
	// incrementing pending. We deliberately do not drain/ack the channel.
	if _, err := sched.Submit(context.Background(), jobs.JobSpec{
		SessionID: sid,
		Kind:      "host.test",
		Handler:   echoHandler("x"),
	}); err != nil {
		t.Fatalf("submit: %v", err)
	}

	// Wait until pending has actually been incremented (the fanout happened).
	deadline := time.After(2 * time.Second)
	for {
		// WaitSessionDrained with an already-cancelled ctx would race the fanout;
		// instead poll a short blocking attempt.
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		err := sched.WaitSessionDrained(ctx, sid)
		cancel()
		if err != nil {
			// Timed out because pending > 0 — the fanout has landed.
			break
		}
		select {
		case <-deadline:
			t.Fatal("fanout never incremented pending (drain returned nil)")
		default:
		}
	}

	baseline := runtime.NumGoroutine()

	// Now block in WaitSessionDrained and cancel mid-wait.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sched.WaitSessionDrained(ctx, sid) }()

	// Give the call time to park on the cond, then cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected ctx.Err() from cancelled WaitSessionDrained, got nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WaitSessionDrained did not return after ctx cancel (goroutine leaked / blocked)")
	}

	// The internal cond goroutine must have exited too. Allow the scheduler a
	// moment to unwind, then assert goroutine count did not grow.
	for i := 0; i < 50; i++ {
		if runtime.NumGoroutine() <= baseline {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("goroutine count did not return to baseline (%d) — leak suspected, now %d",
		baseline, runtime.NumGoroutine())
}
