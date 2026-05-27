#!/bin/sh
set -e

echo "Running database migrations..."
./migrate up

if [ "$#" -gt 0 ]; then
  exec "$@"
fi

echo "Starting server..."
exec ./server
