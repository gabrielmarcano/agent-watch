package com.gabriel.agentwatch.network

import com.gabriel.agentwatch.model.AgentState
import com.gabriel.agentwatch.model.AgentsSnapshot
import com.google.gson.Gson
import kotlinx.coroutines.flow.StateFlow
import java.io.BufferedInputStream
import java.io.Closeable
import java.io.InputStream
import java.io.OutputStream
import java.net.InetAddress
import java.net.ServerSocket
import java.net.Socket
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.CopyOnWriteArrayList
import kotlin.concurrent.thread

/**
 * A minimal HTTP/1.1 relay for engine tests. Unlike MockWebServer it keeps SSE streams open until the
 * test drops them, and never writes on its own, so a silent stream is just a stream nobody writes to.
 */
class FakeRelay : Closeable {
    data class Recorded(val method: String, val path: String, val authorization: String?, val body: String)

    private val gson = Gson()
    private val serverSocket = ServerSocket(0, 50, InetAddress.getLoopbackAddress())
    private val sockets = CopyOnWriteArrayList<Socket>()
    private val streams = CopyOnWriteArrayList<OutputStream>()

    val url: String = "http://127.0.0.1:${serverSocket.localPort}"
    val requests = CopyOnWriteArrayList<Recorded>()

    @Volatile var agents: AgentsSnapshot = AgentsSnapshot(host_online = true, herdr_online = true)
    @Volatile var historyJson: String = """{"items":[]}"""
    /** Status for new `/v1/events` connections; 200 opens a stream that starts with a `snapshot` of [agents]. */
    @Volatile var eventsStatus: Int = 200
    /** Status + body per exact path, overriding the defaults (`200 {"ok":true}` for commands). */
    val responses = ConcurrentHashMap<String, Pair<Int, String>>()

    init {
        thread(isDaemon = true, name = "fake-relay-accept") {
            while (!serverSocket.isClosed) {
                val socket = try { serverSocket.accept() } catch (_: Exception) { break }
                sockets += socket
                thread(isDaemon = true, name = "fake-relay-conn") { serve(socket) }
            }
        }
    }

    fun eventsRequests(): List<Recorded> = requests.filter { it.path == "/v1/events" }
    fun openStreams(): Int = streams.size

    /** Writes raw SSE text to every open stream. */
    fun send(text: String) {
        for (out in streams) {
            try { out.write(text.toByteArray()); out.flush() } catch (_: Exception) { streams.remove(out) }
        }
    }

    fun sendAgent(agent: AgentState) = send("event: agent\ndata: ${gson.toJson(agent)}\n\n")

    /** Closes every open SSE stream (the relay restarted). */
    fun dropStreams() {
        streams.clear()
        for (s in sockets) try { s.close() } catch (_: Exception) {}
        sockets.clear()
    }

    override fun close() {
        try { serverSocket.close() } catch (_: Exception) {}
        dropStreams()
    }

    private fun serve(socket: Socket) {
        try {
            val input = BufferedInputStream(socket.getInputStream())
            val requestLine = readLine(input) ?: return
            val (method, path) = requestLine.split(" ").let { it[0] to it[1] }
            val headers = HashMap<String, String>()
            while (true) {
                val line = readLine(input) ?: return
                if (line.isEmpty()) break
                val i = line.indexOf(':')
                if (i > 0) headers[line.substring(0, i).trim().lowercase()] = line.substring(i + 1).trim()
            }
            val length = headers["content-length"]?.toIntOrNull() ?: 0
            val body = ByteArray(length).also { var read = 0; while (read < length) { val n = input.read(it, read, length - read); if (n < 0) break; read += n } }
            requests += Recorded(method, path, headers["authorization"], String(body))

            val out = socket.getOutputStream()
            val basePath = path.substringBefore('?')
            val override = responses[basePath]
            when {
                override != null -> respond(socket, override.first, override.second)
                basePath == "/v1/events" && eventsStatus == 200 -> {
                    out.write("HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nCache-Control: no-cache\r\n\r\n".toByteArray())
                    out.write("event: snapshot\ndata: ${gson.toJson(agents)}\n\n".toByteArray())
                    out.flush()
                    streams += out
                    // Keep the socket open; the test decides when it dies.
                }
                basePath == "/v1/events" -> respond(socket, eventsStatus, """{"error":{"code":"unauthorized","message":"no"}}""")
                basePath == "/v1/agents" -> respond(socket, 200, gson.toJson(agents))
                basePath == "/v1/history" -> respond(socket, 200, historyJson)
                else -> {
                    val (status, json) = responses[basePath] ?: (200 to """{"ok":true}""")
                    respond(socket, status, json)
                }
            }
        } catch (_: Exception) {
        }
    }

    private fun respond(socket: Socket, status: Int, json: String) {
        val bytes = json.toByteArray()
        val out = socket.getOutputStream()
        out.write("HTTP/1.1 $status X\r\nContent-Type: application/json\r\nContent-Length: ${bytes.size}\r\nConnection: close\r\n\r\n".toByteArray())
        out.write(bytes)
        out.flush()
        socket.close()
    }

    private fun readLine(input: InputStream): String? {
        val sb = StringBuilder()
        while (true) {
            val c = input.read()
            if (c < 0) return if (sb.isEmpty()) null else sb.toString()
            if (c == '\n'.code) return sb.toString().trimEnd('\r')
            sb.append(c.toChar())
        }
    }
}

/** In-memory credentials for engine tests. */
class FakeCredentials(
    @Volatile override var relayUrl: String,
    @Volatile override var deviceToken: String?
) : RelayCredentials {
    @Volatile var cleared = 0
    override fun clearAuth() {
        deviceToken = null
        cleared++
    }
}

/** Polls [flow] until [predicate] holds, or fails after [timeoutMs] with the last value. */
fun <T> awaitValue(flow: StateFlow<T>, timeoutMs: Long = 5_000, what: String = "condition", predicate: (T) -> Boolean): T {
    val deadline = System.currentTimeMillis() + timeoutMs
    while (System.currentTimeMillis() < deadline) {
        val v = flow.value
        if (predicate(v)) return v
        Thread.sleep(10)
    }
    throw AssertionError("Timed out waiting for $what; last value: ${flow.value}")
}

/** Polls [condition] until true, or fails after [timeoutMs]. */
fun awaitTrue(timeoutMs: Long = 5_000, what: String = "condition", condition: () -> Boolean) {
    val deadline = System.currentTimeMillis() + timeoutMs
    while (System.currentTimeMillis() < deadline) {
        if (condition()) return
        Thread.sleep(10)
    }
    throw AssertionError("Timed out waiting for $what")
}
