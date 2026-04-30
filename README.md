# llm_prompt_cli

通用 LLM 提示词工具。将输入内容与提示词组合，调用 OpenAI 兼容 API，流式输出结果到终端。

## 安装

```bash
go build -o llm_prompt_cli .
```

## 配置

默认加载当前目录 `.env` 文件，也可通过 `--config` 指定：

```
baseURL=https://your-api-endpoint/v1
apiKey=your-api-key
model=your-model-name
headers=X-Custom-Header=value,X-Another-Header=another-value
```

| 变量 | 必填 | 说明 |
|------|------|------|
| `baseURL` | 是 | OpenAI 兼容 API 地址 |
| `apiKey` | 是 | API 密钥 |
| `model` | 否 | 模型名称，默认 `glm-4.7` |
| `headers` | 否 | 自定义 HTTP 请求头，多个用逗号分隔，格式 `Key=Value` |

## 使用

输入内容默认拼接到提示词后面发送给 LLM。

### 基本用法

```bash
# --input-file 指定文件 + --prompt 指定提示词
llm_prompt_cli --input-file config.yaml --prompt "分析安全风险"

# --input 直接输入文本 + --prompt 指定提示词
llm_prompt_cli --input "hello world" --prompt "翻译为中文"

# --stdin 从管道读取 + --prompt 指定提示词
cat main.py | llm_prompt_cli --stdin --prompt "代码审查"

# --prompt-file 从文件读取提示词
llm_prompt_cli --input-file main.py --prompt-file prompt.txt

# --config 指定配置文件
llm_prompt_cli --input "test" --prompt "分析" --config /path/to/config.env
```

### 输出重定向

结果输出到 stdout，错误信息输出到 stderr：

```bash
llm_prompt_cli --input-file main.py --prompt "分析" > report.txt 2> error.log
```

## 参数

| 参数 | 必填 | 说明 |
|------|------|------|
| `--prompt <text>` | 二选一 | 直接指定提示词 |
| `--prompt-file <path>` | 二选一 | 从文件读取提示词 |
| `--input <text>` | 三选一 | 直接指定输入文本 |
| `--input-file <path>` | 三选一 | 从文件读取输入内容 |
| `--stdin` | 三选一 | 从标准输入（管道）读取 |
| `--config <path>` | 否 | 配置文件路径，默认 `.env` |
