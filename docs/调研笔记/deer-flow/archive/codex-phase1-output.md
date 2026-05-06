# Codex 独立调研报告：deer-flow 工具集+LLM+存储+集成
日期：2026-04-21
调研范围：议题 1-7
源码路径：/Users/guoss/workspace/deer-flow
备注：请求路径 /Users/guoss/workspace/company/vast/deer-flow/ 在当前环境不可访问；实际调研路径为 /Users/guoss/workspace/deer-flow。

## 议题 1：工具集全貌

**读过的文件**（每个 file:line_range）

- `config.example.yaml:334-480`，工具组、默认工具列表、provider 可替换配置。
- `backend/packages/harness/deerflow/tools/tools.py:1-168`，工具注册、过滤、MCP/ACP/内置工具合并与去重。
- `backend/packages/harness/deerflow/config/tool_config.py:1-20`，ToolConfig/ToolGroupConfig schema。
- `backend/packages/harness/deerflow/community/ddg_search/tools.py:1-95`，DuckDuckGo web_search 实现。
- `backend/packages/harness/deerflow/community/jina_ai/tools.py:1-32`，Jina web_fetch 实现。
- `backend/packages/harness/deerflow/community/exa/tools.py:1-79`，Exa web_search/web_fetch。
- `backend/packages/harness/deerflow/community/firecrawl/tools.py:1-73`，Firecrawl web_search/web_fetch。
- `backend/packages/harness/deerflow/community/infoquest/tools.py:1-93`，InfoQuest web_search/web_fetch/image_search。
- `backend/packages/harness/deerflow/sandbox/tools.py:1-360`，路径映射、skills/ACP/MCP allowed paths、grep/glob 格式化。
- `backend/packages/harness/deerflow/sandbox/tools.py:545-720`，本地 sandbox 路径安全校验。
- `backend/packages/harness/deerflow/sandbox/tools.py:989-1368`，bash/ls/glob/grep/read_file/write_file/str_replace 实现。
- `backend/packages/harness/deerflow/agents/lead_agent/agent.py:280-357`，agent 创建时按模型、agent config、subagent_enabled 传入工具。
- `backend/packages/harness/deerflow/agents/middlewares/tool_error_handling_middleware.py:1-125`，工具异常转 ToolMessage。
- `backend/packages/harness/deerflow/tools/builtins/task_tool.py:1-252`，task subagent 工具。

**关键代码片段**

`config.example.yaml:345-361`

```yaml
tools:
  # Web search tool (uses DuckDuckGo, no API key required)
  - name: web_search
    group: web
    use: deerflow.community.ddg_search.tools:web_search_tool
    max_results: 5

  # Web search tool (requires Tavily API key)
```

`backend/packages/harness/deerflow/tools/tools.py:55-63`

```python
config = get_app_config()
tool_configs = [tool for tool in config.tools if groups is None or tool.group in groups]

# Do not expose host bash by default when LocalSandboxProvider is active.
if not is_host_bash_allowed(config):
    tool_configs = [tool for tool in tool_configs if not _is_host_bash_tool(tool)]

loaded_tools_raw = [(cfg, resolve_variable(cfg.use, BaseTool)) for cfg in tool_configs]
```

`backend/packages/harness/deerflow/community/ddg_search/tools.py:72-79`

```python
results = _search_text(
    query=query,
    max_results=max_results,
)

if not results:
    return json.dumps({"error": "No results found", "query": query}, ensure_ascii=False)
```

`backend/packages/harness/deerflow/sandbox/tools.py:1215-1220`

```python
Args:
    description: Explain why you are reading this file in short words. ALWAYS PROVIDE THIS PARAMETER FIRST.
    path: The **absolute** path to the file to read.
    start_line: Optional starting line number (1-indexed, inclusive). Use with end_line to read a specific range.
    end_line: Optional ending line number (1-indexed, inclusive). Use with start_line to read a specific range.
```

`backend/packages/harness/deerflow/agents/middlewares/tool_error_handling_middleware.py:29-35`

```python
content = f"Error: Tool '{tool_name}' failed with {exc.__class__.__name__}: {detail}. Continue with available context, or choose an alternative tool."
return ToolMessage(
    content=content,
    tool_call_id=tool_call_id,
    name=tool_name,
    status="error",
)
```

**分析**

deer-flow 的“工具类名”大多数不是自定义 class，而是 `@tool(...)` 装饰的函数，运行时表现为 LangChain `BaseTool`/StructuredTool。工具 schema 主要由函数签名和 docstring 通过 `parse_docstring=True` 生成，底层配置 schema 是 `ToolConfig(name, group, use)`，额外参数通过 Pydantic `extra="allow"` 保留。

内置/默认可用工具分三类：

- 配置工具：`web_search`、`web_fetch`、`image_search`、`ls`、`read_file`、`glob`、`grep`、`write_file`、`str_replace`、`bash`，定义在 `config.example.yaml:345-461`。
- 代码内置工具：`present_files`、`ask_clarification` 默认加入，`view_image` 按模型 vision 能力加入，`task` 按 `subagent_enabled` 加入，`skill_manage` 按 `skill_evolution.enabled` 加入，`tool_search` 按 `tool_search.enabled` 和 MCP deferred registry 加入，`invoke_acp_agent` 按 ACP agents 配置加入，见 `tools.py:13-21`、`tools.py:79-151`。
- MCP 动态工具：`get_cached_mcp_tools()` 读取并合并到工具集，见 `tools.py:102-132`。

工具注册机制是配置驱动加静态内置混合：`config.tools[].use` 是 Python import path，`resolve_variable(cfg.use, BaseTool)` 动态解析为工具对象；内置工具列表静态维护；MCP 工具动态缓存；最后按 tool name 去重，配置工具优先，见 `tools.py:153-168`。agent 可见工具过滤有三层：

- `groups` 过滤：`get_available_tools(groups=agent_config.tool_groups)` 只暴露指定工具组，见 `lead_agent/agent.py:350-356`。
- host bash 安全过滤：LocalSandboxProvider 默认不暴露 host bash，见 `tools.py:58-60`。
- subagent 特殊过滤：subagent 继承 parent tool_groups，并显式 `subagent_enabled=False` 防止递归，见 `task_tool.py:107-116`。

