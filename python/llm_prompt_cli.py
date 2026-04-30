#!/usr/bin/env python3
"""Python port of llm_prompt_cli — feature parity with the Go binary."""

import argparse
import json
import os
import sys
from pathlib import Path

import httpx
from dotenv import load_dotenv
from openai import OpenAI


def parse_headers(raw: str) -> dict:
    out = {}
    if not raw:
        return out
    for pair in raw.split(","):
        pair = pair.strip()
        if not pair or "=" not in pair:
            continue
        k, v = pair.split("=", 1)
        out[k.strip()] = v.strip()
    return out


def parse_bool(raw: str) -> bool:
    return raw.strip().lower() in ("1", "true", "yes", "on")


def parse_bool_default(raw: str, default: bool) -> bool:
    v = raw.strip().lower()
    if v == "":
        return default
    if v in ("1", "true", "yes", "on"):
        return True
    if v in ("0", "false", "no", "off"):
        return False
    print(f"Warning: invalid bool {v!r}, using default {default}", file=sys.stderr)
    return default


def parse_float_env(key: str):
    v = os.getenv(key)
    if v is None or v == "":
        return None
    try:
        return float(v)
    except ValueError:
        print(f"Warning: invalid {key}={v!r}, ignored", file=sys.stderr)
        return None


def read_text(path: str) -> str:
    return Path(path).read_text(encoding="utf-8")


def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(
        prog="llm_prompt_cli",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=(
            "Examples:\n"
            "  llm_prompt_cli --prompt \"翻译为中文\" --input \"Hello world\"\n"
            "  llm_prompt_cli --prompt-file prompt.txt --input-file data.txt\n"
            "  cat file.txt | llm_prompt_cli --prompt \"总结以下内容\" --stdin\n"
            "  llm_prompt_cli --prompt \"explain this\" --config /path/to/.env\n"
            "  llm_prompt_cli --prompt-json --prompt '[{\"role\":\"system\",\"content\":\"翻译为中文\"},{\"role\":\"user\",\"content\":\"Hello\"}]'\n"
            "  llm_prompt_cli --prompt-json --prompt-file messages.json"
        ),
    )
    p.add_argument("--input-file", default="", help="read input content from file")
    p.add_argument("--input", default="", help="input text directly")
    p.add_argument("--stdin", action="store_true", help="read input content from stdin (pipe)")
    p.add_argument("--prompt", default="", help="prompt text")
    p.add_argument("--prompt-file", default="", help="read prompt from file")
    p.add_argument("--config", default="", help="config file path (default: .env)")
    p.add_argument(
        "--prompt-json",
        action="store_true",
        help="treat prompt content as raw JSON messages array for the chat completion API",
    )
    return p


