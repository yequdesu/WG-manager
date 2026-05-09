#!/usr/bin/env bash

update_config_value() {
  local config_file="$1"
  local key="$2"
  local new_value="$3"
  local existing_line current_line current_value
  local temp_file line line_has_newline had_content=0 last_was_newline=0 updated=0

  [[ -f "$config_file" ]] || return 1

  existing_line="$(grep -n "^${key}=" "$config_file" 2>/dev/null | { IFS= read -r first_line; printf '%s' "$first_line"; } || true)"
  if [[ -z $existing_line ]]; then
    existing_line="$(grep -n "^${key} =" "$config_file" 2>/dev/null | { IFS= read -r first_line; printf '%s' "$first_line"; } || true)"
  fi
  if [[ -z $existing_line ]]; then
    existing_line="$(grep -n "^${key}[[:space:]]*=" "$config_file" 2>/dev/null | { IFS= read -r first_line; printf '%s' "$first_line"; } || true)"
  fi

  if [[ -n $existing_line ]]; then
    current_line="${existing_line#*:}"
    if [[ $current_line =~ ^(${key})([[:space:]]*)=([[:space:]]*)(.*)$ ]]; then
      current_value="${BASH_REMATCH[4]}"
      if [[ $current_value == "$new_value" ]]; then
        return 0
      fi
    fi
  fi

  temp_file="$(mktemp /tmp/wg-manager-config.XXXXXX)" || return 1

  while IFS= read -r line && line_has_newline=1 || { line_has_newline=0; [[ -n $line ]]; }; do
    had_content=1
    last_was_newline=$line_has_newline

    if [[ $line =~ ^(${key})([[:space:]]*)=([[:space:]]*)(.*)$ ]]; then
      printf '%s%s=%s%s' "${BASH_REMATCH[1]}" "${BASH_REMATCH[2]}" "${BASH_REMATCH[3]}" "$new_value" >> "$temp_file"
      updated=1
    else
      printf '%s' "$line" >> "$temp_file"
    fi

    if [[ $last_was_newline -eq 1 ]]; then
      printf '\n' >> "$temp_file"
    fi

  done < "$config_file"

  if [[ $updated -eq 0 ]]; then
    if [[ $had_content -eq 1 && $last_was_newline -eq 0 ]]; then
      printf '\n' >> "$temp_file"
    fi
    printf '%s=%s\n' "$key" "$new_value" >> "$temp_file"
  fi

  mv "$temp_file" "$config_file"
}

assert_file_equals() {
  local file_path="$1"
  local expected_path="$2"

  if cmp -s "$file_path" "$expected_path"; then
    return 0
  fi

  return 1
}

test_key_not_present() {
  local tmpdir file expected
  tmpdir="$(mktemp -d)" || return 1
  file="$tmpdir/config.env"
  expected="$tmpdir/expected"

  printf 'WG_PORT=51820\n' > "$file"
  update_config_value "$file" "SERVER_HOST" "new.domain.com" || { rm -rf "$tmpdir"; echo "update failed"; return 1; }
  printf 'WG_PORT=51820\nSERVER_HOST=new.domain.com\n' > "$expected"

  if ! assert_file_equals "$file" "$expected"; then
    rm -rf "$tmpdir"
    echo "content mismatch"
    return 1
  fi

  rm -rf "$tmpdir"
}

test_key_present_with_value() {
  local tmpdir file expected
  tmpdir="$(mktemp -d)" || return 1
  file="$tmpdir/config.env"
  expected="$tmpdir/expected"

  printf 'SERVER_HOST=old.domain.com\n' > "$file"
  update_config_value "$file" "SERVER_HOST" "new.domain.com" || { rm -rf "$tmpdir"; echo "update failed"; return 1; }
  printf 'SERVER_HOST=new.domain.com\n' > "$expected"

  if ! assert_file_equals "$file" "$expected"; then
    rm -rf "$tmpdir"
    echo "content mismatch"
    return 1
  fi

  rm -rf "$tmpdir"
}

test_key_present_with_spaces() {
  local tmpdir file expected
  tmpdir="$(mktemp -d)" || return 1
  file="$tmpdir/config.env"
  expected="$tmpdir/expected"

  printf 'SERVER_HOST = old.domain.com\n' > "$file"
  update_config_value "$file" "SERVER_HOST" "new.domain.com" || { rm -rf "$tmpdir"; echo "update failed"; return 1; }
  printf 'SERVER_HOST = new.domain.com\n' > "$expected"

  if ! assert_file_equals "$file" "$expected"; then
    rm -rf "$tmpdir"
    echo "content mismatch"
    return 1
  fi

  rm -rf "$tmpdir"
}

