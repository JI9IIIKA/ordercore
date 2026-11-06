package platform

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/segmentio/kafka-go"
	"os"
	"time"
)

// Outbox relay is at-least-once. Consumers deduplicate by event id.
func Relay(project string) Worker {
	return func(ctx context.Context, pool *pgxpool.Pool) error {
		broker := os.Getenv("BROKER")
		if broker == "" {
			return nil
		}
		ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		tx, e := pool.Begin(ctx)
		if e != nil {
			return e
		}
		defer tx.Rollback(ctx)
		var id, tenant, kind string
		var payload []byte
		e = tx.QueryRow(ctx, `SELECT id,tenant,kind,payload FROM outbox WHERE project=$1 AND delivered_at IS NULL ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1`, project).Scan(&id, &tenant, &kind, &payload)
		if e != nil {
			if e.Error() == "no rows in result set" {
				return nil
			}
			return e
		}
		body, e := json.Marshal(map[string]any{"id": id, "tenant": tenant, "kind": kind, "payload": json.RawMessage(payload)})
		if e != nil {
			return e
		}
		switch broker {
		case "rabbitmq":
			conn, e := amqp.DialConfig(Env("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/"), amqp.Config{Dial: amqp.DefaultDial(5 * time.Second)})
			if e != nil {
				return e
			}
			defer conn.Close()
			ch, e := conn.Channel()
			if e != nil {
				return e
			}
			defer ch.Close()
			q, e := ch.QueueDeclare(project+".events", true, false, false, false, nil)
			if e != nil {
				return e
			}
			if e = ch.Confirm(false); e != nil {
				return e
			}
			confirmation, e := ch.PublishWithDeferredConfirmWithContext(ctx, "", q.Name, true, false, amqp.Publishing{ContentType: "application/json", DeliveryMode: 2, MessageId: id, Body: body})
			if e != nil {
				return e
			}
			ok, e := confirmation.WaitContext(ctx)
			if e != nil {
				return e
			}
			if !ok {
				return Conflict("broker rejected event")
			}
		case "kafka":
			w := kafka.Writer{Addr: kafka.TCP(Env("KAFKA_ADDR", "localhost:9092")), Topic: project + ".events", RequiredAcks: kafka.RequireAll, AllowAutoTopicCreation: true, WriteTimeout: 5 * time.Second}
			defer w.Close()
			if e = w.WriteMessages(ctx, kafka.Message{Key: []byte(tenant), Value: body}); e != nil {
				return e
			}
		default:
			return Bad("unknown BROKER")
		}
		if _, e = tx.Exec(ctx, `UPDATE outbox SET delivered_at=now() WHERE id=$1`, id); e != nil {
			return e
		}
		return tx.Commit(ctx)
	}
}
