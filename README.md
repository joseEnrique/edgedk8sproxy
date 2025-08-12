# Reverse Proxy Tunnel

Un sistema de proxy inverso que permite a clientes conectarse a un servidor y recibir peticiones HTTP a través de un túnel WebSocket.

## 🚀 Kubernetes v1.31+ Compatible

**✅ Compatibilidad Total con Kubernetes v1.31+**

Nuestro servidor está **completamente optimizado** para la nueva versión de Kubernetes que migra de SPDY/3.1 a WebSocket streaming protocol. Esto significa:

- **Mejor rendimiento** con comandos `kubectl exec`, `kubectl attach`, `kubectl port-forward`, `kubectl cp`
- **Compatibilidad total** con proxies modernos, gateways y load balancers
- **Protocolo estándar** WebSocket en lugar del deprecado SPDY
- **Streaming nativo** para operaciones de Kubernetes

📖 [Ver guía completa de compatibilidad](KUBERNETES_V1.31_COMPATIBILITY.md)

## 🏗️ Arquitectura

```
┌─────────────────┐    WebSocket    ┌─────────────────┐
│   Cliente       │◄──────────────►│   Servidor       │
│   (Sin inbound) │                 │   (Proxy)        │
└─────────────────┘                 └─────────────────┘
         │                                    │
         │ HTTP Request                       │ HTTP Request
         ▼                                    ▼
┌─────────────────┐                 ┌─────────────────┐
│   Servicio      │                 │   Internet      │
│   Local         │                 │   (Usuarios)    │
└─────────────────┘                 └─────────────────┘
```

## 📁 Estructura del Proyecto

```
multiplexer/
├── server/
│   ├── main.go          # Servidor principal
│   ├── proxy.go         # Lógica de proxy HTTP
│   ├── client.go        # Gestión de clientes
│   ├── websocket.go     # Manejo de WebSocket
│   └── go.mod
├── client/
│   ├── main.go          # Cliente principal
│   ├── tunnel.go        # Lógica del túnel
│   ├── http.go          # Manejo de HTTP
│   └── go.mod
├── shared/
│   └── messages.go      # Tipos de mensajes compartidos
└── README.md
```

## 🚀 Cómo Funciona

### 1. Conexión del Cliente
- El cliente se conecta al servidor via WebSocket
- Se registra con un ID único
- El servidor mantiene la conexión activa

### 2. Proxy de Peticiones
- Un usuario hace una petición HTTP al servidor
- El servidor identifica el cliente por subdominio
- Reenvía la petición al cliente via WebSocket
- El cliente procesa la petición localmente
- Devuelve la respuesta al servidor
- El servidor responde al usuario

### 3. Manejo de Datos Grandes
- Streaming de datos para peticiones grandes
- Chunking automático de archivos
- Compresión opcional

## 🔧 Configuración

### Variables de Entorno - Servidor
```bash
PORT=8080                    # Puerto del servidor
DOMAIN=example.com           # Dominio base
MAX_CLIENTS=100             # Máximo número de clientes
CHUNK_SIZE=8192             # Tamaño de chunks para streaming
MAX_MESSAGE_SIZE=10485760   # Tamaño máximo de mensaje (10MB)
READ_TIMEOUT=120           # Timeout de lectura en segundos
WRITE_TIMEOUT=30          # Timeout de escritura en segundos
```

### Variables de Entorno - Cliente
```bash
SERVER_URL=ws://server:8080/ws  # URL del servidor
CLIENT_ID=myapp                 # ID único del cliente
LOCAL_HOST=localhost            # Host del servicio local
LOCAL_PORT=3000                # Puerto del servicio local
LOCAL_SCHEMA=                  # Schema (http/https) - vacío = auto-detect
MAX_MESSAGE_SIZE=10485760      # Tamaño máximo de mensaje (10MB)
READ_TIMEOUT=120              # Timeout de lectura en segundos
WRITE_TIMEOUT=30              # Timeout de escritura en segundos
RECONNECT_DELAY=5              # Delay entre reconexiones en segundos
MAX_RETRIES=-1                 # Máximo intentos de reconexión (-1 = infinito)
```

## 📋 Uso

### 1. Iniciar el Servidor
```bash
cd server
go build -o server .
./server
```

### 2. Iniciar el Cliente
```bash
cd client
go build -o client .
./client
```

### 3. Hacer Peticiones
```bash
# Petición al cliente 'myapp'
curl http://myapp.example.com/api/users

# Petición a otro cliente
curl http://otherapp.example.com/dashboard
```

## 🎯 Características

- ✅ **Conexión Bidireccional**: WebSocket persistente
- ✅ **Proxy HTTP Completo**: Headers, body, métodos HTTP
- ✅ **Transformación de Headers**: Forward-* → * (cualquier cabecera Forward-)
- ✅ **Streaming**: Soporte para archivos grandes
- ✅ **Múltiples Clientes**: Un servidor, muchos clientes
- ✅ **Subdominios**: Routing por subdominio
- ✅ **Reconexión Automática**: Cliente se reconecta automáticamente con delay fijo
- ✅ **Logging**: Logs detallados para debugging
- ✅ **Métricas**: Estadísticas de conexiones y peticiones

## 🔒 Seguridad

- Autenticación básica opcional
- Rate limiting por cliente
- Validación de subdominios
- Timeouts configurables

## 📊 Monitoreo

- Estado de conexiones en tiempo real
- Métricas de peticiones por cliente
- Logs de errores y warnings
- Dashboard web para administración 