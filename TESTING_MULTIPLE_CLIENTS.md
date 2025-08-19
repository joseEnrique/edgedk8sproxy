# 🧪 Testing Guide - Multiple Clients with Subdomain Routing

## **📋 Arquitectura de Múltiples Clientes**

### **Flujo:**
```
Usuario → client1.localhost:8080 → Servidor → Cliente1 → FORWARD_HOST:FORWARD_PORT
Usuario → client2.localhost:8080 → Servidor → Cliente2 → FORWARD_HOST:FORWARD_PORT
Usuario → client3.localhost:8080 → Servidor → Cliente3 → FORWARD_HOST:FORWARD_PORT
```

### **Subdominios:**
- **`client1.localhost`** → Routes to client1
- **`client2.localhost`** → Routes to client2
- **`client3.localhost`** → Routes to client3
- **`localhost`** (sin subdominio) → Status page

## **🚀 Quick Start con Múltiples Clientes**

### **1. Compile and Start Server**
```bash
cd server
go build -o server .
./server
```

**Expected output:**
```
🚀 Starting HTTP server on port 8080
📊 Status endpoint: http://localhost:8080/status
👥 Clients endpoint: http://localhost:8080/clients
🌐 Subdomain routing: client1.localhost, client2.localhost, etc.
🔌 TCP tunnel server will listen for client connections
🔌 TCP tunnel server listening on port 8081
```

### **2. Start Multiple Clients**

**Terminal 2 - Client1:**
```bash
cd client
export CLIENT_ID=client1
export FORWARD_HOST=192.168.1.2
export FORWARD_PORT=6443
go build -o client .
./client
```

**Terminal 3 - Client2:**
```bash
cd client
export CLIENT_ID=client2
export FORWARD_HOST=192.168.1.2
export FORWARD_PORT=6443
go build -o client .
./client
```

**Terminal 4 - Client3:**
```bash
cd client
export CLIENT_ID=client3
export FORWARD_HOST=192.168.1.2
export FORWARD_PORT=6443
go build -o client .
./client
```

### **3. Verify All Clients Connected**
```bash
# Check server status
curl http://localhost:8080/status

# Check connected clients
curl http://localhost:8080/clients
```

## **🧪 Testing Subdomain Routing**

### **A. Test client1.localhost**
```bash
# HTTP request to client1
curl -H "Host: client1.localhost" http://localhost:8080/api/v1/pods

# Expected: Request routed to client1 via TCP tunnel
```

### **B. Test client2.localhost**
```bash
# HTTP request to client2
curl -H "Host: client2.localhost" http://localhost:8080/api/v1/pods

# Expected: Request routed to client2 via TCP tunnel
```

### **C. Test client3.localhost**
```bash
# HTTP request to client3
curl -H "Host: client3.localhost" http://localhost:8080/api/v1/pods

# Expected: Request routed to client3 via TCP tunnel
```

### **D. Test no subdomain (localhost)**
```bash
# Request to root (no subdomain)
curl http://localhost:8080/

# Expected: Shows status page
```

## **🔧 Configuración de Entorno para Múltiples Clientes**

### **Client1:**
```bash
export CLIENT_ID=client1
export FORWARD_HOST=192.168.1.2
export FORWARD_PORT=6443
```

### **Client2:**
```bash
export CLIENT_ID=client2
export FORWARD_HOST=192.168.1.2
export FORWARD_PORT=6443
```

### **Client3:**
```bash
export CLIENT_ID=client3
export FORWARD_HOST=192.168.1.2
export FORWARD_PORT=6443
```

### **Archivo .env para cada cliente:**

**client1/.env:**
```bash
CLIENT_ID=client1
FORWARD_HOST=192.168.1.2
FORWARD_PORT=6443
```

**client2/.env:**
```bash
CLIENT_ID=client2
FORWARD_HOST=192.168.1.2
FORWARD_PORT=6443
```

**client3/.env:**
```bash
CLIENT_ID=client3
FORWARD_HOST=192.168.1.2
FORWARD_PORT=6443
```

## **🌐 Subdomain Routing Examples**

### **1. SSH to specific client:**
```bash
# SSH to client1
ssh -p 8081 client1.localhost

# SSH to client2
ssh -p 8081 client2.localhost

# SSH to client3
ssh -p 8081 client3.localhost
```