重点工具结论：

- web search：默认 DDG。参数 schema 是 `query: str, max_results: int = 5`，返回 JSON 字符串 `{query,total_results,results:[{title,url,content}]}`；空结果显式返回 `{"error":"No results found","query":...}`，见 `ddg_search/tools.py:55-95`。
- crawl/web_fetch：默认 Jina。参数 `url: str`，读取 tool config `timeout`，调用 `JinaClient.crawl(... return_format="html")`，若结果以 `Error:` 开头直接返回，否则 readability 抽正文并截断 4096，见 `jina_ai/tools.py:12-32`。
- Python REPL：没有独立 Python REPL 工具或类。Python 能力通过 `bash` 工具提示“Use `python` to run Python code”，见 `sandbox/tools.py:989-1001`。
- TTS：未发现 TTS/text-to-speech 工具实现。`rg` 只命中 lockfile 里的 speechrecognition 依赖和通用文本，不存在 TTS 工具。
- RAG/retriever：未发现向量检索工具；memory 注入不是 RAG，详见议题 4。
- file 读写：`ls`、`glob`、`grep`、`read_file`、`write_file`、`str_replace` 全部在 sandbox 层。读路径允许 `/mnt/user-data`、只读 skills、只读 ACP workspace、自定义 mount；写路径禁止 skills/ACP，只允许 user-data 或可写 mount，见 `sandbox/tools.py:545-596`。写入和替换用 `get_file_operation_lock(sandbox,path)` 串行化，见 `sandbox/tools.py:1285-1287`、`sandbox/tools.py:1329-1340`。

错误处理分两层：

- 工具自身常把错误返回为 `"Error: ..."` 字符串，例如 file/batch 工具，见 `sandbox/tools.py:1030-1035`、`sandbox/tools.py:1248-1257`。
- 若工具抛异常，`ToolErrorHandlingMiddleware` 把异常转成 `ToolMessage(status="error")`，见 `tool_error_handling_middleware.py:19-65`。

**对照 agents-hive 的借鉴点**

agents-hive 的 DDG “HTTP 200 + 空正则 = 静默零结果”问题，在 deer-flow DDG 默认实现里更好：空结果被显式编码为 JSON error，不是成功空列表，见 `ddg_search/tools.py:77-78`。但 Exa/Firecrawl 的 search 在空结果时返回 `[]` JSON，没有统一 error envelope，见 `exa/tools.py:42-53`、`firecrawl/tools.py:35-46`。如果 agents-hive 要抄，应抄 DDG 的显式失败格式，再统一所有 search provider。

deer-flow 对 agents-hive “每轮全量工具无 tool_choice”的借鉴点不是 tool_choice，而是按 `tool_groups`、vision、subagent_enabled、host bash 安全、MCP deferred tool_search 做上下文削减，见 `tools.py:35-40`、`tools.py:102-132`、`lead_agent/agent.py:350-356`。它仍没有在源码里显式传 `tool_choice`，但通过工具可见性降低选择空间。

## 议题 2：LLM 适配层 / provider

**读过的文件**（每个 file:line_range）

- `config.example.yaml:37-320`，provider 示例和参数。
- `backend/packages/harness/deerflow/config/model_config.py:1-41`，ModelConfig schema。
- `backend/packages/harness/deerflow/models/factory.py:1-141`，模型创建、thinking/reasoning 参数注入。
- `backend/packages/harness/deerflow/models/openai_codex_provider.py:1-430`，Codex Responses API provider。
- `backend/packages/harness/deerflow/models/claude_provider.py:1-348`，Claude OAuth provider、prompt cache、retry/backoff。
- `backend/packages/harness/deerflow/models/patched_openai.py:1-132`，OpenAI-compatible Gemini thought_signature 保真。
- `backend/packages/harness/deerflow/models/patched_deepseek.py:1-73`，DeepSeek reasoning_content 保真。
- `backend/packages/harness/deerflow/models/vllm_provider.py:1-258`，vLLM reasoning/tool_call chunk 保真。
- `backend/packages/harness/deerflow/models/patched_minimax.py:1-220`，MiniMax reasoning_details 保真。
- `backend/app/gateway/services.py:287-309`，前端 context 参数转 configurable。
- `backend/packages/harness/deerflow/client.py:560-680`，embedded client 流式 messages/tool_calls/usage 处理。

**关键代码片段**

`backend/packages/harness/deerflow/models/factory.py:64-79`

```python
model_class = resolve_class(model_config.use, BaseChatModel)
model_settings_from_config = model_config.model_dump(
    exclude_none=True,
    exclude={
        "use",
        "name",
        "display_name",
```

`backend/packages/harness/deerflow/models/factory.py:87-95`

```python
if thinking_enabled and has_thinking_settings:
    if not model_config.supports_thinking:
        raise ValueError(f"Model {name} does not support thinking. Set `supports_thinking` to true in the `config.yaml` to enable thinking.") from None
    if effective_wte:
        model_settings_from_config.update(effective_wte)
if not thinking_enabled:
```

`backend/packages/harness/deerflow/models/openai_codex_provider.py:181-188`

```python
payload = {
    "model": self.model,
    "instructions": instructions,
    "input": input_items,
    "store": False,
    "stream": True,
    "reasoning": {"effort": self.reasoning_effort, "summary": "detailed"} if self.reasoning_effort != "none" else {"effort": "none"},
}
```

`backend/packages/harness/deerflow/models/vllm_provider.py:107-113`

```python
reasoning = _dict.get("reasoning")
if reasoning is not None:
    additional_kwargs["reasoning"] = reasoning
    reasoning_text = _reasoning_to_text(reasoning)
    if reasoning_text:
        additional_kwargs["reasoning_content"] = reasoning_text
```

`backend/packages/harness/deerflow/models/patched_openai.py:120-132`

