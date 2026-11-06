// Event consumer demonstrates commit-after-persist and idempotent inbox processing.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/segmentio/kafka-go"
	"log/slog"
	p "ordercore/internal/platform"
	"ordercore/migrations"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	project := p.Env("PROJECT", "ordercore")
	db, e := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if e != nil {
		panic(e)
	}
	defer db.Close()
	if e = migrations.Apply(ctx, db, project); e != nil {
		panic(e)
	}
	persist := func(body []byte) error {
		var event struct {
			ID, Tenant, Kind string
			Payload          json.RawMessage
		}
		if e := json.Unmarshal(body, &event); e != nil {
			return e
		}
		if event.ID == "" || event.Tenant == "" || event.Kind == "" || event.Payload == nil {
			return fmt.Errorf("invalid event envelope")
		}
		_, e := db.Exec(ctx, `INSERT INTO event_receipts(id,project,tenant,kind,payload) VALUES($1,$2,$3,$4,$5) ON CONFLICT(id) DO NOTHING`, event.ID, project, event.Tenant, event.Kind, string(event.Payload))
		return e
	}
	switch p.Env("BROKER", "rabbitmq") {
	case "rabbitmq":
		conn, e := amqp.DialConfig(os.Getenv("RABBITMQ_URL"), amqp.Config{Dial: amqp.DefaultDial(5 * time.Second)})
		if e != nil {
			panic(e)
		}
		defer conn.Close()
		ch, e := conn.Channel()
		if e != nil {
			panic(e)
		}
		defer ch.Close()
		q, e := ch.QueueDeclare(project+".events", true, false, false, false, nil)
		if e != nil {
			panic(e)
		}
		if e = ch.Qos(1, 0, false); e != nil {
			panic(e)
		}
		messages, e := ch.ConsumeWithContext(ctx, q.Name, "", false, false, false, false, nil)
		if e != nil {
			panic(e)
		}
		for msg := range messages {
			if e = persist(msg.Body); e != nil {
				slog.Error("event not persisted; consumer stopping", "error", e)
				return
			}
			if e = msg.Ack(false); e != nil {
				panic(e)
			}
		}
	case "kafka":
		reader := kafka.NewReader(kafka.ReaderConfig{Brokers: []string{p.Env("KAFKA_ADDR", "localhost:9092")}, Topic: project + ".events", GroupID: project + "-inbox", CommitInterval: 0})
		defer reader.Close()
		for {
			msg, e := reader.FetchMessage(ctx)
			if e != nil {
				if ctx.Err() != nil {
					return
				}
				panic(e)
			}
			if e = persist(msg.Value); e != nil {
				slog.Error("event not persisted; consumer stopping", "error", e)
				return
			}
			if e = reader.CommitMessages(ctx, msg); e != nil {
				panic(e)
			}
		}
	default:
		panic("BROKER must be kafka or rabbitmq")
	}
}
