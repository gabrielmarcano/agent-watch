# Agent Watch - Client Architecture (Wear OS)

Este documento describe la arquitectura del cliente de Wear OS de **Agent Watch**. Su objetivo es servir como referencia técnica y guía de diseño, especialmente útil para futuras implementaciones en otras plataformas como **WatchOS (SwiftUI)**.

## 1. Visión General
Agent Watch es una aplicación para smartwatches diseñada para interactuar con un servidor puente local (Bridge Server) que hospeda un agente de IA. La aplicación permite:
- Configurar la conexión al servidor puente y la ruta del workspace del agente.
- Monitorizar el estado actual del agente en tiempo real (Idle, Working, Error, etc.).
- Ver un historial de las acciones, comandos ejecutados y respuestas devueltas por el agente.
- Leer las respuestas del agente a pantalla completa con renderizado avanzado de Markdown.
- *(Futuro)* Enviar nuevos prompts mediante dictado por voz desde el reloj.

## 2. Pila Tecnológica (Tech Stack)
- **UI:** Jetpack Compose for Wear OS (`androidx.wear.compose`).
- **Arquitectura:** MVVM (Model-View-ViewModel).
- **Red & Streaming:** OkHttp3 + OkHttp-SSE (para Server-Sent Events).
- **Serialización:** Gson.
- **Renderizado Markdown:** `multiplatform-markdown-renderer` (Mikepenz) 100% nativo Compose.

## 3. Capas de la Aplicación (Mapeo hacia SwiftUI)

### 3.1. Capa de Interfaz de Usuario (UI Layer)
Construida de forma declarativa con Jetpack Compose. Utiliza un `SwipeToDismissBox` o Pagers de Wear OS para navegar fluidamente entre vistas.

- **`AgentScreen.kt`** (Equivalente WatchOS: `ContentView.swift`)
  - Es el orquestador principal. Observa el estado del ViewModel y decide qué vista hija mostrar (Config, Dashboard, Listado o Lector Completo).
  
- **`ServerConfigScreen.kt`** (Equivalente WatchOS: `ConfigView.swift`)
  - Interfaz sencilla con campos de texto para configurar la IP:Puerto del servidor y la ruta del workspace. Guarda esto localmente y se lo pasa al ViewModel.
  
- **`DashboardScreen.kt`** (Equivalente WatchOS: `DashboardView.swift`)
  - Muestra el estado en vivo del agente (Status indicator) y detalles de la conexión. En el futuro hospedará el botón primario de dictado por voz (Micrófono).
  
- **`HistoryListScreen.kt`** (Equivalente WatchOS: `HistoryListView.swift`)
  - Muestra el historial de interacciones. Usa `ScalingLazyColumn` (similar a un `List` o `ScrollView` con padding rotacional en WatchOS) para que los elementos se encojan en los bordes curvos de la pantalla. Muestra el texto truncado de las respuestas.
  
- **`ResponseReaderScreen.kt`** (Equivalente WatchOS: `ReaderDetailView.swift`)
  - Pantalla dedicada a leer una respuesta larga. Utiliza el parser nativo de Markdown y reemplaza la tipografía y colores para adaptarse al _Dark Mode_ de las pantallas OLED pequeñas.

### 3.2. Capa de Gestión de Estado (State Management)
- **`AgentViewModel.kt`** (Equivalente WatchOS: `AgentViewModel.swift` usando `@Observable` u `ObservableObject`)
  - Mantiene la fuente de verdad (Source of Truth) de la UI usando flujos de estado (`StateFlow`).
  - Mantiene la IP guardada, el estado de conexión actual, el estado del agente y la lista de interacciones (`HistoryItem`).
  - Lanza corrutinas (equivalente a `Task` / Swift Concurrency) para interactuar con la red sin bloquear la UI.

### 3.3. Capa de Datos y Red (Data & Networking Layer)
- **`AgentApiClient.kt` / `AgentApi.kt`** (Equivalente WatchOS: `AgentNetworkService.swift` usando `URLSession.bytes` o librerías de SSE)
  - Abstrae toda la comunicación HTTP hacia el servidor puente local.
  - Implementa **Server-Sent Events (SSE)** mediante `okhttp-sse` para recibir el stream de logs y estados del agente en vivo sin usar websockets complejos.
  
- **Modelos (`AgentState.kt`, `HistoryItem.kt`)** (Equivalente WatchOS: `structs` conformando a `Codable`)
  - Modelos de datos crudos que se parsean de las respuestas JSON del servidor usando Gson.

### 3.4. Utilidades
- **`MarkdownFormatter.kt`** (Equivalente WatchOS: Extensiones de `String` o utilidades de Regex)
  - Contiene heurísticas para limpiar etiquetas de Markdown (limpiar asteriscos, quitar backticks) y truncar el texto para mostrar previsualizaciones limpias en el `HistoryListScreen` sin la complejidad de renderizar Markdown en las listas principales.

## 4. Notas de Diseño y UX para Smartwatches
1. **Tipografía:** Se usa una jerarquía de fuentes reducida (e.g. 13sp para body, 18sp para Headers) debido a que los motores de Markdown genéricos escalan fuentes muy grandes. En WatchOS (Dynamic Type) esto requiere escalar las fuentes `.body`, `.headline`, etc., a tamaños compactos.
2. **Navegación:** Minimizar jerarquías profundas. Todo se mantiene horizontal o accesible con una capa de profundidad (Lista -> Lector).
3. **Colores:** Fondos completamente negros (True Black) para aprovechar la pantalla OLED y ahorrar batería, contrastando con tarjetas oscuras (grises oscuros o colores con baja opacidad) para la separación semántica.
4. **Scroll:** Utilizar coronas rotativas (Rotary Input en Wear OS / Digital Crown en WatchOS) en vistas de lectura y listas largas.