```python
for idx, payload_tc in enumerate(payload_tool_calls):
    # Try matching by id first, then fall back to positional.
    raw_tc = raw_by_id.get(payload_tc.get("id", ""))
    if raw_tc is None and idx < len(raw_tool_calls):
        raw_tc = raw_tool_calls[idx]

    if raw_tc is None:
```

**分析**

支持 provider 类型不是 enum 固定，而是 `models[].use` 指向任意 LangChain `BaseChatModel` 子类。示例明确覆盖：

- OpenAI：`langchain_openai:ChatOpenAI`，含 Chat Completions 和 Responses API 示例，见 `config.example.yaml:59-81`。
- Anthropic：`langchain_anthropic:ChatAnthropic` 和自定义 `deerflow.models.claude_provider:ClaudeChatModel`，见 `config.example.yaml:118-133`、`claude_provider.py:44-53`。
- Azure：没有在读到的源码和 config 示例中出现显式 Azure provider 示例，但可通过 LangChain provider path 扩展。
- 字节豆包/火山：示例把 Volcengine Ark/Doubao 作为 OpenAI/DeepSeek-compatible，使用 `PatchedChatDeepSeek` 加 `api_base: https://ark.cn-beijing.volces.com/api/v3`，见 `config.example.yaml:38-57`。
- DeepSeek：`deerflow.models.patched_deepseek:PatchedChatDeepSeek`，见 `config.example.yaml:171-190`。
- Gemini：native `langchain_google_genai:ChatGoogleGenerativeAI`，以及 OpenAI-compatible gateway 用 `PatchedChatOpenAI` 保 thought_signature，见 `config.example.yaml:135-169`。
- Ollama：`langchain_ollama:ChatOllama`，见 `config.example.yaml:83-117`。
- MiniMax：可直接 `langchain_openai:ChatOpenAI`，也存在 `PatchedChatMiniMax` 适配类，见 `patched_minimax.py:1-11`。
- vLLM：`deerflow.models.vllm_provider:VllmChatModel`，见 `config.example.yaml:307-320`。
- Codex CLI/ChatGPT Codex：`deerflow.models.openai_codex_provider:CodexChatModel`，见 `openai_codex_provider.py:1-12`。

参数传递链：

- 前端/HTTP body 的 `context` 只白名单转入 `configurable`，包括 `model_name`、`thinking_enabled`、`reasoning_effort`、`subagent_enabled`，见 `services.py:287-307`。
- `make_lead_agent` 读取这些 configurable，解析模型名和 thinking，调用 `create_chat_model(name=model_name, thinking_enabled=..., reasoning_effort=...)`，见 `lead_agent/agent.py:285-352`。
- `create_chat_model` 从 config dump 出除元字段以外的全部 provider 参数，因此 `temperature`、`top_p`、`max_tokens`、`request_timeout`、`base_url` 等 extra 字段都会透传给 provider 构造函数，见 `factory.py:64-79` 和 `model_config.py:15`。
- `reasoning_effort` 只有 `supports_reasoning_effort=True` 才透传；否则从 kwargs 和 config 中移除，见 `factory.py:112-115`。
- Codex provider 特殊把 thinking 映射成 `reasoning.effort`，thinking off 则 `none`，显式 effort 支持 `low/medium/high/xhigh`，见 `factory.py:118-132`、`openai_codex_provider.py:181-188`。

流式支持：

- 通用 LangChain provider 走 LangGraph `agent.astream(... stream_mode=...)`，gateway worker 发布 `values`、`messages`、`custom` 等 mode，见 `runtime/runs/worker.py:154-181`。
- embedded client 同时订阅 `["values","messages","custom"]`，对 `messages` mode 里的 AIMessage 直接发 text delta 和 tool_calls，对 ToolMessage 发工具结果，见 `client.py:595-638`。
- `client.py:620-632` 说明主回调不会只看文本；它显式处理 `msg_chunk.tool_calls`。
- vLLM provider 的 `_convert_delta_to_message_chunk_with_reasoning` 既保 `reasoning/reasoning_content`，也把 streaming `tool_calls` 转成 `tool_call_chunk(...)`，见 `vllm_provider.py:94-147`。
- MiniMax provider 保 `reasoning_details`，把它转到 `additional_kwargs.reasoning_content`，见 `patched_minimax.py:119-181`。
- Codex provider 实际要求 `stream=True`，但它当前把 SSE 聚合到最终 `ChatResult`，不是逐 token 向 LangChain 产出 chunk，见 `openai_codex_provider.py:220-265`。

重试/退避：

- Claude provider 手写 retry，捕获 `anthropic.RateLimitError` 和 `anthropic.InternalServerError`，指数退避 `2000 * 2^(attempt-1)` 加 20% buffer，优先尊重 `Retry-After`，见 `claude_provider.py:281-348`。
- Codex provider 手写 retry，HTTP 429/500/529 时指数退避 2s、4s、8s，无 jitter，见 `openai_codex_provider.py:201-218`。
- 其他 LangChain provider 主要依赖 provider 自身的 `max_retries` 参数，deer-flow 只是透传 config。

结构化输出：

- 没发现 deer-flow 自己实现 `response_format`、JSON mode 或 `with_structured_output` 的统一抽象。
- Memory updater prompt 要求“Return ONLY valid JSON”，属于提示约束，不是 provider 层 JSON mode。
- Tool calling 依赖 LangChain `bind_tools`；Codex provider 自己把 LangChain tool schema 转 Responses API function schema，见 `openai_codex_provider.py:152-175`、`openai_codex_provider.py:388-430`。

tool_choice：

- 源码搜索未发现 `tool_choice` 的主动传递。deer-flow 的策略是过滤工具可见性和 deferred MCP tool_search，而不是每轮指定 tool_choice。

## 议题 3：MCP 集成

**读过的文件**（每个 file:line_range）

