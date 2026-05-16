#!/usr/bin/env bash
set -euo pipefail

frontend_forbidden='Partial<(AdminProvider|LLMProviderRecord|LLMModelRecord|ExternalResource|ScheduledTask)>'
backend_forbidden='\.(SaveScheduledTask|SaveMCPServer|SaveExternalResource|SaveLLMProvider|SaveLLMModel|SaveChannelConfig)\('

if rg -n "$frontend_forbidden" frontend/src/api frontend/src/pages frontend/src/components; then
  echo "found broad frontend write type; use explicit CreateRequest/UpdateRequest DTOs" >&2
  exit 1
fi

if rg -n "$backend_forbidden" internal/api internal/gateway --glob '!**/*_test.go'; then
  echo "new API/gateway code should not call store Save* full-record methods directly" >&2
  exit 1
fi
