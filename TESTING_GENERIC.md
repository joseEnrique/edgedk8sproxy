# 🧪 Testing Guide - Generic TCP Forwarding

## **📋 Arquitectura Genérica Simplificada**

### **Flujo:**
```
Usuario → Servidor (Puerto 8081) → Cliente → FORWARD_HOST:FORWARD_PORT
```

**El servidor NO detecta protocolos, solo reenvía TODO el tráfico TCP al cliente.**

## **🚀 Quick Start**

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

### **2. Start Client**
```bash
cd client
export CLIENT_ID=client1
export FORWARD_HOST=192.168.1.2
export FORWARD_PORT=6443
go build -o client .
./client
```

**Expected output:**
```
🚀 Starting TCP tunnel client
🔌 Connecting to server: localhost:8081
🆔 Client ID: client1
🎯 Forwarding all traffic to: 192.168.1.2:6443
✅ Connected to server localhost:8081
📡 Starting TCP tunnel
✅ Client identification sent: client1
📡 TCP tunnel active
```

## **🧪 Testing Generic TCP Forwarding**

### **A. SSH Test (Puerto 8081)**
```bash
# Conectar SSH directamente al puerto 8081
ssh -p 8081 localhost

# Expected: SSH traffic forwarded to client, then to FORWARD_HOST:FORWARD_PORT
```

### **B. HTTP Test (Puerto 8081)**
```bash
# Conectar HTTP directamente al puerto 8081
curl -v telnet://localhost:8081

# Expected: HTTP traffic forwarded to client, then to FORWARD_HOST:FORWARD_PORT
```

### **C. kubectl Test (Puerto 8081)**
```bash
# Conectar kubectl directamente al puerto 8081
kubectl port-forward pod/nginx 8081:80

# Expected: kubectl traffic forwarded to client, then to FORWARD_HOST:FORWARD_PORT
```

### **D. Any TCP Test (Puerto 8081)**
```bash
# Enviar cualquier dato TCP
echo "Hello World" | nc localhost 8081

# Expected: All TCP traffic forwarded to client, then to FORWARD_HOST:FORWARD_PORT
```

## **🔍 What to Look For**

### **Server Logs:**
```
🔌 TCP connection accepted from 127.0.0.1:xxxxx
📡 User traffic detected, forwarding to client
📡 Handling user traffic (X bytes)
✅ Routing user traffic to client: client1
📡 Starting bidirectional forwarding between user and client client1
📤 Forwarding X bytes from user to client client1
📥 Forwarding X bytes from client client1 to user
```

### **Client Logs:**
```
✅ Connected to server localhost:8081
✅ Client identification sent: client1
📡 TCP tunnel active
📤 Received X bytes from server
📤 Forwarding X bytes to target 192.168.1.2:6443
✅ Created new connection to target 192.168.1.2:6443
✅ Data forwarded to target
📤 Received X bytes from target, forwarding to tunnel
```

## **⚙️ Configuration**

### **Variables de Entorno:**
```bash
# Conexión al servidor
SERVER_HOST=localhost
SERVER_PORT=8081
CLIENT_ID=client1

# Target único para TODO el tráfico
FORWARD_HOST=192.168.1.2    # IP del target
FORWARD_PORT=6443           # Puerto del target
```

## **🎯 Cómo Funciona Ahora:**

### **1. Cliente se conecta:**
- Cliente se conecta al puerto 8081
- Envía su ID (ej: "client1")
- Servidor lo registra como cliente activo

### **2. Usuario se conecta:**
- Usuario se conecta al puerto 8081
- Servidor detecta que NO es un cliente (no envía ID)
- Servidor lo trata como "user traffic"
- Servidor encuentra un cliente disponible
- Servidor reenvía TODO el tráfico al cliente

### **3. Cliente reenvía:**
- Cliente recibe tráfico del servidor
- Cliente lo reenvía a FORWARD_HOST:FORWARD_PORT
- Cliente recibe respuesta del target
- Cliente envía respuesta de vuelta al servidor
- Servidor envía respuesta al usuario

## **🧪 Testing Scenarios**

### **1. Target es Kubernetes API:**
```bash
export FORWARD_HOST=192.168.1.2
export FORWARD_PORT=6443

# Test: ssh -p 8081 localhost
# Result: SSH traffic → Servidor → Cliente → Kubernetes API
```

### **2. Target es SSH Server:**
```bash
export FORWARD_HOST=192.168.1.2
export FORWARD_PORT=22

# Test: ssh -p 8081 localhost
# Result: SSH traffic → Servidor → Cliente → SSH Server
```

### **3. Target es HTTP Server:**
```bash
export FORWARD_HOST=192.168.1.2
export FORWARD_PORT=80

# Test: curl telnet://localhost:8081
# Result: HTTP traffic → Servidor → Cliente → HTTP Server
```

## **🎉 Ventajas de la Nueva Arquitectura:**

- ✅ **Completamente genérica** - funciona con cualquier protocolo
- ✅ **Sin detección de protocolo** - solo forward directo
- ✅ **Más simple** - menos código, menos bugs
- ✅ **Más eficiente** - sin overhead de análisis
- ✅ **Más flexible** - cualquier tráfico TCP funciona

## **🔧 Troubleshooting**

### **Client not receiving traffic:**
```bash
# Check if client is connected
curl http://localhost:8080/clients

# Check server logs for "User traffic detected"
# Check client logs for "Received X bytes from server"
```

### **No response from target:**
```bash
# Check if target is reachable
nc -z 192.168.1.2 6443

# Check client logs for target connection
# Verify FORWARD_HOST and FORWARD_PORT
```

## **🎯 Resultado Final:**

- ✅ **Cliente inicia** conexión TCP al servidor
- ✅ **Servidor mantiene** tunnel abierto
- ✅ **Usuario se conecta** al puerto 8081
- ✅ **Servidor reenvía TODO** al cliente (sin detectar protocolo)
- ✅ **Cliente reenvía TODO** a FORWARD_HOST:FORWARD_PORT
- ✅ **Funciona con cualquier protocolo** (SSH, HTTP, kubectl, etc.)

¡Ahora tienes un **TCP tunnel inverso completamente genérico** que reenvía todo sin complicaciones! 🚀
