package com.gabriel.agentwatch.network

import com.gabriel.agentwatch.model.AgentKey
import com.google.gson.JsonObject
import com.google.gson.JsonParseException
import com.google.gson.JsonParser
import com.google.gson.JsonPrimitive
import java.util.LinkedHashMap

/**
 * contracts §4.1: FCM does not guarantee order. A `blocked` push whose seq is not newer than the last
 * `resolved` seen for its (host, pane) announces a prompt that was already answered: don't show it.
 */
fun shouldShowBlocked(seq: Long, lastResolved: Long?): Boolean = lastResolved == null || seq > lastResolved

/**
 * The last `resolved` seq per agent, (host, pane), kept across process restarts (a push service can be
 * killed between a `resolved` and a late `blocked`). Immutable; bounded to [maxPanes], dropping the
 * least recently recorded one. Stored under [AgentKey.token]: the pane id alone without a host.
 */
class ResolvedSeqs(
    private val maxPanes: Int = 32,
    private val seqs: LinkedHashMap<String, Long> = LinkedHashMap()
) {
    val size: Int get() = seqs.size

    fun lastFor(agent: AgentKey): Long? = seqs[agent.token]

    /** Records [seq] for [agent], keeping the highest seq seen for it. */
    fun record(agent: AgentKey, seq: Long): ResolvedSeqs {
        val id = agent.token
        val next = LinkedHashMap(seqs)
        val kept = maxOf(seq, next.remove(id) ?: seq)
        next[id] = kept
        while (next.size > maxPanes) next.remove(next.keys.first())
        return ResolvedSeqs(maxPanes, next)
    }

    fun encode(): String = JsonObject().apply { seqs.forEach { (pane, seq) -> addProperty(pane, seq) } }.toString()

    companion object {
        /** Anything unreadable decodes to empty: at worst a stale approval is shown, as before this rule. */
        fun decode(raw: String?, maxPanes: Int = 32): ResolvedSeqs {
            if (raw.isNullOrBlank()) return ResolvedSeqs(maxPanes)
            val obj = try {
                JsonParser.parseString(raw) as? JsonObject
            } catch (e: JsonParseException) {
                null
            } ?: return ResolvedSeqs(maxPanes)
            val seqs = LinkedHashMap<String, Long>()
            for ((pane, value) in obj.entrySet()) {
                val prim = value as? JsonPrimitive ?: continue
                if (prim.isNumber) seqs[pane] = prim.asLong
            }
            return ResolvedSeqs(maxPanes, seqs)
        }
    }
}
