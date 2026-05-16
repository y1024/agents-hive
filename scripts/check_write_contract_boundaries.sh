#!/usr/bin/env bash
set -euo pipefail

frontend_forbidden='Partial<(AdminProvider|LLMProviderRecord|LLMModelRecord|ExternalResource|ScheduledTask)>'
backend_forbidden='\.(SaveScheduledTask|SaveMCPServer|SaveExternalResource|SaveLLMProvider|SaveLLMModel|SaveChannelConfig)\('
store_update_record_forbidden='Update(LLMProvider|LLMModel|ExternalResource)\(ctx context\.Context, [^)]*\*([A-Za-z0-9]+\.)?(LLMProviderRecord|LLMModelRecord|ExternalResourceRecord)\)'
config_update_view_forbidden='(dingtalk\?: DingTalkConfig;|feishu\?: FeishuConfig;|wecom\?: WeComConfig;|wechatbot\?: WeChatBotConfig;|servers\?: Record<string, MCPServerConfig \| null>;)'

if rg -n "$frontend_forbidden" frontend/src/api frontend/src/pages frontend/src/components; then
  echo "found broad frontend write type; use explicit CreateRequest/UpdateRequest DTOs" >&2
  exit 1
fi

if rg -n "$backend_forbidden" internal/api internal/gateway --glob '!**/*_test.go'; then
  echo "new API/gateway code should not call store Save* full-record methods directly" >&2
  exit 1
fi

if rg -n "$store_update_record_forbidden" internal/store --glob '!**/*_test.go'; then
  echo "store Update* methods for sensitive resources must accept field-level update DTOs, not persisted records" >&2
  exit 1
fi

config_update_block="$(sed -n '/export interface ConfigUpdateRequest/,/\/\/ 外部资源/p' frontend/src/types/api.ts)"
if printf '%s\n' "$config_update_block" | rg -n "$config_update_view_forbidden"; then
  echo "ConfigUpdateRequest must use patch DTOs, not runtime view config types" >&2
  exit 1
fi
