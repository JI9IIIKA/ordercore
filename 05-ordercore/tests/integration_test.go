package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http/httptest"
	p "ordercore/internal/platform"
	"ordercore/migrations"
	"ordercore/projects/ordercore"
	"os"
	"sync"
	"testing"
)

var sharedDB *pgxpool.Pool
var sharedErr error
var dbOnce sync.Once

func TestMain(m *testing.M) {
	code := m.Run()
	if sharedDB != nil {
		sharedDB.Close()
	}
	os.Exit(code)
}

type fixture struct {
	t      *testing.T
	app    *p.App
	db     *pgxpool.Pool
	tenant string
}

func setup(t *testing.T, name string, register func(*p.App)) *fixture {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set: integration test requires PostgreSQL")
	}
	dbOnce.Do(func() { sharedDB, sharedErr = pgxpool.New(context.Background(), url) })
	if sharedErr != nil {
		t.Fatal(sharedErr)
	}
	db := sharedDB
	var e error
	if e = migrations.Apply(context.Background(), db, name); e != nil {
		t.Fatal(e)
	}
	tenant := p.ID()
	tables := map[string][]string{
		"teamflow": {"task_dependencies", "task_history", "tasks"}, "bookslot": {"bookings", "resources"},
		"jobrunner": {"jobs"}, "notifyhub": {"delivery_attempts", "notifications"},
		"ordercore": {"payment_events", "order_items", "orders", "products"}, "eventscope": {"analytics_events"},
		"flagcontrol": {"flag_heads", "flags"}, "uptimewatch": {"checks", "incidents", "monitors"},
		"knowledgedesk": {"document_access", "chunks", "documents"}, "supportflow": {"ticket_audit", "support_replies", "tickets"},
	}
	t.Cleanup(func() {
		for _, table := range append(tables[name], "outbox", "idempotency", "event_receipts") {
			if _, err := db.Exec(context.Background(), "DELETE FROM "+table+" WHERE tenant=$1", tenant); err != nil {
				t.Errorf("cleanup %s: %v", table, err)
			}
		}
	})

	a := p.New(name, db, map[string]p.Principal{"admin-token-123456": {Tenant: tenant, User: "alice", Role: "admin"}, "editor-token-12345": {Tenant: tenant, User: "bob", Role: "editor"}, "viewer-token-12345": {Tenant: tenant, User: "reader", Role: "viewer"}, "other-token-123456": {Tenant: p.ID(), User: "mallory", Role: "admin"}})
	register(a)
	return &fixture{t, a, db, tenant}
}
func (f *fixture) call(method, path, token, key string, body any) (int, map[string]any) {
	f.t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(method, path, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+token)
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	f.app.Router.ServeHTTP(w, req)
	var result map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	return w.Code, result
}
func (f *fixture) ok(method, path string, body any) map[string]any {
	f.t.Helper()
	code, out := f.call(method, path, "admin-token-123456", "", body)
	if code != 200 {
		f.t.Fatalf("%s %s: %d %v", method, path, code, out)
	}
	return out
}
func (f *fixture) sql(q string, args ...any) {
	f.t.Helper()
	if _, e := f.db.Exec(context.Background(), q, args...); e != nil {
		f.t.Fatal(e)
	}
}
func (f *fixture) count(q string, args ...any) int {
	f.t.Helper()
	var n int
	if e := f.db.QueryRow(context.Background(), q, args...).Scan(&n); e != nil {
		f.t.Fatal(e)
	}
	return n
}
func require(t *testing.T, ok bool, msg string) {
	t.Helper()
	if !ok {
		t.Fatal(msg)
	}
}
func TestOrderCore(t *testing.T) {
	f := setup(t, "ordercore", ordercore.Register)
	product := f.ok("POST", "/products", map[string]any{"name": "keyboard", "price_cents": 999, "stock": 1})
	body := map[string]any{"items": []map[string]any{{"product_id": product["id"], "quantity": 1}}}
	code, order := f.call("POST", "/orders", "admin-token-123456", "order-1", body)
	require(t, code == 200, "order failed")
	code, again := f.call("POST", "/orders", "admin-token-123456", "order-1", body)
	require(t, code == 200 && again["id"] == order["id"], "idempotency failed")
	code, _ = f.call("POST", "/orders", "admin-token-123456", "", body)
	require(t, code == 409, "oversold stock")
	path := "/orders/" + order["id"].(string) + "/payment"
	payment := map[string]string{"event_id": "evt-1", "outcome": "success"}
	f.ok("POST", path, payment)
	f.ok("POST", path, payment)
	require(t, f.count(`SELECT count(*) FROM payment_events WHERE tenant=$1`, f.tenant) == 1, "duplicate payment")
	require(t, f.count(`SELECT stock FROM products WHERE tenant=$1`, f.tenant) == 0, "stock changed twice")
}
