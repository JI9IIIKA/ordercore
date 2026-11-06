# OrderCore — Заказы и складские остатки

Проект №05. Самостоятельное Go-приложение. Сборка: `./cmd/ordercore`.
Общие пакеты находятся в `internal/platform`, поэтому сохраняйте структуру репозитория.

## Реализовано в v0.1

Фиксация цены; резервирование остатков в транзакции; срок резерва; освобождение при отмене/ошибке; admin-only симулятор оплаты; дедупликация payment events; outbox.

Данные: `products, orders, order_items, payment_events`. Схема: `../../migrations/ordercore_001.sql`.
Все записи изолированы по tenant; роль определяется серверным API-токеном.

## Запуск

Из корня репозитория:

```bash
python3 scripts/setup.py  # один раз для всего набора
PROJECT=ordercore docker compose up --build -d app
python3 scripts/demo.py ordercore
```

Для JobRunner запускайте ещё `--profile jobs`; точные команды профилей — в корневом README.
Демо создаёт тестовые записи. Остановить: `docker compose down`.

## API

Все пути требуют `Authorization: Bearer <token>`. Ошибки возвращаются как JSON.
`Idempotency-Key` необязателен, но рекомендуется для команд создания и отправки.
Повтор того же запроса с тем же ключом возвращает сохранённый ответ; изменение запроса даёт 409.

| Метод | Путь |
|---|---|
| POST | `/products` |
| GET | `/products` |
| POST | `/orders` |
| GET | `/orders/{id}` |
| POST | `/orders/{id}/cancel` |
| POST | `/orders/{id}/payment` |

## Примеры тел запросов

`POST /products`:

```json
{
  "name": "Клавиатура",
  "price_cents": 10000,
  "stock": 5
}
```

`POST /orders`:

```json
{
  "items": [
    {
      "product_id": "PRODUCT_ID",
      "quantity": 1
    }
  ]
}
```

`POST /orders/{id}/payment`:

```json
{
  "event_id": "payment-1",
  "outcome": "success"
}
```

## Проверка

```bash
TEST_DATABASE_URL='postgres://portfolio:portfolio-local-only@localhost:5432/portfolio?sslmode=disable' \
  go test -count=1 -v ./tests -run '^TestOrderCore$'
```

Интеграционные тесты пишут данные в указанную БД. Используйте отдельную тестовую базу.
Полный сценарий проверки находится в `../../tests/integration_test.go`.

## Границы версии и дальнейшая работа

Выделение склада и оплаты в самостоятельные gRPC-сервисы, распределённая saga и реальный платёжный адаптер. Сейчас целостность обеспечивается одной PostgreSQL-транзакцией.

Это реализованное ядро проекта, а не заявление о завершении всех расширений исходной идеи.
Подробнее об ограничениях окружения проверки — `../../VALIDATION.md`.
