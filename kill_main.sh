#!/bin/bash

# Find all processes started by `go run main.go`
PIDS=$(pgrep -f "main.go")

# Kill each process found
for pid in $PIDS; do
    kill $pid
done

echo "All processes started by 'go run main.go' have been killed."