package com.gabriel.agentwatch.mobile.ui.logic

import com.gabriel.agentwatch.model.HostInfo
import com.gabriel.agentwatch.model.SessionOptionsResponse

/** The hosts that can start a session right now: the bridge is connected and its herdr is up. */
fun sessionHosts(hosts: List<HostInfo>): List<HostInfo> = hosts.filter { it.online && it.herdr_online }

/** A host's selectable agent kinds: trimmed, blank-free and unique, in the bridge's order. */
fun sessionKinds(options: SessionOptionsResponse?): List<String> =
    options?.kinds.orEmpty().map { it.trim() }.filter { it.isNotEmpty() }.distinct()

/**
 * The name proposed for a session in [path]: the folder's own name, so "…/agent-watch" starts an
 * "agent-watch" session. Falls back to "session" for a root, and for hosts whose path separator the
 * phone does not share (the bridge uses the last label either way).
 */
fun defaultSessionLabel(path: String?): String {
    val trimmed = path.orEmpty().trim().trimEnd('/')
    val name = trimmed.substringAfterLast('/').trim()
    return name.ifBlank { "session" }
}

/** Whether the form has everything the bridge needs, and is not already sending it. */
fun canCreateSession(host: String?, kind: String?, cwd: String?, creating: Boolean): Boolean =
    !creating && !host.isNullOrBlank() && !kind.isNullOrBlank() && !cwd.isNullOrBlank()
