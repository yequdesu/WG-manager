#!/bin/bash
set -euo pipefail
# DeepSeek Log Analyzer for WG-Manager
# Usage: bash analyze-logs.sh [log_file] [--ask "your question"]

API_KEY="sk-your-deepseek-api-key-here"       # ★ Replace with your DeepSeek API key
MODEL="${DEEPSEEK_MODEL:-deepseek-chat}"       # ★ Model: deepseek-chat or deepseek-reasoner
API_URL="https://api.deepseek.com/v1/chat/completions"

LOG_FILE="${1:-/var/log/wg-mgmt/wg-mgmt.log}"
QUESTION="${2:-}"
if [[ "$QUESTION" == "--ask" ]] && [[ -n "${3:-}" ]]; then
    QUESTION="${3}"
fi

if [[ ! -f "$LOG_FILE" ]]; then
    echo "Error: log file not found: $LOG_FILE"
    exit 1
fi

BOLD='\033[1m'
GREEN='\033[0;32m'
CYAN='\033[0;36m'
NC='\033[0m'

echo -e "${BOLD}${CYAN}=== WG-Manager Log Analyzer (DeepSeek) ===${NC}"
echo ""

# Collect log content (last 500 lines to stay within token limits)
LOG_CONTENT=$(tail -500 "$LOG_FILE")
LINE_COUNT=$(echo "$LOG_CONTENT" | wc -l)
echo -e "${GREEN}[+]${NC} Log: $LOG_FILE ($LINE_COUNT lines analyzed)"
echo ""

SYSTEM_PROMPT=$(cat << 'PROMPT'
You are a WireGuard VPN expert and log analyst. Analyze the provided WG-Manager log and answer in Chinese with the following structure:

1. **概览** (Overview): time range, total events by module (DAEMON/WG/HTTP)
2. **异常检测** (Anomalies): any errors, timeouts, unusual patterns, suspicious IPs
3. **连接健康度** (Connection health): handshake success rate, peer stability, endpoint changes
4. **安全提醒** (Security): unauthorized access attempts, unusual traffic patterns, port scans
5. **建议** (Recommendations): actionable steps to improve stability/security

Keep the response focused and actionable. Use markdown formatting.
PROMPT
)

if [[ -n "$QUESTION" ]]; then
    USER_MSG="Custom question: $QUESTION\n\nLog content (last $LINE_COUNT lines):\n\`\`\`\n$LOG_CONTENT\n\`\`\`"
else
    USER_MSG="Please analyze this WG-Manager log (last $LINE_COUNT lines):\n\`\`\`\n$LOG_CONTENT\n\`\`\`"
fi

# Escape JSON
escape_json() {
    python3 -c "import sys,json; print(json.dumps(sys.stdin.read()))" 2>/dev/null || \
    python3 -c "import sys,json; print(json.dumps(sys.stdin.read()))" 2>/dev/null
}

SYSTEM_JSON=$(echo "$SYSTEM_PROMPT" | escape_json)
USER_JSON=$(echo -e "$USER_MSG" | escape_json)

PAYLOAD=$(cat << EOF
{
  "model": "$MODEL",
  "messages": [
    {"role": "system", "content": $SYSTEM_JSON},
    {"role": "user", "content": $USER_JSON}
  ],
  "temperature": 0.3,
  "max_tokens": 2048
}
EOF
)

echo -e "${GREEN}[+]${NC} Sending to DeepSeek ($MODEL)..."
echo ""

if ! command -v curl &>/dev/null; then
    echo "Error: curl is required"
    exit 1
fi

RESPONSE=$(curl -sS "$API_URL" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "$PAYLOAD" 2>&1) || {
    echo "Error: API request failed"
    echo "$RESPONSE"
    exit 1
}

# Extract and display the response content
if command -v python3 &>/dev/null; then
    echo "$RESPONSE" | python3 -c "
import sys, json
try:
    data = json.load(sys.stdin)
    if 'choices' in data:
        print(data['choices'][0]['message']['content'])
    elif 'error' in data:
        print(f\"API Error: {data['error']}\")
    else:
        print(json.dumps(data, indent=2))
except:
    print(sys.stdin.read())
"
else
    echo "$RESPONSE"
fi

echo ""
echo -e "${GREEN}[+]${NC} Analysis complete"
