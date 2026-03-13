#!/usr/bin/env bash
# Tier 1 curl test cases — copy individual commands into Postman or terminal

# 1. Basic success — public page
curl -s -o - -w "\nHTTP %{http_code}\n" -X POST http://localhost:8080/api/v1/tier1/fetch \
  -H "Content-Type: application/json" \
  -d '{"url":"https://example.com"}'

# 2. API endpoint override — fetch JSON directly
curl -s -o - -w "\nHTTP %{http_code}\n" -X POST http://localhost:8080/api/v1/tier1/fetch \
  -H "Content-Type: application/json" \
  -d '{"url":"https://example.com","api_endpoint":"https://httpbin.org/json"}'

# 3. Custom timeout — short but reachable URL
curl -s -o - -w "\nHTTP %{http_code}\n" -X POST http://localhost:8080/api/v1/tier1/fetch \
  -H "Content-Type: application/json" \
  -d '{"url":"https://example.com","timeout_ms":10000}'

# 4. Missing url field — expect 400
curl -s -o - -w "\nHTTP %{http_code}\n" -X POST http://localhost:8080/api/v1/tier1/fetch \
  -H "Content-Type: application/json" \
  -d '{}'

# 5. Malformed JSON — expect 400
curl -s -o - -w "\nHTTP %{http_code}\n" -X POST http://localhost:8080/api/v1/tier1/fetch \
  -H "Content-Type: application/json" \
  -d 'not-json'

# 6. Unreachable host — expect 502
curl -s -o - -w "\nHTTP %{http_code}\n" -X POST http://localhost:8080/api/v1/tier1/fetch \
  -H "Content-Type: application/json" \
  -d '{"url":"http://127.0.0.1:1","timeout_ms":500}'

# 7. Redirect following
curl -s -o - -w "\nHTTP %{http_code}\n" -X POST http://localhost:8080/api/v1/tier1/fetch \
  -H "Content-Type: application/json" \
  -d '{"url":"http://httpbin.org/redirect/2"}'