- `backend/packages/harness/deerflow/config/extensions_config.py:1-260`，extensions_config.json/mcp_config.json schema、服务发现、env 解析。
- `backend/packages/harness/deerflow/mcp/client.py:1-68`，MCP server config 转 MultiServerMCPClient 参数。
- `backend/packages/harness/deerflow/mcp/tools.py:1-113`，加载 MCP tools、OAuth header、sync wrapper。
- `backend/packages/harness/deerflow/mcp/cache.py:1-142`，MCP tool cache、mtime stale reload、lazy init。
- `backend/packages/harness/deerflow/mcp/oauth.py:1-150`，HTTP/SSE MCP OAuth token 管理。
- `backend/app/gateway/routers/mcp.py:1-169`，MCP 配置 API。
- `backend/packages/harness/deerflow/tools/tools.py:102-132`，MCP 工具并入 agent tool list 或 deferred registry。
- `backend/packages/harness/deerflow/tools/builtins/tool_search.py:1-156`，deferred MCP 工具搜索和提升，已读到函数定位但未全文引用。
- `backend/packages/harness/deerflow/agents/lead_agent/prompt.py:610-632`，prompt 里列出 deferred tools 名称。

**关键代码片段**

`backend/packages/harness/deerflow/config/extensions_config.py:55-67`

```python
class ExtensionsConfig(BaseModel):
    """Unified configuration for MCP servers and skills."""

    mcp_servers: dict[str, McpServerConfig] = Field(
        default_factory=dict,
        description="Map of MCP server name to configuration",
        alias="mcpServers",
```

`backend/packages/harness/deerflow/mcp/client.py:21-40`

```python
transport_type = config.type or "stdio"
params: dict[str, Any] = {"transport": transport_type}

if transport_type == "stdio":
    if not config.command:
        raise ValueError(f"MCP server '{server_name}' with stdio transport requires 'command' field")
    params["command"] = config.command
```

`backend/packages/harness/deerflow/mcp/tools.py:98-107`

```python
client = MultiServerMCPClient(servers_config, tool_interceptors=tool_interceptors, tool_name_prefix=True)

# Get all tools from all servers
tools = await client.get_tools()
logger.info(f"Successfully loaded {len(tools)} tool(s) from MCP servers")

# Patch tools to support sync invocation, as deerflow client streams synchronously
```

`backend/packages/harness/deerflow/tools/tools.py:121-132`

```python
# When tool_search is enabled, register MCP tools in the
# deferred registry and add tool_search to builtin tools.
if config.tool_search.enabled:
    from deerflow.tools.builtins.tool_search import DeferredToolRegistry, set_deferred_registry
    from deerflow.tools.builtins.tool_search import tool_search as tool_search_tool
```

**分析**

存在 MCP client 集成，不是 MCP server。实现依赖 `langchain-mcp-adapters` 的 `MultiServerMCPClient`，见 `mcp/tools.py:62-66`、`mcp/tools.py:98-101`。

服务发现：

- 配置文件优先级：显式路径、`DEER_FLOW_EXTENSIONS_CONFIG_PATH`、backend/repo root 的 `extensions_config.json`，然后 legacy `mcp_config.json`，见 `extensions_config.py:69-115`。
- schema 支持 `stdio`、`sse`、`http`，字段包括 `command,args,env,url,headers,oauth,description`，见 `extensions_config.py:34-46`。
- env 变量解析：extensions config 里 `$VAR` 不存在时被置为空字符串，不像 app config 那样报错，见 `extensions_config.py:145-173`。

协议适配：

- `stdio` 要求 `command`，传 `args/env`。
- `sse/http` 要求 `url`，传 `headers`。
- 其他 transport 直接 ValueError 并被 `build_servers_config` 记录错误跳过，见 `mcp/client.py:21-68`。

工具代理：

- `get_mcp_tools()` 实例化 `MultiServerMCPClient(... tool_name_prefix=True)`，避免不同 server 工具重名。
- HTTP/SSE OAuth 有两段：初始化连接前注入 initial Authorization headers，工具调用时通过 `tool_interceptors` 刷新并覆盖 headers，见 `mcp/tools.py:83-98`、`mcp/oauth.py:122-150`。
- 如果 MCP tool 只有 coroutine 没有 sync func，会用 `_make_sync_tool_wrapper` 包一层，支持同步 embedded client 调用，见 `mcp/tools.py:25-53`、`mcp/tools.py:104-107`。

和内置工具如何并列：

- 默认 `get_available_tools()` 顺序是 config tools、builtins、mcp_tools、acp_tools，然后按名字去重，见 `tools.py:151-168`。
- 如果 `tool_search.enabled=false`，MCP tools 直接进入 model tool schema。
- 如果 `tool_search.enabled=true`，MCP tools 注册到 DeferredToolRegistry，只把 `tool_search` 暴露给模型，prompt 里列可用 deferred tool names，见 `tools.py:121-132`、`lead_agent/prompt.py:610-632`。

## 议题 4：RAG / 知识库 / 检索

**读过的文件**（每个 file:line_range）

- `backend/docs/MEMORY_IMPROVEMENTS.md:1-65`，明确 TF-IDF/context-aware retrieval planned/not merged。
- `backend/packages/harness/deerflow/agents/middlewares/memory_middleware.py:1-98`，memory 运行时队列入口。
- `backend/packages/harness/deerflow/agents/memory/storage.py:1-216`，memory JSON 文件存储。
- `backend/packages/harness/deerflow/agents/memory/prompt.py:1-340`，memory prompt、事实注入、token budget。
- `backend/packages/harness/deerflow/agents/lead_agent/prompt.py:677-700`，system prompt 注入 memory/subagent sections。
- `backend/app/gateway/routers/uploads.py:1-180`，上传入口已检索但未作为 RAG chunk/embed/index 使用。
- `backend/packages/harness/deerflow/uploads/manager.py` 已定位上传管理，但未发现 chunk/embed/index。
- 全局搜索 `RAG|retriev|vector|embed|embedding|faiss|chroma|pgvector|qdrant|milvus|weaviate`，未发现向量库/embedding pipeline。

**关键代码片段**

`backend/docs/MEMORY_IMPROVEMENTS.md:13-18`

