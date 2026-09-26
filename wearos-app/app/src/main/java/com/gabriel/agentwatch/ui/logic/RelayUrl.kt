package com.gabriel.agentwatch.ui.logic

import java.net.URI

/**
 * Cleans a typed or dictated relay address: no spaces (voice input adds them around dots), no
 * trailing slash, lowercase scheme and host, and `https://` when no scheme was given.
 */
fun normalizeRelayUrl(input: String): String {
    val compact = input.filterNot { it.isWhitespace() }.trimEnd('/')
    if (compact.isEmpty()) return ""
    val withScheme = if ("://" in compact) compact else "https://$compact"
    val scheme = withScheme.substringBefore("://").lowercase()
    val rest = withScheme.substringAfter("://")
    val host = rest.substringBefore('/').lowercase()
    val path = rest.substring(host.length)
    return "$scheme://$host$path"
}

/** HTTPS with a host; plain HTTP only in debug builds (the emulator's local relay). */
fun isAcceptableRelayUrl(url: String, allowCleartext: Boolean): Boolean {
    val uri = try {
        URI(url)
    } catch (_: Exception) {
        return false
    }
    val schemeOk = uri.scheme == "https" || (allowCleartext && uri.scheme == "http")
    return schemeOk && !uri.host.isNullOrBlank()
}

/** The relay as one short line: host and port, without the scheme or a trailing slash. */
fun displayHost(url: String): String = url.substringAfter("://").trimEnd('/')