### **2. HTTP to specific client:**
```bash
# HTTP to client1
curl -H "Host: client1.localhost" http://localhost:8080/api/v1/pods

# HTTP to client2
curl -H "Host: client2.localhost" http://localhost:8080/api/v1/pods

# HTTP to client3
curl -H "Host: client3.localhost" http://localhost:8080/api/v1/pods
```

### **3. kubectl to specific client:**
```bash
# kubectl to client1
kubectl port-forward pod/nginx 8081:80

# kubectl to client2
kubectl port-forward pod/nginx 8082:80

# kubectl to client3
kubectl port-forward pod/nginx 8083:80
```

## **🔍 What to Look For**

### **Server Logs:**
```
🔌 TCP client connection accepted from 127.0.0.1:xxxxx
🔌 Client client1 connected via TCP tunnel
✅ Created new TCP client: client1
📡 Starting TCP tunnel for client: client1

🔌 TCP client connection accepted from 127.0.0.1:xxxxx
🔌 Client client2 connected via TCP tunnel
✅ Created new TCP client: client2
📡 Starting TCP tunnel for client: client2

🌐 Routing request for subdomain 'client1' to client 'client1'
📡 Forwarding HTTP request to client client1: GET /api/v1/pods
✅ HTTP request forwarded to client client1
```

### **Client Logs:**
```
# Client1:
✅ Connected to server localhost:8081
✅ Client identification sent: client1
📡 TCP tunnel active

# Client2:
✅ Connected to server localhost:8081
✅ Client identification sent: client2
📡 TCP tunnel active

# Client3:
✅ Connected to server localhost:8081
✅ Client identification sent: client3
📡 TCP tunnel active
```

## **📊 Expected Results**

### **✅ Success Indicators:**
- Server shows "TCP tunnel server listening on port 8081"
- Multiple clients connect successfully
- `curl http://localhost:8080/clients` shows all clients
- Subdomain routing works for each client
- Each client forwards to the same target independently

### **❌ Failure Indicators:**
- "Address already in use" errors
- "Connection refused" errors
- Clients stuck on "Connecting to server"
- Subdomain routing not working
- Clients not shown in `/clients` endpoint

## **🔧 Advanced Testing**

### **Load Testing Multiple Clients:**
```bash
# Send requests to all clients simultaneously
for i in {1..3}; do
    curl -H "Host: client$i.localhost" http://localhost:8080/api/v1/pods &
done
wait
```

### **Different Forwarding Targets:**
```bash
# Client1 forwards to Kubernetes
export FORWARD_HOST=192.168.1.2
export FORWARD_PORT=6443

# Client2 forwards to SSH
export FORWARD_HOST=192.168.1.3
export FORWARD_PORT=22

# Client3 forwards to HTTP
export FORWARD_HOST=192.168.1.4
export FORWARD_PORT=80
```

### **Client Disconnection Test:**
```bash
# Kill client2
pkill -f "CLIENT_ID=client2"

# Test that client2.localhost returns 404
curl -H "Host: client2.localhost" http://localhost:8080/api/v1/pods
# Expected: 404 Client 'client2' not found
```

## **🎯 Use Cases**

### **1. Multiple Kubernetes Clusters:**
- **client1** → Cluster A (192.168.1.2:6443)
- **client2** → Cluster B (192.168.1.3:6443)
- **client3** → Cluster C (192.168.1.4:6443)

### **2. Different Services:**
- **client1** → Kubernetes API (192.168.1.2:6443)
- **client2** → SSH Server (192.168.1.2:22)
- **client3** → HTTP Server (192.168.1.2:80)

### **3. Load Balancing:**
- **client1** → Server A (192.168.1.2:6443)
- **client2** → Server B (192.168.1.2:6443)
- **client3** → Server C (192.168.1.2:6443)

## **🔮 Próximos Pasos**

1. **Implementar balanceo de carga** entre clientes
2. **Agregar autenticación** por subdominio
3. **Implementar rate limiting** por cliente
4. **Agregar métricas** por cliente
5. **Implementar failover** automático

## **🎉 Resultado Final:**

- ✅ **Múltiples clientes** conectados simultáneamente
- ✅ **Routing por subdominio** (client1.localhost, client2.localhost, etc.)
- ✅ **Cada cliente** forward independiente al mismo target
- ✅ **Arquitectura escalable** para muchos clientes
- ✅ **Fácil identificación** de cada cliente por subdominio

¡Ahora tienes un **TCP tunnel inverso multi-cliente** con routing por subdominio! 🚀