```markdown
Planned / not yet merged:
- TF-IDF similarity-based fact retrieval.
- `current_context` input for context-aware scoring.
- Configurable similarity/confidence weights (`similarity_weight`, `confidence_weight`).
- Middleware/runtime wiring for context-aware retrieval before each model call.
```

`backend/packages/harness/deerflow/agents/memory/storage.py:24-40`

```python
def create_empty_memory() -> dict[str, Any]:
    """Create an empty memory structure."""
    return {
        "version": "1.0",
        "lastUpdated": utc_now_iso_z(),
        "user": {
            "workContext": {"summary": "", "updatedAt": ""},
```

`backend/packages/harness/deerflow/agents/memory/prompt.py:256-263`

```python
# Format facts (sorted by confidence; include as many as token budget allows)
facts_data = memory_data.get("facts", [])
if isinstance(facts_data, list) and facts_data:
    ranked_facts = sorted(
        (f for f in facts_data if isinstance(f, dict) and isinstance(f.get("content"), str) and f.get("content").strip()),
        key=lambda fact: _coerce_confidence(fact.get("confidence"), default=0.0),
```

`backend/packages/harness/deerflow/agents/middlewares/memory_middleware.py:86-96`

```python
# Queue the filtered conversation for memory update
correction_detected = detect_correction(filtered_messages)
reinforcement_detected = not correction_detected and detect_reinforcement(filtered_messages)
queue = get_memory_queue()
queue.add(
    thread_id=thread_id,
```

**分析**

没有实现传统 RAG/知识库/向量检索：

- 未发现向量库选型：没有 Chroma/FAISS/pgvector/Qdrant/Milvus/Weaviate 初始化。
- 未发现 chunker：上传文件会进入 thread data/uploads，并可能转换 markdown，但未读到 upload → chunk → embed → index pipeline。
- 未发现 retriever 工具：没有 retriever/RAG tool 被注册给 agent。
- 文档明确说 TF-IDF/context-aware retrieval 只是 planned/not merged，见 `MEMORY_IMPROVEMENTS.md:13-18`。

实际存在的是“长期 memory”：

- storage 是 JSON file provider，可被 `storage_class` 扩展，默认 `FileMemoryStorage`，见 `storage.py:62-95`、`storage.py:181-216`。
- memory 结构是 user summaries、history summaries、facts 列表，见 `storage.py:24-40`。
- 每次 agent after_agent 后，MemoryMiddleware 过滤 user/final assistant，加入 memory queue，异步 LLM 总结更新，见 `memory_middleware.py:24-32`、`memory_middleware.py:75-96`。
- 注入时不是语义检索，而是按 confidence 排序 facts，在 token budget 内塞进 system prompt，见 `memory/prompt.py:256-317`。
- graph 节点层面没有单独 RAG node；它基于 LangChain middleware 和 lead agent prompt 注入，不是传统 graph retriever node。

## 议题 5：数据模型 + 存储层

**读过的文件**（每个 file:line_range）

- `backend/packages/harness/deerflow/agents/checkpointer/async_provider.py:1-106`，async checkpointer backend。
- `backend/packages/harness/deerflow/agents/checkpointer/provider.py:1-191`，sync checkpointer backend，已定位。
- `backend/packages/harness/deerflow/runtime/store/async_provider.py:1-113`，async LangGraph Store backend。
- `backend/packages/harness/deerflow/runtime/store/provider.py:1-188`，sync Store backend。
- `backend/packages/harness/deerflow/config/checkpointer_config.py:1-46`，checkpointer config schema，已定位。
- `backend/app/gateway/routers/threads.py:1-520`，thread API、Store namespace、checkpoint state。
- `backend/app/gateway/services.py:174-335`，run 创建时 upsert thread、title 从 checkpointer 同步到 Store。
- `backend/packages/harness/deerflow/runtime/runs/manager.py:1-210`，in-memory RunManager。
- `backend/packages/harness/deerflow/agents/memory/storage.py:1-216`，memory JSON 存储。
- 全局搜索 SQLAlchemy/alembic，未发现业务 ORM/migration 层。

**关键代码片段**

`backend/packages/harness/deerflow/agents/checkpointer/async_provider.py:51-61`

```python
if config.type == "sqlite":
    try:
        from langgraph.checkpoint.sqlite.aio import AsyncSqliteSaver
    except ImportError as exc:
        raise ImportError(SQLITE_INSTALL) from exc

    conn_str = resolve_sqlite_conn_str(config.connection_string or "store.db")
```

`backend/packages/harness/deerflow/runtime/store/async_provider.py:1-8`

```python
"""Async Store factory — backend mirrors the configured checkpointer.

The store and checkpointer share the same ``checkpointer`` section in
*config.yaml* so they always use the same persistence backend:

- ``type: memory``   → :class:`langgraph.store.memory.InMemoryStore`
```

`backend/app/gateway/routers/threads.py:31-32`

```python
THREADS_NS: tuple[str, ...] = ("threads",)
"""Namespace used by the Store for thread metadata records."""
```

`backend/app/gateway/routers/threads.py:148-156`

```python
async def _store_get(store, thread_id: str) -> dict | None:
    """Fetch a thread record from the Store; returns ``None`` if absent."""
    item = await store.aget(THREADS_NS, thread_id)
    return item.value if item is not None else None

async def _store_put(store, record: dict) -> None:
```

`backend/app/gateway/services.py:216-223`

```python
ckpt_config = {"configurable": {"thread_id": thread_id, "checkpoint_ns": ""}}
ckpt_tuple = await checkpointer.aget_tuple(ckpt_config)
if ckpt_tuple is None:
    return

channel_values = ckpt_tuple.checkpoint.get("channel_values", {})
title = channel_values.get("title")
```

**分析**

数据库选型：

- checkpointer 支持 memory、SQLite、Postgres，见 `checkpointer/async_provider.py:6`、`checkpointer/async_provider.py:45-78`。
- Store 支持 memory、SQLite、Postgres，并且“mirrors configured checkpointer”，即共用 config 的 `checkpointer` section 和 connection string，见 `runtime/store/async_provider.py:1-8`、`runtime/store/async_provider.py:88-113`。
- 没有 SQLAlchemy model、Pydantic ORM model 或 Alembic migration。持久化交给 LangGraph checkpoint/store packages。
- 业务 memory 是 JSON file，不进入 Store/DB，见 `memory/storage.py:90-95`、`memory/storage.py:146-174`。

