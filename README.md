
Минимальный API для заявок на вывод средств с идемпотентностью и защитой от двойного списания.

## Требования
- Go 1.21+
- Docker (для локального Postgres)

## Запуск
1. Поднять Postgres:

   ```bash
   docker compose up -d db
   ```

2. Применить схему.

   Если установлен `psql`:

   ```bash
   psql "postgres://postgres:4444@localhost:5432/app?sslmode=disable" -f schema.sql
   ```

   Если `psql` не установлен, можно выполнить через контейнер.

   Bash:

   ```bash
   cat schema.sql | docker compose exec -T db psql -U postgres -d app
   ```

   PowerShell:

   ```powershell
   Get-Content -Raw schema.sql | docker compose exec -T db psql -U postgres -d app
   ```

3. Установить переменные окружения.

   Bash:

   ```bash
   export DATABASE_URL="postgres://postgres:4444@localhost:5432/app?sslmode=disable"
   export AUTH_TOKEN="devtoken"
   export PORT="8080"
   ```

   PowerShell:

   ```powershell
   $env:DATABASE_URL = "postgres://postgres:4444@localhost:5432/app?sslmode=disable"
   $env:AUTH_TOKEN   = "devtoken"
   $env:PORT         = "8080"
   ```

   Либо можно задать параметры подключения отдельно:

   ```bash
   export DB_HOST="localhost"
   export DB_PORT="5432"
   export DB_USER="postgres"
   export DB_PASSWORD="4444"
   export DB_NAME="app"
   export DB_SSLMODE="disable"
   export AUTH_TOKEN="devtoken"
   export PORT="8080"
   ```

4. Запустить сервер:

   ```bash
   go run ./cmd/api
   ```

5. Создать пользователя (нужно перед созданием заявок):

   ```bash
   curl -X POST http://localhost:8080/v1/users \
     -H "Authorization: Bearer devtoken" \
     -H "Content-Type: application/json" \
     -d '{"id":1,"balance":1000}'
   ```

   PowerShell:

   ```powershell
   $body = @{ id = 1; balance = 1000 } | ConvertTo-Json -Compress
   Invoke-RestMethod -Method Post -Uri "http://localhost:8080/v1/users" `
     -Headers @{ Authorization = "Bearer devtoken" } `
     -ContentType "application/json" `
     -Body $body
   ```

## API
- POST `/v1/users`
- POST `/v1/withdrawals`
- GET `/v1/withdrawals/{id}`
- POST `/v1/withdrawals/{id}/confirm`

## Примеры
Создание заявки:

```bash
curl -X POST http://localhost:8080/v1/withdrawals \
  -H "Authorization: Bearer devtoken" \
  -H "Content-Type: application/json" \
  -d '{"user_id":1,"amount":100,"currency":"USDT","destination":"addr","idempotency_key":"k1"}'
```

Получение заявки:

```bash
curl -X GET http://localhost:8080/v1/withdrawals/1 \
  -H "Authorization: Bearer devtoken"
```

Подтверждение заявки:

```bash
curl -X POST http://localhost:8080/v1/withdrawals/1/confirm \
  -H "Authorization: Bearer devtoken"
```

## Корректность
- Создание заявки выполняется в одной транзакции PostgreSQL.
- Баланс пользователя блокируется `SELECT ... FOR UPDATE`, что сериализует конкурентные выводы по пользователю.
- Идемпотентный ключ проверяется в этой же транзакции: тот же payload возвращает исходную заявку, другой payload дает 422.
- Обновление баланса и вставка заявки происходят в одной транзакции, что исключает двойное списание.
- Уникальное ограничение на `(user_id, idempotency_key)` — дополнительная защита.
- В `ledger_entries` записывается дебетовая проводка для каждого успешного списания.

## Логи
Структурные логи пишутся в JSON-виде для событий `user_created`, `user_create_failed`, `withdrawal_created`, `withdrawal_create_failed`, `withdrawal_confirmed`, `withdrawal_confirm_failed`.

## Тесты
1. Убедитесь, что Postgres запущен и применен `schema.sql`.
2. Установите `DATABASE_URL` или `DB_*` и `AUTH_TOKEN`.
3. Запустите тесты:

   ```bash
   go test ./...
   ```