def main() -> int:
    parser = build_parser()
    if len(sys.argv) == 1:
        parser.print_help()
        return 0
    args = parser.parse_args()

    # validate prompt: exactly one required
    prompt_count = bool(args.prompt) + bool(args.prompt_file)
    if prompt_count != 1:
        print("Error: exactly one of --prompt, --prompt-file is required", file=sys.stderr)
        parser.print_help(sys.stderr)
        return 1

    # validate input: optional, at most one
    input_count = bool(args.input_file) + bool(args.input) + (1 if args.stdin else 0)
    if input_count > 1:
        print("Error: at most one of --input, --input-file, --stdin is allowed", file=sys.stderr)
        return 1

    env_file = args.config or ".env"
    if not load_dotenv(env_file, override=False):
        # godotenv prints a warning on missing/unreadable file; mirror that.
        if not Path(env_file).exists():
            print(f"Warning: could not load config file {env_file}: not found", file=sys.stderr)

    base_url = os.getenv("baseURL", "")
    api_key = os.getenv("apiKey", "")
    model = os.getenv("model") or "glm-4.7"

    if not api_key:
        print("Error: apiKey is not set. Check your config file.", file=sys.stderr)
        return 1
    if not base_url:
        print("Error: baseURL is not set. Check your config file.", file=sys.stderr)
        return 1

    max_tokens = 8192
    raw_max = os.getenv("maxTokens", "")
    if raw_max:
        try:
            n = int(raw_max)
            if n <= 0:
                raise ValueError
            max_tokens = n
        except ValueError:
            print(
                f"Warning: invalid maxTokens={raw_max!r}, using default {max_tokens}",
                file=sys.stderr,
            )

    temperature = parse_float_env("temperature")
    top_p = parse_float_env("topP")
    presence_penalty = parse_float_env("presencePenalty")
    frequency_penalty = parse_float_env("frequencyPenalty")

    timeout = None
    raw_timeout = os.getenv("timeout", "")
    if raw_timeout:
        try:
            n = int(raw_timeout)
            if n <= 0:
                raise ValueError
            timeout = n
        except ValueError:
            print(f"Warning: invalid timeout={raw_timeout!r}, ignored", file=sys.stderr)

    show_thinking = parse_bool(os.getenv("showThinking", ""))
    verify_tls = parse_bool_default(os.getenv("verifyTLS", ""), False)
    custom_headers = parse_headers(os.getenv("headers", ""))

    # read prompt
    if args.prompt:
        prompt_text = args.prompt
    else:
        try:
            prompt_text = read_text(args.prompt_file)
        except OSError as e:
            print(f"Error reading prompt file {args.prompt_file}: {e}", file=sys.stderr)
            return 1

    # read input (optional)
    user_input = ""
    if args.input_file:
        try:
            user_input = read_text(args.input_file)
        except OSError as e:
            print(f"Error reading input file {args.input_file}: {e}", file=sys.stderr)
            return 1
    elif args.input:
        user_input = args.input
    elif args.stdin:
        user_input = sys.stdin.read()

    # build messages
    if args.prompt_json:
        try:
            messages = json.loads(prompt_text)
        except json.JSONDecodeError as e:
            print(
                f"Error: --prompt-json requires valid JSON messages array: {e}",
                file=sys.stderr,
            )
            return 1
        if not isinstance(messages, list) or not messages:
            print("Error: --prompt-json messages array is empty", file=sys.stderr)
            return 1
    else:
        messages = [{"role": "user", "content": prompt_text}]

    if user_input:
        messages.append({"role": "user", "content": user_input})

    client_kwargs = {"api_key": api_key, "base_url": base_url}
    if custom_headers:
        client_kwargs["default_headers"] = custom_headers
    if timeout is not None:
        client_kwargs["timeout"] = timeout
    if not verify_tls:
        client_kwargs["http_client"] = httpx.Client(verify=False, timeout=timeout)
    client = OpenAI(**client_kwargs)

    req_kwargs = {
        "model": model,
        "messages": messages,
        "stream": True,
        "max_tokens": max_tokens,
    }
    if temperature is not None:
        req_kwargs["temperature"] = temperature
    if top_p is not None:
        req_kwargs["top_p"] = top_p
    if presence_penalty is not None:
        req_kwargs["presence_penalty"] = presence_penalty
    if frequency_penalty is not None:
        req_kwargs["frequency_penalty"] = frequency_penalty

    try:
        stream = client.chat.completions.create(**req_kwargs)
    except Exception as e:
        print(f"Error: failed to create chat completion stream: {e}", file=sys.stderr)
        return 1

    content_seen = False
    thinking_open = False
    try:
        for chunk in stream:
            if not chunk.choices:
                continue
            delta = chunk.choices[0].delta
            reasoning = getattr(delta, "reasoning_content", None)
            if show_thinking and reasoning:
                if not thinking_open:
                    sys.stderr.write("<think>")
                    thinking_open = True
                sys.stderr.write(reasoning)
                sys.stderr.flush()
            content = getattr(delta, "content", None)
            if content:
                if thinking_open:
                    sys.stderr.write("</think>\n")
                    sys.stderr.flush()
                    thinking_open = False
                sys.stdout.write(content)
                sys.stdout.flush()
                content_seen = True
    except Exception as e:
        if thinking_open:
            sys.stderr.write("</think>\n")
        print(f"\nError: stream error: {e}", file=sys.stderr)
        return 1

    if thinking_open:
        sys.stderr.write("</think>\n")
    if content_seen:
        sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