所有表/模型层面：

- 没有显式表定义，所以无法列出 SQL table DDL。LangGraph checkpointer/store 的内部表由 `saver.setup()` / `store.setup()` 创建，见 `checkpointer/async_provider.py:59-60`、`runtime/store/async_provider.py:59-61`、`runtime/store/async_provider.py:74-76`。
- Gateway 的 thread metadata 是 Store namespace `("threads",)` 里的 dict，字段包括 `thread_id,status,created_at,updated_at,metadata,values`，见 `threads.py:159-190`。
- thread state/messages/sources/citations 不在业务 ORM 表里；messages 等在 LangGraph checkpoint `channel_values` 里，`get_thread` 会从 checkpoint 读取并 `serialize_channel_values`，见 `threads.py:494-505`。
- runs 是 `RunManager` in-memory dict，不持久化，状态字段见 `RunRecord`，`runtime/runs/manager.py:20-38`。
- users、tasks、sources、citations 未发现独立业务表/模型。subagent background tasks 也是进程内 dict，见议题 6/7。

checkpointer 与业务库关系：

- checkpointer 和 Store 共用同一个 `checkpointer` 配置后端，但语义上分层：checkpointer 保存 LangGraph state/checkpoints；Store 保存 thread metadata index，见 `runtime/store/async_provider.py:1-8`。
- `services.py` 说明 title 先由 TitleMiddleware 写入 checkpointer channel_values，再 fire-and-forget 同步到 Store record，见 `services.py:190-236`。
- 这意味着业务 thread 列表依赖 Store，但状态真源仍在 checkpointer。

迁移工具：

- 未发现 Alembic。SQLite/Postgres schema 由 LangGraph saver/store `setup()` 负责。

## 议题 6：Subagent / Skills 体系

**读过的文件**（每个 file:line_range）

- `backend/packages/harness/deerflow/tools/builtins/task_tool.py:1-252`，subagent tool 调用、轮询、事件回流。
- `backend/packages/harness/deerflow/subagents/executor.py:1-611`，subagent 状态机、线程池、执行、超时、取消。
- `backend/packages/harness/deerflow/subagents/config.py:1-40`，SubagentConfig，已定位。
- `backend/packages/harness/deerflow/subagents/registry.py:1-98`，subagent 注册与可用性过滤，已定位。
- `backend/packages/harness/deerflow/subagents/builtins/general_purpose.py:1-30`，general-purpose built-in config，已定位。
- `backend/packages/harness/deerflow/subagents/builtins/bash_agent.py:1-52`，bash built-in config，已定位。
- `backend/packages/harness/deerflow/agents/middlewares/subagent_limit_middleware.py:1-120`，max_concurrent_subagents 限制，已定位。
- `backend/packages/harness/deerflow/agents/lead_agent/agent.py:262-267`、`lead_agent/agent.py:280-357`，subagent_enabled 和 max_concurrent_subagents 注入。
- `backend/packages/harness/deerflow/skills/loader.py:1-103`，skills 扫描。
- `backend/packages/harness/deerflow/skills/parser.py:1-80`，SKILL.md frontmatter 解析。
- `backend/packages/harness/deerflow/skills/manager.py:1-159`，custom skills 管理和历史。
- `backend/packages/harness/deerflow/agents/lead_agent/prompt.py:560-599`，skills progressive loading prompt。

**关键代码片段**

`backend/packages/harness/deerflow/tools/builtins/task_tool.py:22-30`

```python
@tool("task", parse_docstring=True)
async def task_tool(
    runtime: ToolRuntime[ContextT, ThreadState],
    description: str,
    prompt: str,
    subagent_type: str,
    tool_call_id: Annotated[str, InjectedToolCallId],
```

`backend/packages/harness/deerflow/subagents/executor.py:26-35`

```python
class SubagentStatus(Enum):
    """Status of a subagent execution."""

    PENDING = "pending"
    RUNNING = "running"
    COMPLETED = "completed"
    FAILED = "failed"
```

`backend/packages/harness/deerflow/subagents/executor.py:72-80`

```python
# Thread pool for background task scheduling and orchestration
_scheduler_pool = ThreadPoolExecutor(max_workers=3, thread_name_prefix="subagent-scheduler-")

# Thread pool for actual subagent execution (with timeout support)
# Larger pool to avoid blocking when scheduler submits execution tasks
_execution_pool = ThreadPoolExecutor(max_workers=3, thread_name_prefix="subagent-exec-")
```

`backend/packages/harness/deerflow/skills/parser.py:30-39`

```python
# Extract YAML front-matter block between leading ``---`` fences.
front_matter_match = re.match(r"^---\s*\n(.*?)\n---\s*\n", content, re.DOTALL)
if not front_matter_match:
    return None

front_matter_text = front_matter_match.group(1)
```

`backend/packages/harness/deerflow/agents/lead_agent/prompt.py:560-565`

```python
**Progressive Loading Pattern:**
1. When a user query matches a skill's use case, immediately call `read_file` on the skill's main file using the path attribute provided in the skill tag below
2. Read and understand the skill's workflow and instructions
3. The skill file contains references to external resources under the same folder
4. Load referenced resources only when needed during execution
```

**分析**

子 agent 触发机制：

- lead agent 只有在 runtime `subagent_enabled=True` 时暴露 `task` 工具，见 `tools.py:87-90`、`lead_agent/agent.py:291-356`。
- 模型调用 `task(description,prompt,subagent_type,max_turns)`，`task_tool` 验证 subagent type，提取父 runtime 的 sandbox_state/thread_data/thread_id/parent_model/trace_id，见 `task_tool.py:62-106`。
- `task_tool` 获取父 agent 可用工具并继承 `tool_groups`，但 `subagent_enabled=False` 防递归，见 `task_tool.py:107-116`。
- `SubagentExecutor` 根据 config allowlist/denylist 过滤工具，创建独立 `create_agent(...)`，thinking disabled，使用 shared runtime middleware，见 `executor.py:160-185`。
- subagent 初始 state 只含 `HumanMessage(content=task)`，并透传 sandbox/thread_data，见 `executor.py:187-206`。

