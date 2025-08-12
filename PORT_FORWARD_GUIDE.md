# Port-Forward Guide

## Overview

This multiplexer supports TCP port-forwarding for kubectl and other tools that need to tunnel TCP connections through the reverse proxy.

## How it works

1. **kubectl port-forward** establishes a WebSocket connection to the server
2. The server forwards the TCP tunnel request to the appropriate client
3. The client establishes a TCP connection to the local Kubernetes API
4. Data flows bidirectionally through the tunnel

## Setup

### 1. Start the server

```bash
cd server
go run .
```

The server will start on port 8080 by default.

### 2. Start the client

```bash
cd client
go run .
```

Make sure the client is configured to connect to your Kubernetes API server. The default configuration assumes:
- Kubernetes API is running on `192.168.1.2:6443`
- Client ID is `client1`

### 3. Verify connection

Check that the client is connected:

```bash
curl http://localhost:8080/clients
```

You should see your client listed.

## Using kubectl port-forward

### Method 1: Direct kubectl command

```bash
kubectl port-forward --address 0.0.0.0 --port 0 kubernetes.default.svc.cluster.local:443
```

### Method 2: Using the test script

```bash
./test-port-forward.sh
```

## Troubleshooting

### Common issues

1. **Client not connected**
   - Check that the client is running and connected to the server
   - Verify the client configuration matches your Kubernetes setup

2. **Connection refused**
   - Ensure the Kubernetes API server is running and accessible
   - Check that the client's `LOCAL_HOST` and `LOCAL_PORT` are correct

3. **Timeout errors**
   - The tunnel has a 10-second timeout for initial connection
   - Check network connectivity between client and Kubernetes API

### Debug logs

Enable verbose logging by setting the log level:

```bash
export LOG_LEVEL=debug
```

### Testing the tunnel

You can test if the tunnel is working by making a request to the Kubernetes API:

```bash
# Get the port that kubectl is using
KUBECTL_PORT=$(netstat -tlnp | grep kubectl | awk '{print $4}' | cut -d: -f2)

# Test the connection
curl -k https://localhost:$KUBECTL_PORT/api/v1/namespaces
```

## Configuration

### Client configuration

The client can be configured via environment variables:

```bash
export SERVER_URL="ws://localhost:8080/ws"
export CLIENT_ID="client1"
export LOCAL_HOST="192.168.1.2"
export LOCAL_PORT=6443
export SKIP_VERIFY=true
```

### Server configuration

The server can be configured via environment variables:

```bash
export PORT="8080"
export DOMAIN="localhost"
export MAX_CLIENTS=10
```

## Architecture

```
kubectl port-forward
       ↓
   WebSocket (binary)
       ↓
   Server (proxy.go)
       ↓
   WebSocket (binary)
       ↓
   Client (tcp.go)
       ↓
   TCP connection
       ↓
   Kubernetes API
```

The tunnel supports bidirectional streaming:
- **kubectl → Kubernetes API**: WebSocket → TCP
- **Kubernetes API → kubectl**: TCP → WebSocket

## Security considerations

1. **Authentication**: The tunnel does not add authentication - use kubectl's built-in auth
2. **Encryption**: WebSocket connections should use WSS (TLS) in production
3. **Access control**: Ensure only authorized clients can connect to the server

## Performance

- The tunnel uses binary WebSocket messages for efficient data transfer
- Buffer size is configurable (default: 4KB)
- Connection timeouts are configurable
- Automatic reconnection is supported
