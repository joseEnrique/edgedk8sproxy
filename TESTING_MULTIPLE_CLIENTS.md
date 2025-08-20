# 🧪 Testing Guide - Multiple Clients with Subdomain Routing

## **📋 Arquitectura de Múltiples Agentes**

### **Flujo:**
```
Usuario → agent1.localhost:8080 → Servidor → Agente1 → FORWARD_HOST:FORWARD_PORT
Usuario → agent2.localhost:8080 → Servidor → Agente2 → FORWARD_HOST:FORWARD_PORT
Usuario → agent3.localhost:8080 → Servidor → Agente3 → FORWARD_HOST:FORWARD_PORT
```

### **Subdominios:**
- **`agent1.localhost`** → Routes to agent1
- **`agent2.localhost`** → Routes to agent2
- **`agent3.localhost`** → Routes to agent3
- **`localhost`** (sin subdominio) → Status page

## **🚀 Quick Start con Múltiples Agentes**

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
👥 Clients endpoint: http://localhost:8080/agents
🌐 Subdomain routing: agent1.localhost, agent2.localhost, etc.
🔌 TCP tunnel server will listen for client connections
🔌 TCP tunnel server listening on port 8081
```

### **2. Start Multiple Clients**

**Terminal 2 - Client1:**
```bash
cd agent
export AGENT_ID=agent1
export FORWARD_HOST=192.168.1.2
export FORWARD_PORT=6443
go build -o agent .
./agent
```

**Terminal 3 - Client2:**
```bash
cd client
export AGENT_ID=agent2
export FORWARD_HOST=192.168.1.2
export FORWARD_PORT=6443
go build -o agent .
./agent
```

**Terminal 4 - Client3:**
```bash
cd client
export AGENT_ID=agent3
export FORWARD_HOST=192.168.1.2
export FORWARD_PORT=6443
go build -o agent .
./agent
```

### **3. Verify All Clients Connected**
```bash
# Check server status
curl http://localhost:8080/status

# Check connected agents
curl http://localhost:8080/agents
```

## **🧪 Testing Subdomain Routing**

### **A. Test agent1.localhost**
```bash
# HTTP request to agent1
curl -H "Host: agent1.localhost" http://localhost:8080/api/v1/pods

# Expected: Request routed to agent1 via TCP tunnel
```

### **B. Test agent2.localhost**
```bash
# HTTP request to agent2
curl -H "Host: agent2.localhost" http://localhost:8080/api/v1/pods

# Expected: Request routed to agent2 via TCP tunnel
```

### **C. Test agent3.localhost**
```bash
# HTTP request to agent3
curl -H "Host: agent3.localhost" http://localhost:8080/api/v1/pods

# Expected: Request routed to agent3 via TCP tunnel
```

### **D. Test no subdomain (localhost)**
```bash
# Request to root (no subdomain)
curl http://localhost:8080/

# Expected: Shows status page
```

## **🔧 Configuración de Entorno para Múltiples Agentes**

### **Client1:**
```bash
export CLIENT_ID=agent1
export FORWARD_HOST=192.168.1.2
export FORWARD_PORT=6443
```

### **Client2:**
```bash
export AGENT_ID=agent2
export FORWARD_HOST=192.168.1.2
export FORWARD_PORT=6443
```

### **Client3:**
```bash
export AGENT_ID=agent3
export FORWARD_HOST=192.168.1.2
export FORWARD_PORT=6443
```

### **Archivo .env para cada cliente:**

**agent1/.env:**
```bash
CLIENT_ID=agent1
FORWARD_HOST=192.168.1.2
FORWARD_PORT=6443
```

**agent2/.env:**
```bash
CLIENT_ID=agent2
FORWARD_HOST=192.168.1.2
FORWARD_PORT=6443
```

**agent3/.env:**
```bash
CLIENT_ID=agent3
FORWARD_HOST=192.168.1.2
FORWARD_PORT=6443
```

## **🌐 Subdomain Routing Examples**

### **1. SSH to specific client:**
```bash
# SSH to agent1
ssh -p 8081 agent1.localhost

# SSH to agent2
ssh -p 8081 agent2.localhost

