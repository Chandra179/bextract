#!/usr/bin/env bash
# Tier 3 curl test cases — copy individual commands into Postman or terminal
# Note: Tier 3 requires Chrome to be available on the server.

# 1. Static page — short-circuits (no browser needed)
curl -s -o - -w "\nHTTP %{http_code}\n" -X POST http://localhost:8080/api/v1/tier3/render \
  -H "Content-Type: application/json" \
  -d '{"url":"https://example.com"}'

# 2. With target fields on a static page
curl -s -o - -w "\nHTTP %{http_code}\n" -X POST http://localhost:8080/api/v1/tier3/render \
  -H "Content-Type: application/json" \
  -d '{"url":"https://example.com","target_fields":["title","description"]}'

# 3. Custom render timeout
curl -s -o - -w "\nHTTP %{http_code}\n" -X POST http://localhost:8080/api/v1/tier3/render \
  -H "Content-Type: application/json" \
  -d '{"url":"https://example.com","render_timeout_ms":8000}'

# 4. All timeouts specified
curl -s -o - -w "\nHTTP %{http_code}\n" -X POST http://localhost:8080/api/v1/tier3/render \
  -H "Content-Type: application/json" \
  -d '{"url":"https://example.com","fetch_timeout_ms":10000,"extraction_timeout_ms":5000,"render_timeout_ms":8000}'

# 5. Missing url — expect 400
curl -s -o - -w "\nHTTP %{http_code}\n" -X POST http://localhost:8080/api/v1/tier3/render \
  -H "Content-Type: application/json" \
  -d '{}'

# 6. Malformed JSON — expect 400
curl -s -o - -w "\nHTTP %{http_code}\n" -X POST http://localhost:8080/api/v1/tier3/render \
  -H "Content-Type: application/json" \
  -d 'not-json'

# 7. Unreachable host — expect 502
curl -s -o - -w "\nHTTP %{http_code}\n" -X POST http://localhost:8080/api/v1/tier3/render \
  -H "Content-Type: application/json" \
  -d '{"url":"http://127.0.0.1:1","fetch_timeout_ms":500}'

# 8. SPA page — triggers browser rendering
curl -s -o - -w "\nHTTP %{http_code}\n" -X POST http://localhost:8080/api/v1/tier3/render \
  -H "Content-Type: application/json" \
  -d '{"url":"https://react.dev","target_fields":["title"],"render_timeout_ms":15000}'