结果回流：

- executor 后台跑 `agent.astream(... stream_mode="values")`，捕获 AIMessage 到 `result.ai_messages`，见 `executor.py:260-295`。
- `task_tool` 每 5 秒轮询 `_background_tasks`，用 `get_stream_writer()` 发 `task_started/task_running/task_completed/task_failed/task_timed_out` custom events，并把最终结果以工具返回字符串 `Task Succeeded. Result: ...` 回主 agent，见 `task_tool.py:141-198`。
- 这不是创建真正的 LangGraph subgraph；它是后台线程池中的独立 agent 执行，再通过 custom stream event + ToolMessage result 回流。

并发控制：

- executor 线程池硬编码 3 个 scheduler、3 个 execution、3 个 isolated-loop workers，见 `executor.py:72-80`。
- 可配置 `max_concurrent_subagents` 在 lead agent runtime configurable 里，`SubagentLimitMiddleware(max_concurrent=...)` 限制同一 AI response 里的并行 task calls，见 `lead_agent/agent.py:262-267`。
- prompt 也注入“最多 n 个 task calls per response”的行为提示，见 `lead_agent/prompt.py:685-699`。
- 线程池 3 和 middleware max_concurrent 不是同一个机制；前者是执行资源硬上限，后者是模型输出层截断/限制。

Skills 体系：

- skills 根目录是 repo root 的 `skills/`，扫描 `public` 和 `custom`，递归查 `SKILL.md`，跳过 hidden dirs，见 `skills/loader.py:11-22`、`skills/loader.py:60-79`。
- `SKILL.md` 必须有 YAML frontmatter，至少 `name` 和 `description`，见 `skills/parser.py:12-76`。
- enabled 状态来自 `extensions_config.json` 的 `skills` map，默认 public/custom 都启用，见 `extensions_config.py:183-197`。
- prompt 只列 skill name/description/path，真正内容由 agent 在匹配时用 `read_file` 渐进读取，见 `lead_agent/prompt.py:560-599`。
- 自定义 skill 支持 `references/templates/scripts/assets` 等支持目录，manager 限制路径和历史，见 `skills/manager.py:19-20`、`skills/manager.py:84-104`、`skills/manager.py:127-148`。

和 Claude Code sub-agent dispatch 最大不同：

- Claude Code 的 sub-agent dispatch 通常是模型外的编排器明确 spawn/wait/close 子代理，并可并行返回给主控集成；deer-flow 是把 dispatch 暴露成 LLM tool `task`，由主模型自己决定何时调用。
- deer-flow subagent 的结果最终仍是工具调用结果和 custom events，不是主运行时原生 agent tree，也没有独立用户可交互的 subagent session。
- deer-flow 强制 subagent 不再拥有 `task` 工具，避免递归；Claude Code 类系统通常可由编排器决定是否允许多级 delegation。
- deer-flow 的 skills 是 prompt + read_file progressive loading，不是像当前 Codex/Claude Code 技能那样由宿主在 turn 开始前根据触发规则读取并强制执行。

## 议题 7：长时任务 / 调度 / 队列

**读过的文件**（每个 file:line_range）

- `backend/packages/harness/deerflow/runtime/runs/manager.py:1-210`，RunManager in-memory 状态机、cancel、multitask strategy。
- `backend/packages/harness/deerflow/runtime/runs/worker.py:1-381`，后台 agent task、stream bridge、rollback。
- `backend/packages/harness/deerflow/runtime/runs/schemas.py:1-21`，RunStatus/DisconnectMode。
- `backend/app/gateway/services.py:239-369`，HTTP run 创建、SSE disconnect cancel/continue。
- `backend/packages/harness/deerflow/runtime/stream_bridge/memory.py:1-120`，SSE bridge replay buffer，已定位。
- `backend/packages/harness/deerflow/subagents/executor.py:465-611`，subagent background task 状态机、超时、取消、清理。
- `backend/packages/harness/deerflow/tools/builtins/task_tool.py:128-252`，subagent polling timeout、parent cancellation cleanup。
- `backend/packages/harness/deerflow/agents/memory/queue.py:1-220`，memory debounce queue，已定位。
- 全局搜索 Celery/RQ/APScheduler，未发现相关依赖或实现。

**关键代码片段**

`backend/packages/harness/deerflow/runtime/runs/schemas.py:6-14`

```python
class RunStatus(StrEnum):
    """Lifecycle status of a single run."""

    pending = "pending"
    running = "running"
    success = "success"
    error = "error"
    timeout = "timeout"
```

`backend/packages/harness/deerflow/runtime/runs/manager.py:111-123`

```python
async with self._lock:
    record = self._runs.get(run_id)
    if record is None:
        return False
    if record.status not in (RunStatus.pending, RunStatus.running):
        return False
    record.abort_action = action
    record.abort_event.set()
```

`backend/packages/harness/deerflow/runtime/runs/worker.py:71-84`

```python
# Snapshot the latest pre-run checkpoint so rollback can restore it.
if checkpointer is not None:
    try:
        config_for_check = {"configurable": {"thread_id": thread_id, "checkpoint_ns": ""}}
        ckpt_tuple = await checkpointer.aget_tuple(config_for_check)
        if ckpt_tuple is not None:
            ckpt_config = getattr(ckpt_tuple, "config", {}).get("configurable", {})
```

`backend/packages/harness/deerflow/runtime/runs/worker.py:154-164`

```python
# 7. Stream using graph.astream
if len(lg_modes) == 1 and not stream_subgraphs:
    # Single mode, no subgraphs: astream yields raw chunks
    single_mode = lg_modes[0]
    async for chunk in agent.astream(graph_input, config=runnable_config, stream_mode=single_mode):
        if record.abort_event.is_set():
```