# SSH to agent3
ssh -p 8081 agent3.localhost
```

### **2. HTTP to specific client:**
```bash
# HTTP to agent1
curl -H "Host: agent1.localhost" http://localhost:8080/api/v1/pods

# HTTP to agent2
curl -H "Host: agent2.localhost" http://localhost:8080/api/v1/pods

# HTTP to agent3
curl -H "Host: agent3.localhost" http://localhost:8080/api/v1/pods
```

### **3. kubectl to specific client:**
```bash
# kubectl to agent1
kubectl port-forward pod/nginx 8081:80

# kubectl to agent2
kubectl port-forward pod/nginx 8082:80

# kubectl to agent3
kubectl port-forward pod/nginx 8083:80
```

## **🔍 What to Look For**

### **Server Logs:**
```
🔌 TCP client connection accepted from 127.0.0.1:xxxxx
🔌 Client agent1 connected via TCP tunnel
✅ Created new TCP client: agent1
📡 Starting TCP tunnel for client: agent1

🔌 TCP client connection accepted from 127.0.0.1:xxxxx
🔌 Client agent2 connected via TCP tunnel
✅ Created new TCP client: agent2
📡 Starting TCP tunnel for client: agent2

🌐 Routing request for subdomain 'agent1' to client 'agent1'
📡 Forwarding HTTP request to client agent1: GET /api/v1/pods
✅ HTTP request forwarded to client agent1
```

### **Client Logs:**
```
# Client1:
✅ Connected to server localhost:8081
✅ Client identification sent: agent1
📡 TCP tunnel active

# Client2:
✅ Connected to server localhost:8081
✅ Client identification sent: agent2
📡 TCP tunnel active

# Client3:
✅ Connected to server localhost:8081
✅ Client identification sent: agent3
📡 TCP tunnel active
```

## **📊 Expected Results**

### **✅ Success Indicators:**
- Server shows "TCP tunnel server listening on port 8081"
- Multiple agents connect successfully
- `curl http://localhost:8080/agents` shows all agents
- Subdomain routing works for each client
- Each client forwards to the same target independently

### **❌ Failure Indicators:**
- "Address already in use" errors
- "Connection refused" errors
- Clients stuck on "Connecting to server"
- Subdomain routing not working
- Clients not shown in `/agents` endpoint

## **🔧 Advanced Testing**

### **Load Testing Multiple Clients:**
```bash
# Send requests to all agents simultaneously
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
# Kill agent2
pkill -f "CLIENT_ID=agent2"

# Test that agent2.localhost returns 404
curl -H "Host: agent2.localhost" http://localhost:8080/api/v1/pods
# Expected: 404 Client 'agent2' not found
```

## **🎯 Use Cases**

### **1. Multiple Kubernetes Clusters:**
- **agent1** → Cluster A (192.168.1.2:6443)
- **agent2** → Cluster B (192.168.1.3:6443)
- **agent3** → Cluster C (192.168.1.4:6443)

### **2. Different Services:**
- **agent1** → Kubernetes API (192.168.1.2:6443)
- **agent2** → SSH Server (192.168.1.2:22)
- **agent3** → HTTP Server (192.168.1.2:80)

### **3. Load Balancing:**
- **agent1** → Server A (192.168.1.2:6443)
- **agent2** → Server B (192.168.1.2:6443)
- **agent3** → Server C (192.168.1.2:6443)

## **🔮 Próximos Pasos**

1. **Implementar balanceo de carga** entre clientes
2. **Agregar autenticación** por subdominio
3. **Implementar rate limiting** por cliente
4. **Agregar métricas** por cliente
5. **Implementar failover** automático

## **🎉 Resultado Final:**

- ✅ **Múltiples clientes** conectados simultáneamente
- ✅ **Routing por subdominio** (agent1.localhost, agent2.localhost, etc.)
- ✅ **Cada cliente** forward independiente al mismo target
- ✅ **Arquitectura escalable** para muchos clientes
- ✅ **Fácil identificación** de cada cliente por subdominio

¡Ahora tienes un **TCP tunnel inverso multi-cliente** con routing por subdominio! 🚀
