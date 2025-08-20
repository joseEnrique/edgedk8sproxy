#!/bin/bash

echo "🔄 Testing Agent EOF Reconnection"
echo "================================="

# Kill existing processes
pkill -f "server\|agent" 2>/dev/null
sleep 2

# Start server
echo "🌐 Starting server..."
cd server
go build -o server . 2>/dev/null
TLS_CERT_FILE=../certs/server/server.crt \
TLS_KEY_FILE=../certs/server/server.key \
TLS_CLIENT_CA_FILE=../certs/ca/ca.crt \
./server &

SERVER_PID=$!
echo "Server PID: $SERVER_PID"
cd ..

sleep 3

# Start agent
echo "🔗 Starting agent..."
cd agent
TLS_CERT_FILE=../certs/client/client.crt \
TLS_KEY_FILE=../certs/client/client.key \
TLS_SERVER_CA_FILE=../certs/ca/ca.crt \
./agent &

AGENT_PID=$!
echo "Agent PID: $AGENT_PID"
cd ..

echo ""
echo "✅ System started! Watch the logs..."
echo ""
echo "🧪 Testing sequence:"
echo "1. Wait 5 seconds for initial connection"
sleep 5

echo "2. Kill server to trigger EOF..."
kill $SERVER_PID
echo "   💀 Server killed - watch agent logs for EOF and reconnection attempts"

sleep 5

echo "3. Restart server..."
cd server
TLS_CERT_FILE=../certs/server/server.crt \
TLS_KEY_FILE=../certs/server/server.key \
TLS_CLIENT_CA_FILE=../certs/ca/ca.crt \
./server &

NEW_SERVER_PID=$!
echo "   🌐 Server restarted with PID: $NEW_SERVER_PID"
cd ..

echo ""
echo "🔍 Expected logs in agent:"
echo "   '🔌 Connection error: unexpected EOF'"
echo "   '🔌 Connection lost, triggering reconnection: connection lost: ...'"
echo "   '❌ Connection failed with EOF (attempt X): ...'"
echo "   '🔄 Auto-reconnecting in 2s...'"
echo "   '✅ Reconnected successfully after X failures'"
echo ""
echo "⏰ Waiting 10 seconds to observe reconnection..."
sleep 10

echo ""
echo "🧪 Testing connection after reconnection:"
curl -s http://agent1.localhost:8081 -o /dev/null && echo "✅ Connection successful!" || echo "❌ Connection failed"

echo ""
echo "🛑 Cleanup:"
echo "kill $AGENT_PID $NEW_SERVER_PID"
