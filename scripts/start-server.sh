#!/bin/bash

# Multiplexer Server Startup Script

echo "🚀 Starting Multiplexer Server..."

# Check if we're in the right directory
if [ ! -f "server.go" ]; then
    echo "❌ Error: Please run this script from the server directory"
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

# Check SSL certificates
if [ -z "$CERT_FILE" ] || [ -z "$KEY_FILE" ]; then
    echo "⚠️  SSL certificates not configured, server will not start"
    echo "   Please set CERT_FILE and KEY_FILE environment variables"
    exit 1
fi

if [ ! -f "$CERT_FILE" ] || [ ! -f "$KEY_FILE" ]; then
    echo "❌ SSL certificates not found:"
    echo "   CERT_FILE: $CERT_FILE"
    echo "   KEY_FILE: $KEY_FILE"
    echo "   Please generate SSL certificates first"
    exit 1
fi

echo "✅ SSL certificates found"
echo "🌐 Server will start on port ${PORT:-443}"
echo "📡 WebSocket endpoint: wss://localhost:${PORT:-443}/tunnel"
echo "📋 Clients list: https://localhost:${PORT:-443}/clients"

# Start the server
echo "🚀 Starting server..."
go run server.go 