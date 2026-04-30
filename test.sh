#!/bin/bash

BIN="./audit_one_file"
PASS=0
FAIL=0

assert_exit() {
    local expected=$1
    local desc=$2
    shift 2
    if output=$("$BIN" "$@" 2>&1); actual=$?; [ "$actual" -eq "$expected" ]; then
        echo "  PASS: $desc"
        PASS=$((PASS+1))
    else
        echo "  FAIL: $desc (expected exit $expected, got $actual)"
        FAIL=$((FAIL+1))
    fi
}

assert_stderr() {
    local desc=$1
    local pattern=$2
    shift 2
    if "$BIN" "$@" 2>&1 >/dev/null | grep -q "$pattern"; then
        echo "  PASS: $desc"
        PASS=$((PASS+1))
    else
        echo "  FAIL: $desc (expected stderr to contain '$pattern')"
        FAIL=$((FAIL+1))
    fi
}

assert_stdout() {
    local desc=$1
    local pattern=$2
    shift 2
    if "$BIN" "$@" 2>/dev/null | grep -qi "$pattern"; then
        echo "  PASS: $desc"
        PASS=$((PASS+1))
    else
        echo "  FAIL: $desc (expected stdout to contain '$pattern')"
        FAIL=$((FAIL+1))
    fi
}

# prepare test files
echo 'password = "hardcoded-secret"' > /tmp/test_input.py
echo '你是一个代码审查助手' > /tmp/test_prompt.txt

echo "=== Error handling ==="

assert_exit 1 "no arguments at all"

assert_stderr "no prompt specified" "exactly one of" --input "x"

assert_stderr "no input source" "exactly one of" --prompt "x"

assert_stderr "conflicting input sources" "exactly one of" --stdin --input-file /tmp/test_input.py --prompt "x"

assert_stderr "conflicting prompt sources" "exactly one of" --prompt "x" --prompt-file /tmp/test_prompt.txt --input "x"

assert_exit 1 "nonexistent input file" --input-file /tmp/nonexistent_xyz --prompt "test"

assert_exit 1 "nonexistent prompt file" --input "test" --prompt-file /tmp/nonexistent_xyz

echo ""
echo "=== Functional tests ==="

# stdin test needs pipe, can't use assert_stdout
if echo 'password = secret123' | "$BIN" --stdin --prompt '分析安全风险' 2>/dev/null | grep -qi "secret\|密码\|password"; then
    echo "  PASS: stdin + prompt"
    PASS=$((PASS+1))
else
    echo "  FAIL: stdin + prompt (expected stdout to contain keyword)"
    FAIL=$((FAIL+1))
fi

assert_stdout "input-file + prompt" "secret\|密码\|硬编码" --input-file /tmp/test_input.py --prompt "分析安全风险"

assert_stdout "input + prompt" "世界\|hello\|你好" --input "hello world" --prompt "翻译为中文"

assert_stdout "input-file + prompt-file" "secret\|密码\|硬编码" --input-file /tmp/test_input.py --prompt-file /tmp/test_prompt.txt

cp .env /tmp/test_custom.env
assert_stdout "config custom path" "secret\|密码" --input "password=secret" --prompt "分析" --config /tmp/test_custom.env

echo ""
echo "=== Results ==="
echo "PASS: $PASS  FAIL: $FAIL"
if [ "$FAIL" -ne 0 ]; then
    exit 1
fi
