package ordercore

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	p "ordercore/internal/platform"
	"sort"
)

type Item struct {
	ProductID string `json:"product_id"`
	Quantity  int    `json:"quantity"`
}

func Register(a *p.App) {
	a.Handle("POST", "/products", func(r *p.Request) (any, error) {
		if r.Role != "admin" {
			return nil, p.Fail(403, "admin required")
		}
		var b struct {
			Name  string `json:"name"`
			Price int64  `json:"price_cents"`
			Stock int    `json:"stock"`
		}
		if e := r.Decode(&b); e != nil {
			return nil, e
		}
		if b.Name == "" || b.Price > 100000000000 {
			return nil, p.Bad("invalid product")
		}
		return r.One(`INSERT INTO products(tenant,id,name,price_cents,stock) VALUES($1,$2,$3,$4,$5) RETURNING *`, r.Tenant, p.ID(), b.Name, b.Price, b.Stock)
	})
	a.Handle("GET", "/products", func(r *p.Request) (any, error) {
		return r.All(`SELECT * FROM products WHERE tenant=$1 ORDER BY id LIMIT 100`, r.Tenant)
	})
	a.Handle("POST", "/orders", func(r *p.Request) (any, error) {
		var b struct {
			Items []Item `json:"items"`
		}
		if e := r.Decode(&b); e != nil {
			return nil, e
		}
		if len(b.Items) == 0 || len(b.Items) > 100 {
			return nil, p.Bad("1..100 items required")
		}
		sort.Slice(b.Items, func(i, j int) bool { return b.Items[i].ProductID < b.Items[j].ProductID })
		prices := make([]int64, len(b.Items))
		var total int64
		for i, it := range b.Items {
			if it.Quantity < 1 || it.Quantity > 10000 || (i > 0 && it.ProductID == b.Items[i-1].ProductID) {
				return nil, p.Bad("invalid or duplicate item")
			}
			var stock int
			if e := r.Tx.QueryRow(r.Ctx, `SELECT price_cents,stock FROM products WHERE tenant=$1 AND id=$2 FOR UPDATE`, r.Tenant, it.ProductID).Scan(&prices[i], &stock); e != nil {
				return nil, e
			}
			if stock < it.Quantity {
				return nil, p.Conflict("insufficient stock")
			}
			total += prices[i] * int64(it.Quantity)
		}
		id := p.ID()
		result, e := r.One(`INSERT INTO orders(tenant,id,owner_id,total_cents) VALUES($1,$2,$3,$4) RETURNING *`, r.Tenant, id, r.User, total)
		if e != nil {
			return nil, e
		}
		for i, it := range b.Items {
			if e = r.Exec(`UPDATE products SET stock=stock-$3 WHERE tenant=$1 AND id=$2`, r.Tenant, it.ProductID, it.Quantity); e != nil {
				return nil, e
			}
			if e = r.Exec(`INSERT INTO order_items(tenant,order_id,product_id,quantity,unit_price_cents) VALUES($1,$2,$3,$4,$5)`, r.Tenant, id, it.ProductID, it.Quantity, prices[i]); e != nil {
				return nil, e
			}
		}
		return result, r.Event("order.created", result)
	})
	a.Handle("GET", "/orders/{id}", func(r *p.Request) (any, error) {
		result, e := r.One(`SELECT * FROM orders WHERE tenant=$1 AND id=$2 AND (owner_id=$3 OR $4='admin')`, r.Tenant, r.Param("id"), r.User, r.Role)
		if e != nil {
			return nil, e
		}
		items, e := r.All(`SELECT * FROM order_items WHERE tenant=$1 AND order_id=$2`, r.Tenant, r.Param("id"))
		result["items"] = items
		return result, e
	})
	a.Handle("POST", "/orders/{id}/cancel", func(r *p.Request) (any, error) {
		var status string
		if e := r.Tx.QueryRow(r.Ctx, `SELECT status FROM orders WHERE tenant=$1 AND id=$2 AND (owner_id=$3 OR $4='admin') FOR UPDATE`, r.Tenant, r.Param("id"), r.User, r.Role).Scan(&status); e != nil {
			return nil, e
		}
		if status != "pending" {
			return nil, p.Conflict("order is not pending")
		}
		if e := Release(r.Ctx, r.Tx, r.Tenant, r.Param("id"), "cancelled"); e != nil {
			return nil, e
		}
		return nil, r.Event("order.cancelled", map[string]string{"id": r.Param("id")})
	})
	// This is an explicit payment simulator, not an unverified public payment callback.
	a.Handle("POST", "/orders/{id}/payment", func(r *p.Request) (any, error) {
		if r.Role != "admin" {
			return nil, p.Fail(403, "simulator requires admin")
		}
		var b struct {
			EventID string `json:"event_id"`
			Outcome string `json:"outcome"`
		}
		if e := r.Decode(&b); e != nil {
			return nil, e
		}
		if b.EventID == "" || (b.Outcome != "success" && b.Outcome != "failure") {
			return nil, p.Bad("event_id and success/failure required")
		}
		var oldOrder, oldOutcome string
		e := r.Tx.QueryRow(r.Ctx, `SELECT order_id,outcome FROM payment_events WHERE tenant=$1 AND event_id=$2`, r.Tenant, b.EventID).Scan(&oldOrder, &oldOutcome)
		if e == nil {
			if oldOrder != r.Param("id") || oldOutcome != b.Outcome {
				return nil, p.Conflict("event id reused")
			}
			return map[string]bool{"duplicate": true}, nil
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return nil, e
		}
		var status string
		var expired bool
		if e = r.Tx.QueryRow(r.Ctx, `SELECT status,expires_at<=now() FROM orders WHERE tenant=$1 AND id=$2 FOR UPDATE`, r.Tenant, r.Param("id")).Scan(&status, &expired); e != nil {
			return nil, e
		}
		if status != "pending" || expired {
			return nil, p.Conflict("payment rejected: reservation inactive")
		}
		if e = r.Exec(`INSERT INTO payment_events(tenant,event_id,order_id,outcome) VALUES($1,$2,$3,$4)`, r.Tenant, b.EventID, r.Param("id"), b.Outcome); e != nil {
			return nil, e
		}
		if b.Outcome == "success" {
			e = r.Exec(`UPDATE orders SET status='paid' WHERE tenant=$1 AND id=$2`, r.Tenant, r.Param("id"))
		} else {
			e = Release(r.Ctx, r.Tx, r.Tenant, r.Param("id"), "cancelled")
		}
		if e != nil {
			return nil, e
		}
		return nil, r.Event("payment."+b.Outcome, map[string]string{"order_id": r.Param("id"), "event_id": b.EventID})
	})
	a.Workers = append(a.Workers, Expire)
}
func Release(ctx context.Context, tx pgx.Tx, tenant, id, state string) error {
	if _, e := tx.Exec(ctx, `UPDATE products p SET stock=p.stock+i.quantity FROM order_items i WHERE i.tenant=$1 AND i.order_id=$2 AND p.tenant=i.tenant AND p.id=i.product_id`, tenant, id); e != nil {
		return e
	}
	_, e := tx.Exec(ctx, `UPDATE orders SET status=$3 WHERE tenant=$1 AND id=$2`, tenant, id, state)
	return e
}
func Expire(ctx context.Context, db *pgxpool.Pool) error {
	tx, e := db.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var tenant, id string
	e = tx.QueryRow(ctx, `SELECT tenant,id FROM orders WHERE status='pending' AND expires_at<=now() ORDER BY expires_at FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&tenant, &id)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	if e = Release(ctx, tx, tenant, id, "expired"); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
