#!/bin/bash

# Script simple para probar rápidamente el tunnel
SERVER_URL="http://agent1.localhost:8081"

echo "🧪 SIMPLE TUNNEL TEST"
echo "===================="

# Test básico de conectividad
echo "1. Testing basic connectivity..."
if curl -s $SERVER_URL > /dev/null; then
    echo "✅ Basic connection works"
else
    echo "❌ Basic connection failed"
    exit 1
fi

# Test de velocidad simple
echo ""
echo "2. Testing response time (single request)..."
curl -w "Time: %{time_total}s\n" -s $SERVER_URL -o /dev/null

# Test de 10 requests secuenciales
echo ""
echo "3. Testing 10 sequential requests..."
for i in {1..10}; do
    curl -w "Request $i: %{time_total}s\n" -s $SERVER_URL -o /dev/null
done

# Test de 10 requests simultáneos
echo ""
echo "4. Testing 10 concurrent requests..."
start_time=$(date +%s.%3N)
for i in {1..1000}; do
    curl -s $SERVER_URL -o /dev/null &
done
wait
end_time=$(date +%s.%3N)
duration=$(echo "$end_time - $start_time" | bc -l)
echo "✅ 10 concurrent requests completed in: ${duration}s"

echo ""
echo "🎉 Simple test completed!"
