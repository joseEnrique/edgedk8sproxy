#!/bin/bash

# Multiplexer Client Startup Script

echo "🚀 Starting Multiplexer Client..."

# Check if we're in the right directory
if [ ! -f "client.go" ]; then
    echo "❌ Error: Please run this script from the client directory"
    exit 1
fi

# Check if .env file exists
if [ -f ".env" ]; then
    echo "📋 Loading environment from .env file"
    export $(cat .env | grep -v '^#' | xargs)
else
    echo "⚠️  No .env file found, using default configuration"
fi

# Check dependencies
echo "📦 Checking dependencies..."
go mod tidy

# Display configuration
echo "📋 Client Configuration:"
echo "   Server URL: ${CTRL_URL:-wss://127.0.0.1:443/tunnel}"
echo "   Subdomain:  ${SUBDOMAIN:-cliente1}"
echo "   Local Host: ${LOCAL_HOST:-localhost}"
echo "   Local Port: ${LOCAL_PORT:-3000}"

# Check if local service is accessible
if [ -n "$LOCAL_HOST" ] && [ -n "$LOCAL_PORT" ]; then
    echo "🔍 Checking local service availability..."
    if nc -z "$LOCAL_HOST" "$LOCAL_PORT" 2>/dev/null; then
        echo "✅ Local service is accessible"
    else
        echo "⚠️  Local service may not be running on $LOCAL_HOST:$LOCAL_PORT"
        echo "   Make sure your local service is started before running the client"
    fi
fi

echo "🚀 Starting client..."
go run client.go 