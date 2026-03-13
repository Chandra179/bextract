#!/usr/bin/env bash
# Tier 2 curl test cases — copy individual commands into Postman or terminal

# 1. Basic analysis — public HTML page
curl -s -o - -w "\nHTTP %{http_code}\n" -X POST http://localhost:8080/api/v1/tier2/analyze \
  -H "Content-Type: application/json" \
  -d '{"url":"https://example.com"}'

# 2. With target fields
curl -s -o - -w "\nHTTP %{http_code}\n" -X POST http://localhost:8080/api/v1/tier2/analyze \
  -H "Content-Type: application/json" \
  -d '{"url":"https://example.com","target_fields":["title","description"]}'

# 3. JSON API endpoint
curl -s -o - -w "\nHTTP %{http_code}\n" -X POST http://localhost:8080/api/v1/tier2/analyze \
  -H "Content-Type: application/json" \
  -d '{"url":"https://httpbin.org/json","target_fields":["slideshow"]}'

# 4. Custom fetch and extraction timeouts
curl -s -o - -w "\nHTTP %{http_code}\n" -X POST http://localhost:8080/api/v1/tier2/analyze \
  -H "Content-Type: application/json" \
  -d '{"url":"https://example.com","fetch_timeout_ms":10000,"extraction_timeout_ms":5000}'

# 5. Missing url — expect 400
curl -s -o - -w "\nHTTP %{http_code}\n" -X POST http://localhost:8080/api/v1/tier2/analyze \
  -H "Content-Type: application/json" \
  -d '{}'

# 6. Malformed JSON — expect 400
curl -s -o - -w "\nHTTP %{http_code}\n" -X POST http://localhost:8080/api/v1/tier2/analyze \
  -H "Content-Type: application/json" \
  -d 'not-json'

# 7. Unreachable host — expect 502
curl -s -o - -w "\nHTTP %{http_code}\n" -X POST http://localhost:8080/api/v1/tier2/analyze \
  -H "Content-Type: application/json" \
  -d '{"url":"http://127.0.0.1:1","fetch_timeout_ms":500}'

# 8. Page with JSON-LD structured data
curl -s -o - -w "\nHTTP %{http_code}\n" -X POST http://localhost:8080/api/v1/tier2/analyze \
  -H "Content-Type: application/json" \
  -d '{"url":"https://schema.org/Product","target_fields":["name","description"]}'
