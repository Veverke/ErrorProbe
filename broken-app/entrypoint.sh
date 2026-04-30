#!/bin/sh
i=0
while true; do
  i=$((i + 1))
  echo "[INFO]  broken-app: processing request $i"
  sleep 1
  echo "[WARN]  broken-app: slow response on request $i (took 980ms)"
  sleep 1
  echo "[ERROR] broken-app: database connection lost on request $i"
  sleep 1
  echo "[INFO]  broken-app: retrying request $i"
  sleep 1
  echo "[ERROR] broken-app: panic: nil pointer dereference on request $i"
  sleep 2
done
