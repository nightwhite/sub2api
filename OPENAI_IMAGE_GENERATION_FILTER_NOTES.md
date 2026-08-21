# OpenAI 禁用生图分组的工具过滤逻辑

## 背景

OpenAI Responses 请求可以同时携带多个工具，例如：

```json
{
  "tools": [
    { "type": "web_search" },
    { "type": "image_generation" }
  ],
  "tool_choice": "auto"
}
```

当 API Key 所属分组关闭生图能力时，不能因为请求体里带了 `image_generation` tool 就直接拒绝整次请求。用户可能只是复用了客户端默认 tools 配置，本次请求并不一定要生成图片。

## 核心规则

只要分组关闭生图能力，就先过滤 OpenAI Responses 请求里的生图控制参数：

- 从 `tools` 数组中移除 `type=image_generation` 的 tool。
- 如果移除后 `tools` 为空，同时删除 `tools` 和 `tool_choice`。
- 如果 `tool_choice` 明确选择 `image_generation`，删除 `tool_choice`。
- 过滤后重新判断请求是否仍是生图意图。
- 如果过滤后不再是生图意图，继续转发请求。
- 如果仍是生图意图，返回禁用生图的权限错误。

## 仍然需要拒绝的场景

以下请求不应该被过滤成普通文本请求，仍然要按分组权限拒绝：

- 使用专用生图接口，例如 `/v1/images/generations`。
- 使用生图模型，例如 `gpt-image-2`。
- 过滤 `image_generation` tool 后，请求仍被识别为生图意图。

## 需要保留的关键实现点

合并 upstream 时重点检查这些函数或等价逻辑是否还在：

- `FilterOpenAIResponsesImageGenerationControls`
  - 负责删除 `image_generation` tool。
  - 负责清理空 `tools` 和残留 `tool_choice`。
- `/v1/responses` 请求入口
  - 在分组关闭生图且检测到生图意图时，先调用过滤函数。
  - 过滤后重新计算生图意图。
  - 只有仍然是生图意图时才返回权限错误。
- OpenAI gateway service 的转发路径
  - passthrough 和普通 forward 路径都要做同样处理。
  - 重新序列化 JSON 时不要使用会把 `<`、`>`、`&` 转义为 HTML 实体的默认 encoder。

## 回归测试点

合并后至少验证这些场景：

1. 分组关闭生图，`tools=[web_search, image_generation]`，`tool_choice=auto`：应返回 200，不应返回 403。
2. 分组关闭生图，`tools=[image_generation]`：应删除空 `tools` 和 `tool_choice` 后继续普通请求。
3. 分组关闭生图，`tool_choice={"type":"image_generation"}`：应删除该 `tool_choice`。
4. 分组关闭生图，模型为 `gpt-image-2`：仍应返回 403。
5. 分组开启生图：不应过滤 `image_generation` tool。

## 线上验收命令示例

```bash
curl -sS http://codex3.gbro.site/v1/responses \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-5.5",
    "input": "请用一句话回答：接口是否可用？",
    "tools": [
      { "type": "web_search" },
      { "type": "image_generation" }
    ],
    "tool_choice": "auto"
  }'
```

预期：HTTP 200，`error=null`。