`backend/packages/harness/deerflow/subagents/executor.py:499-520`

```python
# Submit execution to execution pool with timeout
# Pass result_holder so execute() can update it in real-time
execution_future: Future = _execution_pool.submit(self.execute, task, result_holder)
try:
    # Wait for execution with timeout
    exec_result = execution_future.result(timeout=self.config.timeout_seconds)
```

**分析**

没有 Celery/RQ/APScheduler。长时任务分两套：

- Gateway run：`asyncio.Task` + in-memory `RunManager` + `StreamBridge`。
- Subagent task：global dict `_background_tasks` + ThreadPoolExecutor + 轮询。

Run 状态机：

- `RunStatus` 是 `pending/running/success/error/timeout/interrupted`，见 `schemas.py:6-14`。
- 创建 run 时 `RunManager.create_or_reject()` 先查同 thread inflight；`reject` 抛 409，`interrupt/rollback` 先 cancel inflight 再创建，见 `manager.py:126-189`。
- worker 开始时 set running，正常结束 success，异常 error，取消 interrupted；rollback action 则先设置 error，再尝试恢复 pre-run checkpoint，见 `worker.py:67-204`。
- `timeout` enum 存在，但在读到的 worker 主链中未看到 run-level 超时处理，主要 timeout 在 subagent。

重启/断点续跑：

- checkpoint 持久化依赖 LangGraph checkpointer，支持 SQLite/Postgres 时可跨进程保存 thread state。
- RunManager 自身 in-memory，不跨重启保存 run status 或正在运行的 task，见 `manager.py:40-45`。
- `worker.py` 支持 interrupt_before/after、checkpoint history、rollback to pre-run snapshot；这提供状态恢复基础，但不是完整 job queue resume。
- 断点“续跑”更多由 LangGraph checkpoint state 和 API resume/interrupt 配合完成，不是 Celery 式 durable queue。

取消机制：

- HTTP SSE disconnect 根据 `on_disconnect` 决定 cancel 或 continue，见 `services.py:338-369`。
- `RunManager.cancel(action="interrupt"|"rollback")` 设置 abort_event 并 `task.cancel()`，见 `manager.py:101-124`。
- worker 捕获 `asyncio.CancelledError`，如果 action=rollback，则恢复 pre-run checkpoint；否则 interrupted，见 `worker.py:205-223`。
- subagent cancel 是 cooperative：设置 `cancel_event`，`_aexecute` 只在 `agent.astream` 迭代边界检查，长工具调用不能强杀，见 `executor.py:250-272`、`executor.py:535-550`。

超时处理：

- subagent execution pool `future.result(timeout=config.timeout_seconds)`，超时后标记 `TIMED_OUT`，设置 cancel_event，并 `execution_future.cancel()`，见 `executor.py:499-520`。
- `task_tool` 另有 polling safety net：`timeout_seconds + 60`，每 5 秒检查，见 `task_tool.py:136-214`。
- run-level 主 worker 未发现显式 overall timeout。

队列：

- Memory queue 是 debounce 后异步更新 memory 的内部队列，不是通用任务队列。
- Subagent background task dict 是内存队列/状态表，不持久化。
- StreamBridge 是内存事件缓冲，支持 Last-Event-ID replay，但不是 durable log。

## 最终总评

**deer-flow 最独特的 3 个设计**

- Config-driven tools/providers + runtime filtering：工具和模型都用 import path 解析，agent 运行时按 `tool_groups`、vision、subagent、MCP deferred、host bash 安全过滤，锚点 `tools.py:35-63`、`lead_agent/agent.py:350-356`。
- Middleware-first agent runtime：sandbox/thread_data/uploads/tool errors/memory/title/subagent limit/loop detection 都是 LangChain agent middleware，不是散落在 ReAct loop 里，锚点 `lead_agent/agent.py:205-277`、`tool_error_handling_middleware.py:68-125`。
- LangGraph checkpointer + Store 双层持久化：checkpoint 保存真实 state，Store 保存 thread index，并做 lazy migration/title sync，锚点 `runtime/store/async_provider.py:1-8`、`threads.py:317-400`、`services.py:190-236`。

**agents-hive 最该抄的 3 件事（带 file:line 锚点）**

- 抄工具错误显式化：DDG 空结果返回 `{"error":"No results found"}`，工具异常统一转 `ToolMessage(status="error")`，锚点 `ddg_search/tools.py:77-78`、`tool_error_handling_middleware.py:22-35`。这直接修 agents-hive `DDG 200 + 空正则 = 静默成功`。
- 抄工具可见性过滤而不是全量塞工具：按 `group`、host bash、vision、subagent_enabled、MCP deferred 来控制工具上下文，锚点 `tools.py:35-63`、`tools.py:102-132`、`lead_agent/agent.py:350-356`。这缓解 agents-hive 每轮全量工具无 tool_choice。
- 抄流式 tool_call 处理路径：embedded client 在 `messages` mode 同时处理 text、tool_calls、ToolMessage、usage，不只看文本，锚点 `client.py:620-638`、`client.py:657-668`。这对应 agents-hive 已累积 tool_call chunk 但主回调只看文本的问题。

**确认不抄的 2 件事 + 原因**

- 不抄“subagent 线程池 + 内存 `_background_tasks`”作为生产 durable 任务系统：它简单有效，但进程重启丢 task，且线程无法强杀长工具调用，只能 cooperative cancel，锚点 `executor.py:68-80`、`executor.py:535-550`。agents-hive 若要长时任务，应该做 durable job store 或 Go context cancellation + worker supervision。
- 不抄“memory 当 RAG”：deer-flow 当前 memory 是 JSON summaries/facts + confidence 排序注入，文档明确 TF-IDF/context retrieval 还没合并，锚点 `MEMORY_IMPROVEMENTS.md:13-18`、`memory/prompt.py:256-317`。agents-hive 如果需要知识库，应直接实现 chunk/embed/index/retriever，而不是把 memory 注入误称为 RAG。
```
