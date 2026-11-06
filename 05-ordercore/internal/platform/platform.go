package platform

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type Principal struct {
	Tenant string `json:"tenant"`
	User   string `json:"user"`
	Role   string `json:"role"`
}
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string          { return e.Message }
func Fail(status int, msg string) error { return &Error{status, msg} }
func Bad(msg string) error              { return Fail(400, msg) }
func Conflict(msg string) error         { return Fail(409, msg) }
func ID() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func Env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

type Request struct {
	Ctx  context.Context
	Tx   pgx.Tx
	HTTP *http.Request
	Principal
	Project string
}

func (r *Request) Param(k string) string { return mux.Vars(r.HTTP)[k] }
func (r *Request) Decode(v any) error {
	d := json.NewDecoder(r.HTTP.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return Bad("invalid JSON: " + e.Error())
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return Bad("expected one JSON object")
	}
	return nil
}
func (r *Request) Exec(q string, args ...any) error { _, e := r.Tx.Exec(r.Ctx, q, args...); return e }
func (r *Request) One(q string, args ...any) (map[string]any, error) {
	rows, e := r.Tx.Query(r.Ctx, q, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	if !rows.Next() {
		if rows.Err() != nil {
			return nil, rows.Err()
		}
		return nil, pgx.ErrNoRows
	}
	return rowMap(rows)
}
func rowMap(rows pgx.Rows) (map[string]any, error) {
	vals, e := rows.Values()
	if e != nil {
		return nil, e
	}
	m := map[string]any{}
	for i, f := range rows.FieldDescriptions() {
		m[string(f.Name)] = vals[i]
	}
	return m, nil
}
func (r *Request) All(q string, args ...any) ([]map[string]any, error) {
	rows, e := r.Tx.Query(r.Ctx, q, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		m, e := rowMap(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
func (r *Request) Event(kind string, body any) error {
	b, e := json.Marshal(body)
	if e != nil {
		return e
	}
	return r.Exec(`INSERT INTO outbox(id,project,tenant,kind,payload) VALUES($1,$2,$3,$4,$5)`, ID(), r.Project, r.Tenant, kind, string(b))
}

type Handler func(*Request) (any, error)
type Worker func(context.Context, *pgxpool.Pool) error
type App struct {
	Background []func(context.Context)
	Name       string
	Pool       *pgxpool.Pool
	Router     *mux.Router
	Keys       map[string]Principal
	Redis      *redis.Client
	Workers    []Worker
}

func New(name string, pool *pgxpool.Pool, keys map[string]Principal) *App {
	a := &App{Name: name, Pool: pool, Keys: keys, Router: mux.NewRouter()}
	if url := os.Getenv("REDIS_URL"); url != "" {
		opt, e := redis.ParseURL(url)
		if e != nil {
			panic(e)
		}
		a.Redis = redis.NewClient(opt)
	}
	a.Router.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		JSON(w, 200, map[string]string{"status": "ok", "project": name})
	})
	a.Router.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, c := context.WithTimeout(r.Context(), time.Second)
		defer c()
		if pool.Ping(ctx) != nil {
			JSON(w, 503, map[string]string{"status": "unavailable"})
			return
		}
		JSON(w, 200, map[string]string{"status": "ready"})
	})
	return a
}
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func (a *App) Handle(method, path string, h Handler) {
	a.Router.HandleFunc(path, func(w http.ResponseWriter, httpReq *http.Request) {
		start := time.Now()
		rid := ID()
		w.Header().Set("X-Request-ID", rid)
		defer func() {
			if v := recover(); v != nil {
				slog.Error("panic", "request_id", rid, "error", fmt.Sprint(v))
				JSON(w, 500, map[string]string{"error": "internal error", "request_id": rid})
			}
			slog.Info("request", "project", a.Name, "method", method, "route", path, "request_id", rid, "duration_ms", time.Since(start).Milliseconds())
		}()
		token := strings.TrimPrefix(httpReq.Header.Get("Authorization"), "Bearer ")
		p, ok := a.Keys[token]
		if !ok || token == "" || !strings.HasPrefix(httpReq.Header.Get("Authorization"), "Bearer ") {
			JSON(w, 401, map[string]string{"error": "invalid bearer token"})
			return
		}
		if method != "GET" && p.Role == "viewer" {
			JSON(w, 403, map[string]string{"error": "read-only role"})
			return
		}
		ctx, cancel := context.WithTimeout(httpReq.Context(), 15*time.Second)
		defer cancel()
		if a.Redis != nil {
			n, e := a.Redis.Eval(ctx, `local n=redis.call('INCR',KEYS[1]); if n==1 then redis.call('EXPIRE',KEYS[1],60) end; return n`, []string{"rate:" + a.Name + ":" + p.Tenant}).Int64()
			if e != nil {
				JSON(w, 503, map[string]string{"error": "rate limiter unavailable"})
				return
			}
			if n > 600 {
				JSON(w, 429, map[string]string{"error": "rate limit"})
				return
			}
		}
		body, e := io.ReadAll(http.MaxBytesReader(w, httpReq.Body, 1<<20))
		if e != nil {
			JSON(w, 413, map[string]string{"error": "body exceeds 1 MiB"})
			return
		}
		httpReq.Body = io.NopCloser(bytes.NewReader(body))
		tx, e := a.Pool.Begin(ctx)
		if e != nil {
			a.respondError(w, rid, e)
			return
		}
		defer tx.Rollback(ctx)
		// Serialize mutations within a tenant, including graph checks and idempotency.
		if method != "GET" {
			_, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, a.Name+":"+p.Tenant)
			if e != nil {
				a.respondError(w, rid, e)
				return
			}
		}
		r := &Request{Ctx: ctx, Tx: tx, HTTP: httpReq, Principal: p, Project: a.Name}
		key := httpReq.Header.Get("Idempotency-Key")
		if len(key) > 128 {
			a.respondError(w, rid, Bad("idempotency key too long"))
			return
		}
		sum := sha256.Sum256(append([]byte(method+httpReq.URL.RequestURI()+p.User+":"), body...))
		hash := hex.EncodeToString(sum[:])
		if key != "" && method != "GET" {
			var oldHash string
			var response []byte
			e = tx.QueryRow(ctx, `SELECT fingerprint,response FROM idempotency WHERE project=$1 AND tenant=$2 AND key=$3`, a.Name, p.Tenant, key).Scan(&oldHash, &response)
			if e == nil {
				if oldHash != hash {
					a.respondError(w, rid, Conflict("idempotency key reused with different request"))
					return
				}
				JSON(w, 200, json.RawMessage(response))
				return
			}
			if !errors.Is(e, pgx.ErrNoRows) {
				a.respondError(w, rid, e)
				return
			}
		}
		result, e := h(r)
		if e != nil {
			a.respondError(w, rid, e)
			return
		}
		if result == nil {
			result = map[string]bool{"ok": true}
		}
		encoded, e := json.Marshal(result)
		if e != nil {
			a.respondError(w, rid, e)
			return
		}
		if key != "" && method != "GET" {
			e = r.Exec(`INSERT INTO idempotency(project,tenant,key,fingerprint,response) VALUES($1,$2,$3,$4,$5)`, a.Name, p.Tenant, key, hash, string(encoded))
			if e != nil {
				a.respondError(w, rid, e)
				return
			}
		}
		if e = tx.Commit(ctx); e != nil {
			a.respondError(w, rid, e)
			return
		}
		JSON(w, 200, json.RawMessage(encoded))
	}).Methods(method)
}
func (a *App) respondError(w http.ResponseWriter, id string, e error) {
	status, msg := 500, "internal error"
	var api *Error
	var pg *pgconn.PgError
	if errors.As(e, &api) {
		status, msg = api.Status, api.Message
	} else if errors.Is(e, pgx.ErrNoRows) {
		status, msg = 404, "not found"
	} else if errors.As(e, &pg) {
		switch pg.Code {
		case "23505", "23P01", "40001":
			status, msg = 409, "conflicting operation"
		case "23503", "23514", "22P02":
			status, msg = 400, "constraint violation"
		}
	}
	if status == 500 {
		slog.Error("request failed", "request_id", id, "error", e)
	}
	JSON(w, status, map[string]string{"error": msg, "request_id": id})
}
func Serve(name string, register func(*App), migrate func(context.Context, *pgxpool.Pool) error) {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	pool, e := pgxpool.New(ctx, Env("DATABASE_URL", "postgres://portfolio:portfolio@localhost:5432/portfolio?sslmode=disable"))
	if e != nil {
		panic(e)
	}
	defer pool.Close()
	if e = migrate(ctx, pool); e != nil {
		panic(e)
	}
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		return
	}
	keys := map[string]Principal{}
	if e = json.Unmarshal([]byte(os.Getenv("API_KEYS_JSON")), &keys); e != nil || len(keys) == 0 {
		panic("API_KEYS_JSON must contain token -> {tenant,user,role}")
	}
	for token, p := range keys {
		if len(token) < 16 || p.Tenant == "" || p.User == "" || (p.Role != "admin" && p.Role != "editor" && p.Role != "viewer") {
			panic("invalid API key configuration")
		}
	}
	a := New(name, pool, keys)
	register(a)
	for _, start := range a.Background {
		go start(ctx)
	}
	if a.Redis != nil {
		defer a.Redis.Close()
	}
	a.Workers = append(a.Workers, Relay(name))
	for _, worker := range a.Workers {
		go func(fn Worker) {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if e := fn(ctx, pool); e != nil {
						slog.Error("worker failed", "project", name, "error", e)
					}
				}
			}
		}(worker)
	}
	server := &http.Server{Addr: Env("HTTP_ADDR", ":8080"), Handler: a.Router, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		_ = server.Shutdown(shutdown)
	}()
	slog.Info("listening", "project", name, "address", server.Addr)
	if e = server.ListenAndServe(); e != nil && e != http.ErrServerClosed {
		panic(e)
	}
}