test_clear_key() {
  local tmpdir file expected
  tmpdir="$(mktemp -d)" || return 1
  file="$tmpdir/config.env"
  expected="$tmpdir/expected"

  printf 'SERVER_HOST=old.domain.com\n' > "$file"
  update_config_value "$file" "SERVER_HOST" "" || { rm -rf "$tmpdir"; echo "update failed"; return 1; }
  printf 'SERVER_HOST=\n' > "$expected"

  if ! assert_file_equals "$file" "$expected"; then
    rm -rf "$tmpdir"
    echo "content mismatch"
    return 1
  fi

  rm -rf "$tmpdir"
}

test_key_with_dots_in_value() {
  local tmpdir file expected
  tmpdir="$(mktemp -d)" || return 1
  file="$tmpdir/config.env"
  expected="$tmpdir/expected"

  printf 'SERVER_HOST=vpn.example.com\n' > "$file"
  update_config_value "$file" "SERVER_HOST" "vpn2.example.com" || { rm -rf "$tmpdir"; echo "update failed"; return 1; }
  printf 'SERVER_HOST=vpn2.example.com\n' > "$expected"

  if ! assert_file_equals "$file" "$expected"; then
    rm -rf "$tmpdir"
    echo "content mismatch"
    return 1
  fi

  rm -rf "$tmpdir"
}

test_no_trailing_newline() {
  local tmpdir file expected
  tmpdir="$(mktemp -d)" || return 1
  file="$tmpdir/config.env"
  expected="$tmpdir/expected"

  printf 'SERVER_HOST=old.domain.com' > "$file"
  update_config_value "$file" "SERVER_HOST" "new.domain.com" || { rm -rf "$tmpdir"; echo "update failed"; return 1; }
  printf 'SERVER_HOST=new.domain.com' > "$expected"

  if ! assert_file_equals "$file" "$expected"; then
    rm -rf "$tmpdir"
    echo "content mismatch"
    return 1
  fi

  rm -rf "$tmpdir"
}

test_file_not_found() {
  local tmpdir missing_file
  tmpdir="$(mktemp -d)" || return 1
  missing_file="$tmpdir/missing.env"

  if update_config_value "$missing_file" "SERVER_HOST" "new.domain.com"; then
    rm -rf "$tmpdir"
    echo "expected failure"
    return 1
  fi

  rm -rf "$tmpdir"
}

test_idempotent() {
  local tmpdir file snapshot
  tmpdir="$(mktemp -d)" || return 1
  file="$tmpdir/config.env"
  snapshot="$tmpdir/snapshot"

  printf 'WG_PORT=51820\nSERVER_HOST=old.domain.com\n' > "$file"
  update_config_value "$file" "SERVER_HOST" "new.domain.com" || { rm -rf "$tmpdir"; echo "first update failed"; return 1; }
  cp -a "$file" "$snapshot"
  update_config_value "$file" "SERVER_HOST" "new.domain.com" || { rm -rf "$tmpdir"; echo "second update failed"; return 1; }

  if ! cmp -s "$file" "$snapshot"; then
    rm -rf "$tmpdir"
    echo "file changed on second update"
    return 1
  fi

  rm -rf "$tmpdir"
}

test_comments_preserved() {
  local tmpdir file expected
  tmpdir="$(mktemp -d)" || return 1
  file="$tmpdir/config.env"
  expected="$tmpdir/expected"

  printf '# comment one\nSERVER_HOST=old.domain.com\n# comment two\nWG_PORT=51820\n' > "$file"
  update_config_value "$file" "SERVER_HOST" "new.domain.com" || { rm -rf "$tmpdir"; echo "update failed"; return 1; }
  printf '# comment one\nSERVER_HOST=new.domain.com\n# comment two\nWG_PORT=51820\n' > "$expected"

  if ! assert_file_equals "$file" "$expected"; then
    rm -rf "$tmpdir"
    echo "content mismatch"
    return 1
  fi

  rm -rf "$tmpdir"
}

main() {
  local failures=0

  run_test "Key not present" test_key_not_present || failures=$((failures + 1))
  run_test "Key present with value" test_key_present_with_value || failures=$((failures + 1))
  run_test "Key present with spaces" test_key_present_with_spaces || failures=$((failures + 1))
  run_test "Clear key" test_clear_key || failures=$((failures + 1))
  run_test "Key with dots" test_key_with_dots_in_value || failures=$((failures + 1))
  run_test "File without trailing newline" test_no_trailing_newline || failures=$((failures + 1))
  run_test "File not found" test_file_not_found || failures=$((failures + 1))
  run_test "Idempotent" test_idempotent || failures=$((failures + 1))
  run_test "Comments preserved" test_comments_preserved || failures=$((failures + 1))

  return "$failures"
}

run_test() {
  local test_name="$1"
  local test_fn="$2"
  local reason

  if reason="$($test_fn 2>&1)"; then
    printf 'PASS: %s\n' "$test_name"
  else
    reason="${reason//$'\n'/; }"
    printf 'FAIL: %s: %s\n' "$test_name" "$reason"
    return 1
  fi
}

main "$@"
