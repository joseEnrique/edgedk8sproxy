# Arquitectura del Sistema Multiplexer

## 🏗️ Visión General

Este sistema resuelve el problema de **acceso a clusters Kubernetes sin conexiones inbound** mediante un túnel WebSocket bidireccional.

## 🔄 Flujo de Datos CORRECTO

```
┌─────────────┐    kubectl port-forward    ┌─────────────┐
│   kubectl   │ ─────────────────────────► │   Cliente   │
│  (Local)    │     puerto local           │  (Cluster)  │
│             │     (ej: 8081)             │             │
└─────────────┘                             └─────────────┘
       │                                           │
       │ HTTP Request                              │ HTTP Request
       ▼                                           ▼
┌─────────────┐                 ┌─────────────────┐
│   Servidor  │◄────────────────┤   Internet      │
│  (Proxy)    │   WebSocket     │  (Usuarios)     │
└─────────────┘                 └─────────────────┘
```

## 📡 Componentes del Sistema

### 1. **Servidor (Puerto 8080)**
- **Puerto 8080**: WebSocket para clientes + HTTP para requests
- **Endpoint kubectl**: `POST /kubectl` para solicitar conexiones
- **Funcionalidad**: Conecta a puertos locales de kubectl

### 2. **Cliente (Cluster Kubernetes)**
- **Conexión**: WebSocket persistente al servidor
- **Funcionalidad**: Recibe requests de kubectl a través del servidor
- **Estado**: Sin conexiones inbound, solo outbound

### 3. **kubectl**
- **Comando**: `kubectl port-forward --address 0.0.0.0 --port 8081 pod/nginx 80:80`
- **Puerto**: Se abre **localmente** en la máquina del usuario
- **Protocolo**: HTTP/TCP streaming local

## 🔌 Flujo Detallado CORRECTO

### **Fase 1: Establecimiento de Conexión**
```
1. Cliente se conecta al servidor via WebSocket (puerto 8080)
2. Usuario ejecuta kubectl port-forward en puerto local (ej: 8081)
3. Usuario solicita al servidor que se conecte al puerto local
4. Servidor se conecta al puerto local de kubectl
```

### **Fase 2: Túnel de Datos**
```
1. Usuario hace request HTTP al puerto local de kubectl
2. kubectl procesa y envía al cluster
3. Cliente recibe datos a través del WebSocket
4. Cliente responde y datos fluyen de vuelta
```

### **Fase 3: Streaming Continuo**
```
- Conexión WebSocket permanece abierta
- Datos fluyen bidireccionalmente
- Ping/pong mantiene conexión activa
- Timeouts automáticos para conexiones inactivas
```

## 🎯 Casos de Uso Soportados

| Comando kubectl | Descripción | Protocolo |
|-----------------|-------------|-----------|
| `kubectl exec` | Ejecutar comandos en pods | WebSocket + TCP |
| `kubectl attach` | Conectar a contenedores | WebSocket + TCP |
| `kubectl port-forward` | Forward de puertos | WebSocket + TCP |
| `kubectl cp` | Copiar archivos | WebSocket + TCP |
| `kubectl logs -f` | Seguir logs | WebSocket + TCP |

## 🔧 Configuración

### **Servidor**
```bash
PORT=8080                    # WebSocket endpoint
KUBECTL_PROXY_PORT=8081      # kubectl proxy endpoint
MAX_CLIENTS=100             # Máximo clientes concurrentes
READ_TIMEOUT=120            # Timeout para operaciones kubectl
```

### **Cliente**
```bash
SERVER_URL=ws://server:8080/ws
CLIENT_ID=my-cluster
RECONNECT_DELAY=5
MAX_RETRIES=-1              # Reconexión infinita
```

### **kubectl**
```bash
kubectl port-forward --address 0.0.0.0 --port 8081 pod/nginx 80:80
```

## 🚀 Ventajas de esta Arquitectura

### ✅ **Para Clusters sin Inbound**
- **Sin necesidad** de abrir puertos en el cluster
- **Conexión outbound** única al servidor
- **Túnel seguro** a través de WebSocket

### ✅ **Para kubectl**
- **Transparente**: funciona igual que conexión directa
- **Eficiente**: streaming nativo sin overhead
- **Compatible**: soporta todos los comandos streaming

### ✅ **Para el Sistema**
- **Escalable**: múltiples clientes simultáneos
- **Robusto**: reconexión automática y timeouts
- **Monitoreable**: logs detallados y métricas

## 🔍 Debugging y Monitoreo

### **Logs del Servidor**
```
🔌 kubectl proxy listening on port 8081
🔌 kubectl connection from 192.168.1.100:12345
📡 kubectl connection established from 192.168.1.100:12345
✅ kubectl connection from 192.168.1.100:12345 completed
```

### **Logs del Cliente**
```
🔌 Connecting to ws://server:8080/ws?id=my-cluster
✅ Connected successfully!
📨 Sent message: {"type": "register", "payload": {...}}
💓 Sent heartbeat
```

### **Métricas Disponibles**
- Número de clientes conectados
- Bytes transferidos (entrada/salida)
- Tiempo de conexión
- Estado de health check

## 🚨 Consideraciones de Seguridad

### **Autenticación**
- Cliente ID único para cada cluster
- Posibilidad de agregar username/password
- Rate limiting por cliente

### **Encriptación**
- WebSocket sobre TLS (WSS)
- Certificados SSL/TLS
- Validación de origen de conexiones

### **Aislamiento**
- Cada cliente tiene su propio túnel
- No hay cross-talk entre clientes
- Timeouts automáticos para conexiones inactivas

## 🔮 Futuras Mejoras

### **Funcionalidades Planificadas**
- [ ] Load balancing entre múltiples servidores
- [ ] Métricas Prometheus/Grafana
- [ ] API REST para gestión
- [ ] Plugin nativo de kubectl
- [ ] Soporte para múltiples protocolos

### **Integración Kubernetes**
- [ ] CRD para configuración de túneles
- [ ] Operator para gestión automática
- [ ] Service mesh integration
- [ ] RBAC nativo de Kubernetes
